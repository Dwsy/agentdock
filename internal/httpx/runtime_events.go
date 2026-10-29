package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
)

const runtimeEventsPath = "/internal/runtime/events"

// runtimeEventsPingInterval 是 SSE 保活间隔；测试可以缩短。
var runtimeEventsPingInterval = 15 * time.Second

// runtimeEventSubscriber 是 app 层事件订阅能力；流式响应不经过 Dispatch。
type runtimeEventSubscriber interface {
	RuntimeEventsSubscribe(ctx context.Context) <-chan observability.Event
}

// isRuntimeEventsPath 判断是否为 SSE 事件流路由（忽略尾部斜杠）。
func isRuntimeEventsPath(path string) bool {
	return strings.TrimSuffix(strings.TrimSpace(path), "/") == runtimeEventsPath
}

// streamRuntimeEvents 以 SSE 写出运行时事件流。
// 帧格式：`event: <kind>\ndata: <json>\n\n`；每 runtimeEventsPingInterval 发一次
// `event: ping\ndata: {}\n\n` 保活。request context 取消即停止写流并退订。
func streamRuntimeEvents(w http.ResponseWriter, r *http.Request, subscriber runtimeEventSubscriber) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRuntimeAPIError(w, http.StatusInternalServerError, "EVENTS_STREAM_UNSUPPORTED", "event stream requires an HTTP transport with flushing support")
		return
	}
	// 先订阅再写响应头：客户端拿到 200 后发布的事件不会丢。
	events := subscriber.RuntimeEventsSubscribe(r.Context())

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ping := time.NewTicker(runtimeEventsPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			data, err := json.Marshal(event.Payload)
			if err != nil {
				data = []byte("{}")
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Kind, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
