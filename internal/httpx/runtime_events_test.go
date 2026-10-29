package httpx

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goruntime "runtime"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

// sseFrame 是一帧 SSE：`event: <kind>\ndata: <json>\n\n`。
type sseFrame struct {
	kind string
	data string
	raw  string
	err  error
}

// readSSEFrames 按帧解析 SSE 响应体；格式错误通过 frame.err 返回。
func readSSEFrames(body io.Reader) <-chan sseFrame {
	frames := make(chan sseFrame)
	go func() {
		defer close(frames)
		reader := bufio.NewReader(body)
		for {
			eventLine, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					frames <- sseFrame{err: err}
				}
				return
			}
			dataLine, err := reader.ReadString('\n')
			if err != nil {
				frames <- sseFrame{err: err}
				return
			}
			blankLine, err := reader.ReadString('\n')
			if err != nil {
				frames <- sseFrame{err: err}
				return
			}
			raw := eventLine + dataLine + blankLine
			if !strings.HasPrefix(eventLine, "event: ") || !strings.HasSuffix(eventLine, "\n") {
				frames <- sseFrame{raw: raw, err: errSSEFormat("event line", eventLine)}
				return
			}
			if !strings.HasPrefix(dataLine, "data: ") || !strings.HasSuffix(dataLine, "\n") {
				frames <- sseFrame{raw: raw, err: errSSEFormat("data line", dataLine)}
				return
			}
			if blankLine != "\n" {
				frames <- sseFrame{raw: raw, err: errSSEFormat("frame separator", blankLine)}
				return
			}
			frames <- sseFrame{
				kind: strings.TrimSuffix(strings.TrimPrefix(eventLine, "event: "), "\n"),
				data: strings.TrimSuffix(strings.TrimPrefix(dataLine, "data: "), "\n"),
				raw:  raw,
			}
		}
	}()
	return frames
}

type sseFormatError struct{ what, got string }

func errSSEFormat(what, got string) error { return &sseFormatError{what: what, got: got} }

func (e *sseFormatError) Error() string { return "malformed SSE " + e.what + ": " + e.got }

func createEventStreamTask(t *testing.T, cfg config.Config) string {
	t.Helper()
	tasks, err := taskstate.New(filepath.Join(cfg.AgentDockHome, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := tasks.Create("Stream task", "exercise task events", []string{"done"},
		[]taskstate.TaskStepInput{{ID: "step", Title: "Step"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func TestRuntimeEventsStreamDeliversEventsPingAndUnsubscribes(t *testing.T) {
	cfg := testConfig(t)
	rt, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	previousPing := runtimeEventsPingInterval
	runtimeEventsPingInterval = 20 * time.Millisecond
	defer func() { runtimeEventsPingInterval = previousPing }()

	mux := http.NewServeMux()
	registerRuntimeAPI(mux, rt, cfg, auth.NewOAuthStore())
	server := httptest.NewServer(mux)
	transport := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Transport: transport}
	baselineGoroutines := goruntime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/internal/runtime/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q", got)
	}

	frames := readSSEFrames(response.Body)

	// 触发三类事件源：call（工具调用）、task（删除任务）、artifact（发布+撤销）。
	if _, err := rt.Call(context.Background(), "definitely-not-a-tool", nil); err == nil {
		t.Fatal("expected unknown tool error")
	}
	taskID := createEventStreamTask(t, cfg)
	if _, err := rt.RuntimeTaskDelete(taskID); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	source := filepath.Join(cfg.AgentDockDefaultDir, "stream-artifact.txt")
	if err := os.WriteFile(source, []byte("payload\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	published, err := rt.Call(context.Background(), "file_publish", map[string]any{"path": source})
	if err != nil {
		t.Fatalf("file_publish: %v", err)
	}
	artifactID := published["artifact_id"].(string)
	if _, err := rt.RuntimeArtifactRevoke(artifactID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	seen := map[string]bool{}
	var raw strings.Builder
	deadline := time.After(5 * time.Second)
	for !(seen["call"] && seen["task"] && seen["artifact"] && seen["ping"]) {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("event stream closed before delivering all events")
			}
			if frame.err != nil {
				t.Fatalf("stream frame: %v", frame.err)
			}
			raw.WriteString(frame.raw)
			seen[frame.kind] = true
			switch frame.kind {
			case "ping":
				if frame.data != "{}" {
					t.Fatalf("ping data = %q", frame.data)
				}
			case "call", "task", "artifact":
				if !json.Valid([]byte(frame.data)) {
					t.Fatalf("%s event data is not valid JSON: %q", frame.kind, frame.data)
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(frame.data), &payload); err != nil {
					t.Fatal(err)
				}
				switch frame.kind {
				case "call":
					if _, ok := payload["id"]; !ok {
						t.Fatalf("call payload missing id: %s", frame.data)
					}
				case "task":
					if payload["id"] != taskID {
						t.Fatalf("task payload = %s", frame.data)
					}
				case "artifact":
					if payload["artifact_id"] != artifactID {
						t.Fatalf("artifact payload = %s", frame.data)
					}
				}
			default:
				t.Fatalf("unexpected event kind %q", frame.kind)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for events, seen=%v", seen)
		}
	}
	if !strings.Contains(raw.String(), "event: ping\ndata: {}\n\n") {
		t.Fatalf("raw stream missing ping frame:\n%s", raw.String())
	}

	// 客户端断开：请求 context 取消后必须停止写流并退订。
	cancel()
	drained := false
	disconnectDeadline := time.After(2 * time.Second)
	for !drained {
		select {
		case _, ok := <-frames:
			if !ok {
				drained = true
			}
		case <-disconnectDeadline:
			t.Fatal("stream did not stop after client disconnect")
		}
	}

	waitForCondition(t, func() bool { return rt.RuntimeEventsSubscriberCount() == 0 }, "subscription was not removed after disconnect")

	server.Close()
	transport.CloseIdleConnections()
	waitForCondition(t, func() bool { return goruntime.NumGoroutine() <= baselineGoroutines+5 }, "goroutines leaked after stream teardown")
}

func TestRuntimeEventsStreamRejectsNonGETAndStreamsOnlyOnGET(t *testing.T) {
	cfg := testConfig(t)
	rt, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	handler := runtimeAPIHandler(rt, cfg, auth.NewOAuthStore())

	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		request := newLocalRuntimeRequest(method, "/internal/runtime/events", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, response.Code)
		}
		if got := response.Header().Get("Allow"); got != "GET" {
			t.Fatalf("%s Allow = %q", method, got)
		}
	}
}

func waitForCondition(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(message)
}
