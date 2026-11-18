package httpx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
)

func TestSanitizeDashboardJSON(t *testing.T) {
	input := `{"ok":true,"records":[{"request":{"headers":[{"name":"Authorization","value":"Bearer abcdefghijklmnopqrst"},{"name":"Accept","value":"*/*"}]}},{"password":"plain-secret-value-alpha","detail":"Bearer zzzzzzzzzzzz tail"}]}`
	got := sanitizeDashboardJSON([]byte(input))

	// 结构化字段必须脱敏。
	if strings.Contains(string(got), "abcdefghijklmnopqrst") {
		t.Fatalf("Bearer token not redacted: %s", got)
	}
	if strings.Contains(string(got), "plain-secret-value-alpha") {
		t.Fatalf("password field not redacted: %s", got)
	}
	if strings.Contains(string(got), "zzzzzzzzzzzz") {
		t.Fatalf("trailing Bearer not redacted: %s", got)
	}
	// 非敏感字段必须保留。
	if !strings.Contains(string(got), "Accept") || !strings.Contains(string(got), "*/*") {
		t.Fatalf("non-sensitive fields lost: %s", got)
	}
	// 结果仍是合法 JSON。
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	// Authorization 头的值应为 [REDACTED]，name 保留。
	records := m["records"].([]any)
	headers := records[0].(map[string]any)["request"].(map[string]any)["headers"].([]any)
	auth := headers[0].(map[string]any)
	if auth["name"] != "Authorization" || auth["value"] != "[REDACTED]" {
		t.Fatalf("auth header shaping wrong: %#v", auth)
	}
}

func TestSanitizeDashboardJSONNonJSON(t *testing.T) {
	got := sanitizeDashboardJSON([]byte("oops Bearer abcdefghijklmnop text"))
	if strings.Contains(string(got), "abcdefghijklmnop") {
		t.Fatalf("non-JSON fallback failed: %s", got)
	}
}

func TestSanitizeDashboardJSONEdgeCasesAndFastPath(t *testing.T) {
	cleanAnalytics := []byte(`{"total_calls":128,"total_input_tokens":45000,"daily_tokens":[{"date":"2026-11-18","input":1200}],"status":"ok"}`)
	gotClean := sanitizeDashboardJSON(cleanAnalytics)
	if !bytes.Equal(gotClean, cleanAnalytics) {
		t.Fatalf("clean payload altered: %s", gotClean)
	}

	cases := []struct {
		name     string
		input    string
		leaked   string
		retained string
	}{
		{
			name:     "unicode escaped key",
			input:    `{"\u0074oken":"secret-unicode-value","ok":true}`,
			leaked:   "secret-unicode-value",
			retained: `"ok":true`,
		},
		{
			name:     "case variations",
			input:    `{"X-API-Key":"k1","Proxy-Authorization":"k2","Set-Cookie":"c1","detail":"bearer xyz12345678","ok":true}`,
			leaked:   "xyz12345678",
			retained: `[REDACTED]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(sanitizeDashboardJSON([]byte(tc.input)))
			if strings.Contains(got, tc.leaked) {
				t.Fatalf("secret leaked in %q: %s", tc.name, got)
			}
			if !strings.Contains(got, tc.retained) {
				t.Fatalf("expected %q in %s", tc.retained, got)
			}
		})
	}

	pngHeader := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbinarypayload")
	if gotPNG := sanitizeDashboardJSON(pngHeader); !bytes.Equal(gotPNG, pngHeader) {
		t.Fatalf("binary PNG payload altered")
	}
}

func BenchmarkSanitizeDashboardJSONCleanAnalytics(b *testing.B) {
	payload := []byte(`{"total_calls":500,"total_input_tokens":92000,"total_output_tokens":34000,"daily_tokens":[{"date":"2026-11-18","input":12000,"output":4500}],"recent_calls":[{"id":1,"tool":"read_file","success":true}]}`)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(sanitizeDashboardJSON(payload)) == 0 {
			b.Fatal("empty output")
		}
	}
}

func BenchmarkSanitizeDashboardJSONWithSecrets(b *testing.B) {
	payload := []byte(`{"ok":true,"records":[{"headers":[{"name":"Authorization","value":"Bearer abcdefghijklmnopqrst"},{"name":"Accept","value":"*/*"}],"password":"hidden-password"}]}`)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(sanitizeDashboardJSON(payload)) == 0 {
			b.Fatal("empty output")
		}
	}
}

func TestDashboardArtifactPublishAndContentStreaming(t *testing.T) {
	cfg := testConfig(t)
	cfg.AuthToken = "dashboard-test-token"
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	mux := http.NewServeMux()
	registerDashboard(mux, cfg, runtimeAPIHandler(runtime, cfg, auth.NewOAuthStore()))

	// 1. 发布二进制载荷（含 PNG 头特征字节），确认上传 -> 列表 -> 流式预览/Range -> 下载。
	pngBytes := []byte("\x89PNG\r\n\x1a\n0123456789")
	publishBody := `{"filename":"shot.png","mime_type":"image/png","data_base64":"` + base64.StdEncoding.EncodeToString(pngBytes) + `","retention_seconds":3600}`
	pubReq := newLocalRuntimeRequest(http.MethodPost, "http://127.0.0.1:8765/dashboard/api/artifacts", strings.NewReader(publishBody))
	pubReq.Header.Set("Content-Type", "application/json")
	pubRec := httptest.NewRecorder()
	mux.ServeHTTP(pubRec, pubReq)
	if pubRec.Code != http.StatusOK {
		t.Fatalf("publish status = %d body = %s", pubRec.Code, pubRec.Body.String())
	}
	var pubResp struct {
		OK       bool `json:"ok"`
		Artifact struct {
			ArtifactID string `json:"artifact_id"`
			Filename   string `json:"filename"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(pubRec.Body.Bytes(), &pubResp); err != nil || !pubResp.OK || pubResp.Artifact.ArtifactID == "" {
		t.Fatalf("publish response = %s err=%v", pubRec.Body.String(), err)
	}
	id := pubResp.Artifact.ArtifactID

	// 2. 列表接口应返回刚发布的记录。
	listReq := newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), id) {
		t.Fatalf("list status = %d body = %s", listRec.Code, listRec.Body.String())
	}

	// 3. 内容流完整读取：保留原始二进制，inline 预览，sandbox CSP。
	contentReq := newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts/"+id+"/content", nil)
	contentRec := httptest.NewRecorder()
	mux.ServeHTTP(contentRec, contentReq)
	if contentRec.Code != http.StatusOK || !bytes.Equal(contentRec.Body.Bytes(), pngBytes) {
		t.Fatalf("content status = %d bytes = %x", contentRec.Code, contentRec.Body.Bytes())
	}
	if got := contentRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Fatalf("inline Content-Disposition = %q", got)
	}
	if got := contentRec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("expected sandbox CSP for image, got %q", got)
	}

	// 4. Range 请求应返回 206 Partial Content。
	rangeReq := newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts/"+id+"/content", nil)
	rangeReq.Header.Set("Range", "bytes=0-3")
	rangeRec := httptest.NewRecorder()
	mux.ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent || !bytes.Equal(rangeRec.Body.Bytes(), pngBytes[:4]) {
		t.Fatalf("range status = %d body = %x", rangeRec.Code, rangeRec.Body.Bytes())
	}

	// 5. ?download=1 强制 attachment。
	dlReq := newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts/"+id+"/content?download=1", nil)
	dlRec := httptest.NewRecorder()
	mux.ServeHTTP(dlRec, dlReq)
	if got := dlRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("download Content-Disposition = %q", got)
	}

	// 6. PDF 内联放开 sandbox 保留 frame-ancestors 'self'；SVG 强制 attachment + sandbox。
	pdfBody := `{"filename":"doc.pdf","mime_type":"application/pdf","data_base64":"` + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n")) + `"}`
	pdfPubReq := newLocalRuntimeRequest(http.MethodPost, "http://127.0.0.1:8765/dashboard/api/artifacts", strings.NewReader(pdfBody))
	pdfPubReq.Header.Set("Content-Type", "application/json")
	pdfPubRec := httptest.NewRecorder()
	mux.ServeHTTP(pdfPubRec, pdfPubReq)
	if err := json.Unmarshal(pdfPubRec.Body.Bytes(), &pubResp); err != nil {
		t.Fatal(err)
	}
	pdfContentReq := newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts/"+pubResp.Artifact.ArtifactID+"/content", nil)
	pdfContentRec := httptest.NewRecorder()
	mux.ServeHTTP(pdfContentRec, pdfContentReq)
	if got := pdfContentRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Fatalf("pdf Content-Disposition = %q", got)
	}
	if got := pdfContentRec.Header().Get("Content-Security-Policy"); strings.Contains(got, "sandbox") || !strings.Contains(got, "frame-ancestors 'self'") {
		t.Fatalf("pdf CSP = %q", got)
	}

	svgBody := `{"filename":"icon.svg","mime_type":"image/svg+xml","data_base64":"` + base64.StdEncoding.EncodeToString([]byte("<svg/>")) + `"}`
	svgPubReq := newLocalRuntimeRequest(http.MethodPost, "http://127.0.0.1:8765/dashboard/api/artifacts", strings.NewReader(svgBody))
	svgPubReq.Header.Set("Content-Type", "application/json")
	svgPubRec := httptest.NewRecorder()
	mux.ServeHTTP(svgPubRec, svgPubReq)
	if err := json.Unmarshal(svgPubRec.Body.Bytes(), &pubResp); err != nil {
		t.Fatal(err)
	}
	svgContentReq := newLocalRuntimeRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts/"+pubResp.Artifact.ArtifactID+"/content", nil)
	svgContentRec := httptest.NewRecorder()
	mux.ServeHTTP(svgContentRec, svgContentReq)
	if got := svgContentRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("svg Content-Disposition = %q, want attachment", got)
	}

	// 7. 非回环访问仍被 403 拒绝。
	remoteReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/dashboard/api/artifacts/"+id+"/content", nil)
	remoteReq.RemoteAddr = "192.0.2.10:54321"
	remoteRec := httptest.NewRecorder()
	mux.ServeHTTP(remoteRec, remoteReq)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d, want 403", remoteRec.Code)
	}
}
