package nexusbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/manualllm"
	"github.com/uvwt/agentdock/internal/mcp"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

func TestBridgeManualLLMLiveToggleAndToolInvocation(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: filepath.Join(root, "workspace"),
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		ManualLLMEnabled:    false,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	node := mcp.NewServer(rt, cfg)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var connCount atomic.Int32
	doneCh := make(chan error, 1)
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	sseRecorder := httptest.NewRecorder()
	sseDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			doneCh <- err
			return
		}
		defer socket.Close()
		seq := connCount.Add(1)

		var hello protocol.Message
		if err := socket.ReadJSON(&hello); err != nil {
			doneCh <- err
			return
		}
		if err := socket.WriteJSON(protocol.Message{
			Type:            protocol.MessageNodeReady,
			ProtocolVersion: protocol.ConnectionProtocolVersion,
			HeartbeatMS:     60_000,
		}); err != nil {
			doneCh <- err
			return
		}

		if seq == 1 {
			if slices.Contains(hello.Hello.Capabilities, "llm_wait") {
				t.Errorf("connection 1 should not advertise llm_wait when disabled: %v", hello.Hello.Capabilities)
			}
			// 1. Remotely enable Manual LLM over runtime.request!
			toggleArgs, _ := json.Marshal(map[string]any{
				"method": "POST",
				"path":   "/internal/runtime/manual-llm/toggle",
				"body":   map[string]any{"enabled": true, "persist": false},
			})
			if err := socket.WriteJSON(protocol.Message{
				Type:      protocol.MessageToolInvoke,
				RequestID: "toggle-on",
				Operation: protocol.OperationRuntimeRequest,
				Arguments: toggleArgs,
			}); err != nil {
				doneCh <- err
				return
			}
			var toggleResp protocol.Message
			if err := socket.ReadJSON(&toggleResp); err != nil {
				doneCh <- err
				return
			}
			if toggleResp.Type != protocol.MessageToolResult || toggleResp.RequestID != "toggle-on" {
				t.Errorf("expected toggle-on tool.result before reconnect, got %+v", toggleResp)
			}
			// Hold socket open briefly until hook triggers close/reconnect.
			for {
				if _, _, err := socket.ReadMessage(); err != nil {
					return
				}
			}
		}

		// Connection 2: must advertise llm_wait + llm_submit in capabilities & tools!
		if !slices.Contains(hello.Hello.Capabilities, "llm_wait") || !slices.Contains(hello.Hello.Capabilities, "llm_submit") {
			t.Errorf("connection 2 missing manual LLM capabilities after toggle: %v", hello.Hello.Capabilities)
		}
		foundWaitDesc := false
		for _, td := range hello.Hello.Tools {
			if td.Name == "llm_wait" {
				foundWaitDesc = true
			}
		}
		if !foundWaitDesc {
			t.Errorf("connection 2 missing llm_wait tool descriptor")
		}

		// Enqueue a streaming /v1/responses request into the live broker.
		go func() {
			defer close(sseDone)
			rt.ManualLLM().ServeResponses(sseCtx, sseRecorder, []byte(`{"model":"manual","stream":true,"input":"hello over nexus"}`))
		}()

		// Invoke llm_wait over WebSocket OP.TOOL_CALL!
		waitArgs, _ := json.Marshal(map[string]any{
			"tool":      "llm_wait",
			"arguments": map[string]any{"wait_ms": 5000},
		})
		if err := socket.WriteJSON(protocol.Message{
			Type:      protocol.MessageToolInvoke,
			RequestID: "call-wait",
			Operation: protocol.OperationToolCall,
			Arguments: waitArgs,
		}); err != nil {
			doneCh <- err
			return
		}
		var waitResp protocol.Message
		if err := socket.ReadJSON(&waitResp); err != nil {
			doneCh <- err
			return
		}
		if waitResp.Type != protocol.MessageToolResult {
			t.Errorf("expected tool.result for llm_wait, got %+v", waitResp)
		}
		var envResult struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		rawRes, _ := json.Marshal(waitResp.Result)
		_ = json.Unmarshal(rawRes, &envResult)
		if envResult.IsError || len(envResult.Content) == 0 {
			t.Errorf("unexpected llm_wait bridge envelope: %s", string(rawRes))
		}
		var waitOut manualllm.WaitOutput
		_ = json.Unmarshal([]byte(envResult.Content[0].Text), &waitOut)
		if waitOut.Turn == nil || waitOut.Turn.RequestID == "" {
			t.Errorf("expected claimed turn in llm_wait over bridge, got %s", envResult.Content[0].Text)
			doneCh <- nil
			return
		}

		// Invoke llm_submit over WebSocket OP.TOOL_CALL!
		submitArgs, _ := json.Marshal(map[string]any{
			"tool": "llm_submit",
			"arguments": map[string]any{
				"request_id": waitOut.Turn.RequestID,
				"events": []any{
					map[string]any{"type": "text", "text": "nexus reply"},
					map[string]any{"type": "done", "reason": "stop"},
				},
			},
		})
		if err := socket.WriteJSON(protocol.Message{
			Type:      protocol.MessageToolInvoke,
			RequestID: "call-submit",
			Operation: protocol.OperationToolCall,
			Arguments: submitArgs,
		}); err != nil {
			doneCh <- err
			return
		}
		var subResp protocol.Message
		if err := socket.ReadJSON(&subResp); err != nil {
			doneCh <- err
			return
		}
		if subResp.Type != protocol.MessageToolResult {
			t.Errorf("expected tool.result for llm_submit, got %+v", subResp)
		}
		doneCh <- nil
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(
		Identity{Endpoint: server.URL, NodeID: "node-manual", DeviceID: "device-manual", DeviceToken: "tok"},
		node, rt, publicartifacts.Store{}, &ConnectionState{},
	)
	runDone := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(runDone)
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("timed out waiting for bridge manual LLM handshake/tool roundtrip")
	}

	<-sseDone
	if !strings.Contains(sseRecorder.Body.String(), "nexus reply") || !strings.Contains(sseRecorder.Body.String(), "response.completed") {
		t.Fatalf("expected SSE output streamed via nexus bridge, got:\n%s", sseRecorder.Body.String())
	}

	cancel()
	<-runDone
}
