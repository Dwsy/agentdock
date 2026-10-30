package httpx

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
)

// captureBody 是一个有上限的旁路缓冲：只留前 limit 字节，超过后继续计数但不再
// 缓存。Write 永远消费掉整个 p，否则 io.TeeReader 会把短写当成错误传给请求方。
type captureBody struct {
	limit     int
	total     int64
	buf       bytes.Buffer
	truncated bool
}

func (c *captureBody) Write(p []byte) (int, error) {
	if c == nil {
		return len(p), nil
	}
	c.total += int64(len(p))
	if remain := c.limit - c.buf.Len(); remain > 0 {
		if len(p) <= remain {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:remain])
			c.truncated = true
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

// addBytes 只记账不缓存：handler 透传的、超出抓包上限的尾部按字节计数，
// 让 bytes 仍然等于真实体积。
func (c *captureBody) addBytes(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.total += n
	if c.buf.Len() >= c.limit {
		c.truncated = true
	}
}

func (c *captureBody) snapshot() observability.CaptureMessage {
	return observability.CaptureMessage{
		Bytes:     c.total,
		Body:      c.buf.String(),
		Truncated: c.truncated,
	}
}

// captureReadCloser 保留原始 Body 的关闭语义：包装的 Reader 只包 Read，
// 直接替换 r.Body 会让 handler 关不掉底层连接。
type captureReadCloser struct {
	io.Reader
	io.Closer
}

// captureRequestBody 透传请求正文的尾部并按字节记账。前缀已经单独入账，
// 不能再走 Write，否则同一批字节会被数两次。
type captureRequestBody struct {
	src     io.ReadCloser
	capture *captureBody
}

func (r *captureRequestBody) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.capture.addBytes(int64(n))
	return n, err
}

func (r *captureRequestBody) Close() error {
	return r.src.Close()
}

// captureRequestBodyStream 先旁路读走抓包上限内的请求正文再透传。鉴权失败等
// 路径上 handler 可能根本不读 body，那正是最需要看正文的场景。
func captureRequestBodyStream(body io.ReadCloser, capture *captureBody) io.ReadCloser {
	prefix, err := io.ReadAll(io.LimitReader(body, int64(capture.limit)))
	if err != nil {
		// 读失败就退回纯旁路：宁可少抓，也不能把请求体吃掉。
		return &captureReadCloser{Reader: io.TeeReader(body, capture), Closer: body}
	}
	_, _ = capture.Write(prefix)
	return &captureReadCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), &captureRequestBody{src: body, capture: capture}),
		Closer: body,
	}
}

// captureResponseWriter 旁路响应正文，同时把 status/bytes 原样记录。
// Flush 必须透传：SSE 流式响应要逐段刷出，不能被缓冲。
type captureResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	body   *captureBody
}

func (w *captureResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	_, _ = w.body.Write(data[:n])
	return n, err
}

func (w *captureResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *captureResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// networkAPIPath 是抓包面板自身的查询端点。面板轮询会持续产生流量，
// 不排除就会把自己的轮询淹进真实请求里 —— DevTools 的 Network 面板同理。
func networkAPIPath(path string) bool {
	return strings.HasPrefix(path, "/internal/runtime/network")
}

// captureHandler 采集入站请求/响应快照写进内存环。store 为 nil 时原样返回 next，
// 采集关闭必须是零开销，不能给每条请求加一层包装。
func captureHandler(next http.Handler, store *observability.CaptureStore) http.Handler {
	if !store.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if networkAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		requestBody := &captureBody{limit: observability.CaptureBodyBytes}
		if r.Body != nil {
			r.Body = captureRequestBodyStream(r.Body, requestBody)
		}
		responseBody := &captureBody{limit: observability.CaptureBodyBytes}
		recorder := &captureResponseWriter{ResponseWriter: w, body: responseBody}
		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		requestHeaders, requestHeadersTruncated := observability.CaptureHeaders(r.Header, observability.CaptureHeaderBytes)
		responseHeaders, responseHeadersTruncated := observability.CaptureHeaders(recorder.Header(), observability.CaptureHeaderBytes)
		request := requestBody.snapshot()
		request.Headers = requestHeaders
		request.Truncated = request.Truncated || requestHeadersTruncated
		response := responseBody.snapshot()
		response.Headers = responseHeaders
		response.Bytes = recorder.bytes
		response.Truncated = response.Truncated || responseHeadersTruncated

		store.Record(observability.CaptureRecord{
			Channel:    observability.CaptureChannelHTTP,
			StartedAt:  started.UTC(),
			DurationMS: float64(time.Since(started)) / float64(time.Millisecond),
			Method:     r.Method,
			Host:       r.Host,
			Path:       r.URL.Path,
			Query:      r.URL.RawQuery,
			RawURL:     r.URL.String(),
			Proto:      r.Proto,
			RemoteAddr: r.RemoteAddr,
			UserAgent:  r.UserAgent(),
			Status:     status,
			Outcome:    observability.CaptureOutcome(status),
			Request:    request,
			Response:   response,
		})
	})
}
