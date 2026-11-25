package manualllm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/agentdock/internal/observability"
)

func intPtr(v int) *int { return &v }

type sseFrame struct {
	Event string
	Data  string
}

func readSSEFrames(t *testing.T, r io.Reader) []sseFrame {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var frames []sseFrame
	var curEvent string
	var curData strings.Builder
	flush := func() {
		if curEvent != "" || curData.Len() > 0 {
			frames = append(frames, sseFrame{
				Event: curEvent,
				Data:  strings.TrimSuffix(curData.String(), "\n"),
			})
			curEvent = ""
			curData.Reset()
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			curData.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			curData.WriteByte('\n')
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE stream: %v", err)
	}
	return frames
}

func TestStreamRequiredAndErrorShapes(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	rec := httptest.NewRecorder()
	b.ServeResponses(context.Background(), rec, []byte(`{"model":"manual","input":"hi","stream":false}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when stream=false, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "stream_required") {
		t.Fatalf("expected stream_required code, got %s", rec.Body.String())
	}
}

func TestMultiTurnProgressiveWaitPromptHistoryAndSubmit(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build a realistic Codex-like request with large system instructions,
	// multiple tools (including namespace + custom), and initial user message.
	sysPrompt := strings.Repeat("Rule line with CJK 中文规则. ", 250) // > 6 KiB
	turnTools := []any{
		map[string]any{
			"type":        "function",
			"name":        "exec_command",
			"description": "Run shell command",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{"cmd": map[string]any{"type": "string"}}},
		},
		map[string]any{
			"type":        "custom",
			"name":        "apply_patch",
			"description": "Apply freeform patch",
			"format":      map[string]any{"type": "text"},
		},
		map[string]any{
			"type": "namespace",
			"name": "browser",
			"tools": []any{
				map[string]any{"type": "function", "name": "open_url", "parameters": map[string]any{"type": "object"}},
			},
		},
	}
	turn1Body, _ := json.Marshal(map[string]any{
		"model":        "gpt-5.4",
		"stream":       true,
		"instructions": sysPrompt,
		"input": []any{
			map[string]any{"type": "message", "role": "developer", "content": "Workspace root: /tmp/demo"},
			map[string]any{"type": "message", "role": "user", "content": "Inspect README and apply a tiny patch."},
		},
		"tools":            turnTools,
		"prompt_cache_key": "cache-1",
	})

	turn1Recorder := httptest.NewRecorder()
	turn1Done := make(chan struct{})
	go func() {
		defer close(turn1Done)
		b.ServeResponses(ctx, turn1Recorder, turn1Body)
	}()

	// Claim Turn 1 via llm_wait.
	wait1, imgs, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(5000)})
	if err != nil {
		t.Fatalf("Wait turn1: %v", err)
	}
	if len(imgs) != 0 || wait1.Status != "claimed" || wait1.Turn == nil {
		t.Fatalf("expected claimed turn1, got %+v", wait1)
	}
	turn1 := wait1.Turn
	if turn1.Turn != 1 || turn1.Guide == "" {
		t.Fatalf("expected turn 1 with progressive guide, got turn=%d guide=%q", turn1.Turn, turn1.Guide)
	}
	if !turn1.Context.HasSystemPrompt || !turn1.Context.SystemPromptChanged || !turn1.Context.ToolsChanged {
		t.Fatalf("expected system/tools changed flags on turn 1, got %+v", turn1.Context)
	}
	if len(turn1.NewMessages) != 2 {
		t.Fatalf("expected 2 initial messages, got %d", len(turn1.NewMessages))
	}

	// Fetch ONLY selected tool + byte-sliced system_prompt progressively.
	prompt1, _, err := b.Wait(ctx, WaitInput{
		Action:    "prompt",
		RequestID: turn1.RequestID,
		ToolNames: []string{"exec_command", "apply_patch"},
		MaxBytes:  2048,
	})
	if err != nil {
		t.Fatalf("Wait prompt: %v", err)
	}
	if prompt1.Prompt == nil || prompt1.Prompt.SystemPrompt == nil || prompt1.Prompt.SystemPrompt.EOF {
		t.Fatalf("expected first slice with EOF=false, got %+v", prompt1.Prompt)
	}
	if len(prompt1.Prompt.Tools) != 2 {
		t.Fatalf("expected filtered 2 tools, got %d", len(prompt1.Prompt.Tools))
	}

	// Verify unknown tool call is rejected before streaming corrupts SSE.
	_, _, badToolErr := b.Submit(ctx, SubmitInput{
		RequestID: turn1.RequestID,
		Events:    []Event{{Type: "tool_call", Name: "nonexistent", Arguments: map[string]any{}}, {Type: "done"}},
	})
	if badToolErr == nil {
		t.Fatal("expected error for undeclared tool")
	}

	// Stream thinking + text + function tool_call + custom tool_call + done, with wait_ms waiting for turn 2.
	submit1Done := make(chan SubmitOutput, 1)
	go func() {
		out, _, submitErr := b.Submit(ctx, SubmitInput{
			RequestID: turn1.RequestID,
			SessionID: turn1.SessionID,
			Events: []Event{
				{Type: "thinking", Text: "Checking workspace..."},
				{Type: "text", Text: "Let me inspect README first."},
				{Type: "tool_call", CallID: "call_exec_1", Name: "exec_command", Arguments: map[string]any{"cmd": "cat README.md"}},
				{Type: "done", Reason: "tool_calls"},
			},
			WaitMs: intPtr(5000),
		})
		if submitErr != nil {
			t.Errorf("Submit turn1 failed: %v", submitErr)
		}
		submit1Done <- out
	}()

	<-turn1Done
	frames1 := readSSEFrames(t, turn1Recorder.Body)
	var eventNames []string
	var completedRespID string
	for _, f := range frames1 {
		if f.Event != "" {
			eventNames = append(eventNames, f.Event)
		}
		if f.Event == "response.completed" {
			var payload struct {
				Response struct {
					ID     string           `json:"id"`
					Status string           `json:"status"`
					Output []map[string]any `json:"output"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(f.Data), &payload); err != nil {
				t.Fatalf("unmarshal response.completed: %v", err)
			}
			completedRespID = payload.Response.ID
			if payload.Response.Status != "completed" || len(payload.Response.Output) != 3 {
				t.Fatalf("unexpected completed response: %+v", payload.Response)
			}
		}
	}
	if completedRespID != turn1.ResponseID {
		t.Fatalf("response_id mismatch: %q != %q", completedRespID, turn1.ResponseID)
	}
	if frames1[len(frames1)-1].Data != "[DONE]" {
		t.Fatalf("expected trailing [DONE] sentinel, got %+v", frames1[len(frames1)-1])
	}
	for _, required := range []string{
		"response.created",
		"response.reasoning_summary_text.delta",
		"response.output_text.delta",
		"response.function_call_arguments.delta",
		"response.completed",
	} {
		found := false
		for _, got := range eventNames {
			if got == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing SSE event %q in %v", required, eventNames)
		}
	}

	// Send Turn 2 with previous_response_id AND huge multi-byte tool_output (> DefaultPreviewBytes).
	largeOut := strings.Repeat("日志记录-✅-", 350) // ~4550 bytes
	turn2Body, _ := json.Marshal(map[string]any{
		"model":                "gpt-5.4",
		"stream":               true,
		"instructions":         sysPrompt,
		"previous_response_id": completedRespID,
		"input": []any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call_exec_1",
				"output":  largeOut,
			},
		},
		"tools": turnTools,
	})

	turn2Recorder := httptest.NewRecorder()
	turn2Done := make(chan struct{})
	go func() {
		defer close(turn2Done)
		b.ServeResponses(ctx, turn2Recorder, turn2Body)
	}()

	// Turn 1's Submit(wait_ms=5000) should receive Turn 2 inline without another llm_wait!
	sub1 := <-submit1Done
	if sub1.NextRequest == nil {
		t.Fatalf("expected Turn 2 returned inline via NextRequest, got %+v", sub1)
	}
	turn2 := sub1.NextRequest
	if turn2.Turn != 2 {
		t.Fatalf("expected turn=2, got %d", turn2.Turn)
	}
	if turn2.Guide != "" {
		t.Fatalf("expected guide omitted on turn 2+, got %q", turn2.Guide)
	}
	if turn2.Context.SystemPromptChanged || turn2.Context.ToolsChanged {
		t.Fatalf("expected unchanged system/tools flags on turn 2, got %+v", turn2.Context)
	}
	if turn2.Context.OmittedMessages != 4 || len(turn2.NewMessages) != 1 {
		t.Fatalf("expected 4 omitted messages and 1 delta message, got omitted=%d new=%d", turn2.Context.OmittedMessages, len(turn2.NewMessages))
	}
	if !turn2.NewMessages[0].Truncated || turn2.NewMessages[0].Truncated && turn2.NewMessages[0].TextBytes != len(largeOut) {
		t.Fatalf("expected truncated preview with exact byte size %d, got %+v", len(largeOut), turn2.NewMessages[0])
	}

	// Read full tool output via action="history" using call_id + UTF-8 safe byte slicing
	// even with tiny slice budget (verifying no rune splits or stall).
	var reassembled strings.Builder
	offset := 0
	for {
		hist, _, hErr := b.Wait(ctx, WaitInput{
			Action:      "history",
			RequestID:   turn2.RequestID,
			CallID:      "call_exec_1",
			OffsetBytes: offset,
			MaxBytes:    1024,
		})
		if hErr != nil {
			t.Fatalf("history byte slice at %d failed: %v", offset, hErr)
		}
		if hist.Slice == nil {
			t.Fatalf("expected slice result, got %+v", hist)
		}
		if !utf8.ValidString(hist.Slice.Content) {
			t.Fatalf("slice at offset %d produced invalid UTF-8", offset)
		}
		reassembled.WriteString(hist.Slice.Content)
		if hist.Slice.EOF {
			break
		}
		if hist.Slice.NextOffset <= offset {
			t.Fatalf("slice pagination did not advance: next=%d cur=%d", hist.Slice.NextOffset, offset)
		}
		offset = hist.Slice.NextOffset
	}
	if reassembled.String() != largeOut {
		t.Fatalf("reassembled UTF-8 output mismatch (got %d bytes, want %d bytes)", reassembled.Len(), len(largeOut))
	}

	// Emit a custom_tool_call on turn 2 and close turn 2.
	sub2, _, err := b.Submit(ctx, SubmitInput{
		RequestID: turn2.RequestID,
		Events: []Event{
			{Type: "tool_call", Name: "apply_patch", Input: "*** Begin Patch\n*** End Patch"},
			{Type: "done", Reason: "tool_calls"},
		},
	})
	if err != nil {
		t.Fatalf("Submit turn2 failed: %v", err)
	}
	if sub2.Status != "completed" || len(sub2.ToolCalls) != 1 {
		t.Fatalf("unexpected submit2 result: %+v", sub2)
	}
	<-turn2Done
	frames2 := readSSEFrames(t, turn2Recorder.Body)
	sawCustom := false
	for _, f := range frames2 {
		if f.Event == "response.custom_tool_call_input.done" && strings.Contains(f.Data, "Begin Patch") {
			sawCustom = true
		}
	}
	if !sawCustom {
		t.Fatalf("expected custom_tool_call_input.done frame, got %+v", frames2)
	}
}

func TestStatelessPrefixDedupAndToolChoiceAndImagesAndMCPHTTP(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	// Tiny 1x1 PNG base64.
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	imgDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	// Expose /mcp/manual via httptest server and connect using official go-sdk MCP client!
	mcpSrv := httptest.NewServer(NewMCPHTTPHandler(b, "test"))
	defer mcpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: mcpSrv.URL}, nil)
	if err != nil {
		t.Fatalf("MCP client connect failed: %v", err)
	}
	defer session.Close()

	listRes, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	schemaRaw, _ := json.Marshal(listRes.Tools)
	if len(schemaRaw) >= 3200 {
		t.Fatalf("expected compact tools/list JSON < 3200 bytes, got %d: %s", len(schemaRaw), string(schemaRaw))
	}
	if strings.Contains(string(schemaRaw), `"null"`) {
		t.Fatalf("expected null-free schemas for strict MCP connectors, got: %s", string(schemaRaw))
	}
	if len(listRes.Tools) != 2 {
		t.Fatalf("expected exactly 2 compressed tools on /mcp/manual, got %d", len(listRes.Tools))
	}
	if listRes.Tools[0].OutputSchema != nil || listRes.Tools[1].OutputSchema != nil {
		t.Fatalf("expected nil outputSchema to minimize token overhead")
	}

	// Enqueue Turn 1 with image input.
	req1Recorder := httptest.NewRecorder()
	req1Done := make(chan struct{})
	req1Body, _ := json.Marshal(map[string]any{
		"model":        "manual",
		"stream":       true,
		"conversation": "conv_abc",
		"tool_choice":  "required",
		"tools": []any{
			map[string]any{"type": "function", "name": "echo", "parameters": map[string]any{"type": "object"}},
		},
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "See image"},
					map[string]any{"type": "input_image", "image_url": imgDataURI},
				},
			},
		},
	})
	go func() {
		defer close(req1Done)
		b.ServeResponses(ctx, req1Recorder, req1Body)
	}()

	// Call llm_wait over MCP wire!
	waitRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "llm_wait",
		Arguments: map[string]any{"wait_ms": 5000},
	})
	if err != nil {
		t.Fatalf("MCP llm_wait failed: %v", err)
	}
	if waitRes.IsError || len(waitRes.Content) < 2 {
		t.Fatalf("expected text JSON + image content blocks, got isError=%v len=%d", waitRes.IsError, len(waitRes.Content))
	}
	txtBlock, ok := waitRes.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected TextContent first, got %T", waitRes.Content[0])
	}
	imgBlock, ok := waitRes.Content[1].(*mcpsdk.ImageContent)
	if !ok || !bytes.Equal(imgBlock.Data, pngBytes) || imgBlock.MIMEType != "image/png" {
		t.Fatalf("expected PNG ImageContent block, got %+v", waitRes.Content[1])
	}
	var parsedWait WaitOutput
	if err := json.Unmarshal([]byte(txtBlock.Text), &parsedWait); err != nil {
		t.Fatalf("unmarshal WaitOutput: %v", err)
	}
	req1ID := parsedWait.Turn.RequestID

	// Verify tool_choice=required rejects done without tool_call!
	badDone, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "llm_submit",
		Arguments: map[string]any{
			"request_id": req1ID,
			"events":     []any{map[string]any{"type": "text", "text": "no tool"}, map[string]any{"type": "done"}},
		},
	})
	if err != nil {
		t.Fatalf("MCP llm_submit protocol error: %v", err)
	}
	if !badDone.IsError {
		t.Fatalf("expected IsError=true when tool_choice=required violated, got %+v", badDone.Content)
	}

	// Now submit valid tool_call + done via MCP.
	goodSub, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "llm_submit",
		Arguments: map[string]any{
			"request_id":  req1ID,
			"response_id": "idem_1",
			"events": []any{
				map[string]any{"type": "tool_call", "call_id": "c1", "name": "echo", "arguments": map[string]any{"msg": "ok"}},
				map[string]any{"type": "done", "reason": "tool_calls"},
			},
		},
	})
	if err != nil || goodSub.IsError {
		t.Fatalf("MCP llm_submit failed: err=%v res=%+v", err, goodSub)
	}
	<-req1Done

	// Enqueue Turn 2 stateless replay on same conversation="conv_abc" containing Turn 1 items + tool output.
	req2Recorder := httptest.NewRecorder()
	req2Done := make(chan struct{})
	req2Body, _ := json.Marshal(map[string]any{
		"model":        "manual",
		"stream":       true,
		"conversation": "conv_abc",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "See image"},
					map[string]any{"type": "input_image", "image_url": imgDataURI},
				},
			},
			map[string]any{
				"type":      "function_call",
				"call_id":   "c1",
				"name":      "echo",
				"arguments": `{"msg":"ok"}`,
			},
			map[string]any{
				"type":    "function_call_output",
				"call_id": "c1",
				"output":  "echoed: ok",
			},
		},
	})
	go func() {
		defer close(req2Done)
		b.ServeResponses(ctx, req2Recorder, req2Body)
	}()

	wait2Res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "llm_wait",
		Arguments: map[string]any{"session_id": parsedWait.Turn.SessionID, "wait_ms": 5000},
	})
	if err != nil || wait2Res.IsError {
		t.Fatalf("MCP llm_wait turn2 failed: err=%v res=%+v", err, wait2Res)
	}
	var parsedWait2 WaitOutput
	_ = json.Unmarshal([]byte(wait2Res.Content[0].(*mcpsdk.TextContent).Text), &parsedWait2)
	if parsedWait2.Turn.Context.OmittedMessages != 2 || len(parsedWait2.Turn.NewMessages) != 1 || parsedWait2.Turn.NewMessages[0].CallID != "c1" {
		t.Fatalf("expected stateless prefix dedup leaving only c1 output, got %+v", parsedWait2.Turn)
	}

	// Cancel Turn 2 from Dashboard and verify SSE ends with response.failed.
	if _, err := b.CancelRequest(parsedWait2.Turn.RequestID, "operator aborted"); err != nil {
		t.Fatalf("CancelRequest failed: %v", err)
	}
	<-req2Done
	frames2 := readSSEFrames(t, req2Recorder.Body)
	if len(frames2) < 2 || frames2[len(frames2)-2].Event != "response.failed" {
		t.Fatalf("expected response.failed on dashboard cancel, got %+v", frames2)
	}
	snap := b.Snapshot(true, "/v1/responses", "/mcp/manual")
	if len(snap.Requests) != 2 || snap.Stats.CompletedCount != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

func TestPromptDesignAndBranchingAndSessionIsolationAndUsage(t *testing.T) {
	// 1. Verify single copyable entry prompt mentions ONLY llm_wait and stays minimal.
	idlePrompt := BuildManualPrompt("", "")
	if len(idlePrompt) >= 140 || !strings.Contains(idlePrompt, "llm_wait") || strings.Contains(idlePrompt, "llm_submit") || strings.Contains(idlePrompt, "history") || strings.Contains(idlePrompt, "prompt") {
		t.Fatalf("unexpected idle prompt: %q", idlePrompt)
	}
	targetedPrompt := BuildManualPrompt("req_1", "s_1")
	if len(targetedPrompt) >= 160 || !strings.Contains(targetedPrompt, "llm_wait") || strings.Contains(targetedPrompt, "llm_submit") {
		t.Fatalf("unexpected targeted prompt: %q", targetedPrompt)
	}

	b := NewBroker()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 2. Verify Turn 1 -> Turn 2 (no tools) -> Turn 3 (re-adds tools) resets ToolsChanged,
	// and branching via previous_response_id=resp_1 isolates away Turn 2's messages!
	toolsA := []any{map[string]any{"type": "function", "name": "ping", "parameters": map[string]any{"type": "object"}}}
	body1, _ := json.Marshal(map[string]any{"model": "manual", "stream": true, "input": "turn 1", "tools": toolsA})
	rec1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		b.ServeResponses(ctx, rec1, body1)
	}()

	w1, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w1.Turn == nil {
		t.Fatalf("wait 1: %v %+v", err, w1)
	}
	resp1 := w1.Turn.ResponseID
	sess1 := w1.Turn.SessionID
	if !strings.Contains(w1.Turn.Guide, "tool_calls") || !strings.Contains(w1.Turn.Guide, "action=\"prompt\"") {
		t.Fatalf("expected actionable Turn 1 Guide mentioning prompt + tool_calls, got %q", w1.Turn.Guide)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{
		RequestID: w1.Turn.RequestID,
		Events:    []Event{{Type: "text", Text: "answer 1"}, {Type: "done", Reason: "stop"}},
	}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	<-done1

	// Check response.completed includes OpenAI usage object.
	frames1 := readSSEFrames(t, rec1.Body)
	hasUsage := false
	for _, f := range frames1 {
		if f.Event == "response.completed" && strings.Contains(f.Data, `"usage"`) && strings.Contains(f.Data, `"total_tokens"`) {
			hasUsage = true
		}
	}
	if !hasUsage {
		t.Fatalf("expected usage object in response.completed, got %+v", frames1)
	}

	// Turn 2 clears tools on same session.
	body2, _ := json.Marshal(map[string]any{"model": "manual", "stream": true, "previous_response_id": resp1, "input": "turn 2"})
	rec2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		b.ServeResponses(ctx, rec2, body2)
	}()

	// Start an active session waiter FIRST, then also verify a generic Wait(session_id="") does NOT hijack Turn 2!
	sessWaitCh := make(chan WaitOutput, 1)
	go func() {
		out, _, wErr := b.Wait(ctx, WaitInput{SessionID: sess1, WaitMs: intPtr(3000)})
		if wErr != nil {
			t.Errorf("session wait error: %v", wErr)
		}
		sessWaitCh <- out
	}()
	time.Sleep(25 * time.Millisecond)

	genericIdle, _, gErr := b.Wait(ctx, WaitInput{WaitMs: intPtr(0)})
	if gErr != nil {
		t.Fatalf("generic wait error: %v", gErr)
	}
	if genericIdle.Status != "idle" || genericIdle.Hint == "" {
		t.Fatalf("expected generic waiter not to steal active session follow-up and include idle hint, got %+v", genericIdle)
	}

	w2 := <-sessWaitCh
	if w2.Turn == nil || w2.Turn.Context.ToolsChanged {
		t.Fatalf("unexpected Turn 2: %+v", w2)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{
		RequestID: w2.Turn.RequestID,
		Events:    []Event{{Type: "text", Text: "answer 2"}, {Type: "done", Reason: "stop"}},
	}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}
	<-done2

	// Turn 3 branches back from resp1 (bypassing turn 2!) and re-introduces toolsA.
	body3, _ := json.Marshal(map[string]any{"model": "manual", "stream": true, "previous_response_id": resp1, "input": "branch from turn 1", "tools": toolsA})
	rec3 := httptest.NewRecorder()
	done3 := make(chan struct{})
	go func() {
		defer close(done3)
		b.ServeResponses(ctx, rec3, body3)
	}()
	w3, _, err := b.Wait(ctx, WaitInput{SessionID: sess1, WaitMs: intPtr(3000)})
	if err != nil || w3.Turn == nil {
		t.Fatalf("wait 3: %v", err)
	}
	if !w3.Turn.Context.ToolsChanged {
		t.Fatal("expected ToolsChanged=true when tools re-added after empty-tools turn")
	}
	// resp1 committed 2 messages (user "turn 1" + assistant "answer 1"), NOT turn 2's messages!
	if w3.Turn.Context.TotalMessages != 3 || w3.Turn.Context.OmittedMessages != 2 {
		t.Fatalf("expected branching from resp1 with 2 prior messages + 1 new, got total=%d omitted=%d", w3.Turn.Context.TotalMessages, w3.Turn.Context.OmittedMessages)
	}
	// Verify canonical error event with text field (6-field Event schema).
	if _, _, err := b.Submit(ctx, SubmitInput{
		RequestID: w3.Turn.RequestID,
		Events:    []Event{{Type: "error", Text: "branch error via text"}},
	}); err != nil {
		t.Fatalf("submit error via text: %v", err)
	}
	<-done3
	frames3 := readSSEFrames(t, rec3.Body)
	if len(frames3) < 2 || !strings.Contains(frames3[len(frames3)-2].Data, "branch error via text") {
		t.Fatalf("expected error text on SSE failure frame, got %+v", frames3)
	}
}

func TestContextCacheCancelledAndFailedTurnDoNotPoisonSession(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools1 := []any{map[string]any{"type": "function", "name": "ping", "parameters": map[string]any{"type": "object"}}}
	tools2 := []any{
		map[string]any{"type": "function", "name": "ping", "parameters": map[string]any{"type": "object"}},
		map[string]any{"type": "function", "name": "pong", "parameters": map[string]any{"type": "object"}},
	}

	// Turn 1 fails via error: must NOT mark Guide/SystemPrompt/Tools as confirmed.
	b1, _ := json.Marshal(map[string]any{
		"model": "manual", "stream": true, "prompt_cache_key": "ck_cancel",
		"instructions": "sys v1", "tools": tools1, "input": "attempt 1",
	})
	rec1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() { defer close(done1); b.ServeResponses(ctx, rec1, b1) }()
	w1, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w1.Turn == nil || w1.Turn.Guide == "" || !w1.Turn.Context.SystemPromptChanged || !w1.Turn.Context.ToolsChanged {
		t.Fatalf("unexpected Turn 1: %v %+v", err, w1)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w1.Turn.RequestID, Events: []Event{{Type: "error", Text: "transient failure"}}}); err != nil {
		t.Fatalf("submit error: %v", err)
	}
	<-done1

	// Retry same prompt/tools on Turn 2: because Turn 1 failed, Guide & changed flags MUST stay true.
	b2, _ := json.Marshal(map[string]any{
		"model": "manual", "stream": true, "prompt_cache_key": "ck_cancel",
		"instructions": "sys v1", "tools": tools1, "input": "attempt 2",
	})
	rec2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() { defer close(done2); b.ServeResponses(ctx, rec2, b2) }()
	w2, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w2.Turn == nil {
		t.Fatalf("wait 2: %v", err)
	}
	if w2.Turn.Guide == "" || !w2.Turn.Context.SystemPromptChanged || !w2.Turn.Context.ToolsChanged {
		t.Fatalf("failed Turn 1 poisoned Turn 2 cache: %+v", w2.Turn)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w2.Turn.RequestID, Events: []Event{{Type: "text", Text: "ok"}, {Type: "done", Reason: "stop"}}}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}
	<-done2

	// Turn 3 updates system_prompt & adds pong, then is cancelled before completion.
	b3, _ := json.Marshal(map[string]any{
		"model": "manual", "stream": true, "prompt_cache_key": "ck_cancel",
		"instructions": "sys v2", "tools": tools2, "input": []any{
			map[string]any{"role": "user", "content": "attempt 2"},
			map[string]any{"role": "assistant", "content": "ok"},
			map[string]any{"role": "user", "content": "turn 3"},
		},
	})
	rec3 := httptest.NewRecorder()
	done3 := make(chan struct{})
	go func() { defer close(done3); b.ServeResponses(ctx, rec3, b3) }()
	w3, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w3.Turn == nil || !w3.Turn.Context.SystemPromptChanged || !w3.Turn.Context.ToolsChanged {
		t.Fatalf("wait 3: %v %+v", err, w3)
	}
	if !slices.Equal(w3.Turn.Context.ChangedTools, []string{"pong"}) {
		t.Fatalf("expected ChangedTools=[pong], got %+v", w3.Turn.Context.ChangedTools)
	}
	if _, err := b.CancelRequest(w3.Turn.RequestID, "abort turn 3"); err != nil {
		t.Fatalf("cancel 3: %v", err)
	}
	<-done3

	// Turn 4 resends sys v2 + tools2: cancelled Turn 3 must NOT have committed S2/tools2!
	b4, _ := json.Marshal(map[string]any{
		"model": "manual", "stream": true, "prompt_cache_key": "ck_cancel",
		"instructions": "sys v2", "tools": tools2, "input": []any{
			map[string]any{"role": "user", "content": "attempt 2"},
			map[string]any{"role": "assistant", "content": "ok"},
			map[string]any{"role": "user", "content": "turn 4 retry"},
		},
	})
	rec4 := httptest.NewRecorder()
	done4 := make(chan struct{})
	go func() { defer close(done4); b.ServeResponses(ctx, rec4, b4) }()
	w4, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w4.Turn == nil {
		t.Fatalf("wait 4: %v", err)
	}
	if !w4.Turn.Context.SystemPromptChanged || !w4.Turn.Context.ToolsChanged || !slices.Equal(w4.Turn.Context.ChangedTools, []string{"pong"}) {
		t.Fatalf("cancelled Turn 3 poisoned Turn 4 delta flags: %+v", w4.Turn.Context)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w4.Turn.RequestID, Events: []Event{{Type: "text", Text: "done4"}, {Type: "done"}}}); err != nil {
		t.Fatalf("submit 4: %v", err)
	}
	<-done4
}

func TestContextCacheColdStartFreshAndCallerSwitchResetsManifest(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	mcpHandler := NewMCPHTTPHandler(b, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	callMCPWait := func(metaSession string, args map[string]any) WaitOutput {
		t.Helper()
		params := map[string]any{
			"name":      "llm_wait",
			"arguments": args,
		}
		if metaSession != "" {
			params["_meta"] = map[string]any{"openai/session": metaSession}
		}
		rpcBody, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params":  params,
		})
		req := httptest.NewRequest(http.MethodPost, "/mcp/manual", bytes.NewReader(rpcBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp := httptest.NewRecorder()
		mcpHandler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("MCP HTTP status=%d body=%s", resp.Code, resp.Body.String())
		}
		var rpcResp struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &rpcResp); err != nil {
			t.Fatalf("unmarshal MCP response %q: %v", resp.Body.String(), err)
		}
		if rpcResp.Result.IsError || len(rpcResp.Result.Content) == 0 {
			t.Fatalf("unexpected MCP error result: %+v", rpcResp)
		}
		var out WaitOutput
		if err := json.Unmarshal([]byte(rpcResp.Result.Content[0].Text), &out); err != nil {
			t.Fatalf("unmarshal WaitOutput %q: %v", rpcResp.Result.Content[0].Text, err)
		}
		return out
	}

	toolsA := []any{map[string]any{"type": "function", "name": "ping", "parameters": map[string]any{"type": "object"}}}
	spawnTurn := func(prevResp, input string) (<-chan struct{}, *httptest.ResponseRecorder) {
		payload := map[string]any{"model": "manual", "stream": true, "instructions": "system stable", "tools": toolsA, "input": input}
		if prevResp != "" {
			payload["previous_response_id"] = prevResp
		}
		raw, _ := json.Marshal(payload)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); b.ServeResponses(ctx, rec, raw) }()
		return done, rec
	}

	// Turn 1 claimed by chat-A.
	d1, _ := spawnTurn("", "hello 1")
	w1 := callMCPWait("chat-A", map[string]any{"wait_ms": 3000})
	if w1.Turn == nil || w1.Turn.Guide == "" || w1.Turn.Context.CacheReset {
		t.Fatalf("unexpected Turn 1: %+v", w1)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w1.Turn.RequestID, CallerKey: "meta:openai/session:chat-A", Events: []Event{{Type: "text", Text: "a1"}, {Type: "done"}}}); err != nil {
		t.Fatal(err)
	}
	<-d1

	// Turn 2 claimed by SAME chat-A: Guide omitted, no reset, changes false.
	d2, _ := spawnTurn(w1.Turn.ResponseID, "hello 2")
	w2 := callMCPWait("chat-A", map[string]any{"session_id": w1.Turn.SessionID, "wait_ms": 3000})
	if w2.Turn == nil || w2.Turn.Guide != "" || w2.Turn.Context.CacheReset || w2.Turn.Context.SystemPromptChanged || w2.Turn.Context.ToolsChanged {
		t.Fatalf("expected warm hit on chat-A Turn 2, got %+v", w2.Turn)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w2.Turn.RequestID, CallerKey: "meta:openai/session:chat-A", Events: []Event{{Type: "text", Text: "a2"}, {Type: "done"}}}); err != nil {
		t.Fatal(err)
	}
	<-d2

	// Turn 3 claimed by DIFFERENT chat-B via _meta["openai/session"]: automatic cold-start reset!
	// Also check Snapshot CopyPrompt includes fresh=true for Turn > 1!
	d3, _ := spawnTurn(w2.Turn.ResponseID, "hello 3")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap := b.Snapshot(true, "/v1/responses", "/mcp/manual")
		if len(snap.Requests) > 0 && snap.Requests[0].Turn == 3 {
			if !strings.Contains(snap.Requests[0].CopyPrompt, "fresh=true") {
				t.Fatalf("expected CopyPrompt for Turn>1 to include fresh=true, got %q", snap.Requests[0].CopyPrompt)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w3 := callMCPWait("chat-B", map[string]any{"session_id": w1.Turn.SessionID, "wait_ms": 3000})
	if w3.Turn == nil || w3.Turn.Guide == "" || !w3.Turn.Context.CacheReset || !w3.Turn.Context.SystemPromptChanged || !w3.Turn.Context.ToolsChanged {
		t.Fatalf("expected automatic cache reset on chat-B switch, got %+v", w3.Turn)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w3.Turn.RequestID, CallerKey: "meta:openai/session:chat-B", Events: []Event{{Type: "text", Text: "a3"}, {Type: "done"}}}); err != nil {
		t.Fatal(err)
	}
	<-d3

	// Turn 4 on SAME chat-B with explicit fresh=true also resets cleanly.
	d4, _ := spawnTurn(w3.Turn.ResponseID, "hello 4")
	w4 := callMCPWait("chat-B", map[string]any{"session_id": w1.Turn.SessionID, "fresh": true, "wait_ms": 3000})
	if w4.Turn == nil || w4.Turn.Guide == "" || !w4.Turn.Context.CacheReset || !w4.Turn.Context.SystemPromptChanged || !w4.Turn.Context.ToolsChanged {
		t.Fatalf("expected explicit fresh=true reset, got %+v", w4.Turn)
	}
	_, _, _ = b.Submit(ctx, SubmitInput{RequestID: w4.Turn.RequestID, Events: []Event{{Type: "done"}}})
	<-d4
}

func TestContextCachePerToolDiffAndResilientStatelessDedup(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	toolDef := func(name, desc string) map[string]any {
		return map[string]any{"type": "function", "name": name, "description": desc, "parameters": map[string]any{"type": "object"}}
	}

	// Stateless Turn 1 with dynamic developer index-0 message + 3 tools [a, b, c].
	b1, _ := json.Marshal(map[string]any{
		"model":            "manual",
		"stream":           true,
		"prompt_cache_key": "ck_resilient",
		"tools":            []any{toolDef("a", "v1"), toolDef("b", "v1"), toolDef("c", "v1")},
		"input": []any{
			map[string]any{"role": "developer", "content": "env ts=1000"},
			map[string]any{"role": "user", "content": "first question"},
		},
	})
	rec1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() { defer close(done1); b.ServeResponses(ctx, rec1, b1) }()
	w1, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w1.Turn == nil {
		t.Fatalf("wait 1: %v", err)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w1.Turn.RequestID, Events: []Event{{Type: "text", Text: "first answer"}, {Type: "done"}}}); err != nil {
		t.Fatal(err)
	}
	<-done1

	// Turn 2: index-0 developer updated (ts=2000!), [first question, first answer] unchanged, user asks turn 2.
	// Tools: b modified -> v2, d added -> ChangedTools=["b", "d"].
	b2, _ := json.Marshal(map[string]any{
		"model":            "manual",
		"stream":           true,
		"prompt_cache_key": "ck_resilient",
		"tools":            []any{toolDef("a", "v1"), toolDef("b", "v2"), toolDef("c", "v1"), toolDef("d", "v1")},
		"input": []any{
			map[string]any{"role": "developer", "content": "env ts=2000"},
			map[string]any{"role": "user", "content": "first question"},
			map[string]any{"role": "assistant", "content": "first answer"},
			map[string]any{"role": "user", "content": "second question"},
		},
	})
	rec2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() { defer close(done2); b.ServeResponses(ctx, rec2, b2) }()
	w2, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w2.Turn == nil {
		t.Fatalf("wait 2: %v", err)
	}
	if len(w2.Turn.NewMessages) != 2 || w2.Turn.Context.OmittedMessages != 2 {
		t.Fatalf("expected resilient dedup to emit [dev_v2, second question] with 2 omitted, got %+v", w2.Turn)
	}
	if w2.Turn.NewMessages[0].Index != 0 || w2.Turn.NewMessages[0].Preview != "env ts=2000" {
		t.Fatalf("expected index 0 for updated developer msg, got %+v", w2.Turn.NewMessages[0])
	}
	if w2.Turn.NewMessages[1].Index != 3 || w2.Turn.NewMessages[1].Preview != "second question" {
		t.Fatalf("expected index 3 for trailing user msg, got %+v", w2.Turn.NewMessages[1])
	}
	if !w2.Turn.Context.ToolsChanged || !slices.Equal(w2.Turn.Context.ChangedTools, []string{"b", "d"}) || len(w2.Turn.Context.RemovedTools) != 0 {
		t.Fatalf("expected per-tool diff ChangedTools=[b d], got %+v", w2.Turn.Context)
	}
	// History cursor=0 resolves updated developer prompt.
	h0, _, err := b.Wait(ctx, WaitInput{Action: "history", RequestID: w2.Turn.RequestID, Cursor: intPtr(0), MaxBytes: 1024})
	if err != nil || h0.Slice == nil || h0.Slice.Content != "env ts=2000" {
		t.Fatalf("history cursor=0 mismatch: %v %+v", err, h0)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w2.Turn.RequestID, Events: []Event{{Type: "text", Text: "second answer"}, {Type: "done"}}}); err != nil {
		t.Fatal(err)
	}
	<-done2

	// Turn 3 removes tool a without changing remaining tools -> ToolsChanged=false, RemovedTools=["a"].
	b3, _ := json.Marshal(map[string]any{
		"model":            "manual",
		"stream":           true,
		"prompt_cache_key": "ck_resilient",
		"tools":            []any{toolDef("b", "v2"), toolDef("c", "v1"), toolDef("d", "v1")},
		"input": []any{
			map[string]any{"role": "developer", "content": "env ts=2000"},
			map[string]any{"role": "user", "content": "first question"},
			map[string]any{"role": "assistant", "content": "first answer"},
			map[string]any{"role": "user", "content": "second question"},
			map[string]any{"role": "assistant", "content": "second answer"},
			map[string]any{"role": "user", "content": "third question"},
		},
	})
	rec3 := httptest.NewRecorder()
	done3 := make(chan struct{})
	go func() { defer close(done3); b.ServeResponses(ctx, rec3, b3) }()
	w3, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w3.Turn == nil {
		t.Fatalf("wait 3: %v", err)
	}
	if w3.Turn.Context.ToolsChanged || len(w3.Turn.Context.ChangedTools) != 0 || !slices.Equal(w3.Turn.Context.RemovedTools, []string{"a"}) {
		t.Fatalf("expected RemovedTools=[a] with ToolsChanged=false, got %+v", w3.Turn.Context)
	}
	_, _, _ = b.Submit(ctx, SubmitInput{RequestID: w3.Turn.RequestID, Events: []Event{{Type: "done"}}})
	<-done3
}

func TestPruneRetainsExactCapAndImageByteBudgetAndCustomOptions(t *testing.T) {
	b := NewBrokerWithOptions(Options{
		PreviewBytes:          300,
		SliceBytes:            512,
		MaxSliceBytes:         1024,
		DefaultWaitMs:         2000,
		MaxWaitMs:             5000,
		CacheMaxResponses:     16,
		MaxRetainedImageBytes: 1500, // holds at most 1 x 1000-byte completed image
	})
	defer b.Close()

	// Override lower for targeted unit test of prune math cleanly (normalized min is 16 for config, force 2 in-package).
	b.mu.Lock()
	b.opts.CacheMaxResponses = 2
	b.mu.Unlock()

	if opts := b.Options(); opts.PreviewBytes != 300 || opts.SliceBytes != 512 || opts.DefaultWaitMs != 2000 {
		t.Fatalf("unexpected custom options: %+v", opts)
	}
	// Verify MCP schema reflects custom options.
	waitSchema := waitInputSchemaForOptions(b.Options())
	if waitSchema.Properties["max_bytes"].Maximum == nil || *waitSchema.Properties["max_bytes"].Maximum != 1024 {
		t.Fatalf("expected schema max_bytes max=1024, got %+v", waitSchema.Properties["max_bytes"])
	}
	if waitSchema.Properties["wait_ms"].Maximum == nil || *waitSchema.Properties["wait_ms"].Maximum != 5000 {
		t.Fatalf("expected schema wait_ms max=5000, got %+v", waitSchema.Properties["wait_ms"])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	imgBytes := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 250) // 1000 bytes
	imgURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes)

	var reqIDs []string
	for i := 0; i < 4; i++ {
		raw, _ := json.Marshal(map[string]any{
			"model":  "manual",
			"stream": true,
			"input": []any{map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": strings.Repeat("Z", 450)},
					map[string]any{"type": "input_image", "image_url": imgURL},
				},
			}},
		})
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); b.ServeResponses(ctx, rec, raw) }()
		w, imgs, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
		if err != nil || w.Turn == nil || len(imgs) != 1 {
			t.Fatalf("turn %d wait: err=%v turn=%+v imgs=%d", i, err, w.Turn, len(imgs))
		}
		if i == 0 && (!w.Turn.NewMessages[0].Truncated || len(w.Turn.NewMessages[0].Preview) > 360) {
			t.Fatalf("expected custom PreviewBytes=300 truncation, got %+v", w.Turn.NewMessages[0])
		}
		reqIDs = append(reqIDs, w.Turn.RequestID)
		if _, _, err := b.Submit(ctx, SubmitInput{RequestID: w.Turn.RequestID, Events: []Event{{Type: "text", Text: "ok"}, {Type: "done"}}}); err != nil {
			t.Fatal(err)
		}
		<-done
	}

	// Trigger pruneLocked via Snapshot: must keep EXACTLY 2 newest terminal requests (reqIDs[2], reqIDs[3]).
	snap := b.Snapshot(true, "/v1/responses", "/mcp/manual")
	if len(snap.Requests) != 2 || snap.Stats.CompletedCount != 2 {
		t.Fatalf("expected exact 2 surviving terminal requests, got len=%d stats=%+v", len(snap.Requests), snap.Stats)
	}
	if snap.Requests[0].RequestID != reqIDs[3] || snap.Requests[1].RequestID != reqIDs[2] {
		t.Fatalf("expected newest [req3 req2], got [%s %s]", snap.Requests[0].RequestID, snap.Requests[1].RequestID)
	}
	if !strings.Contains(snap.IdlePrompt, "wait_ms=2000") {
		t.Fatalf("expected IdlePrompt wait_ms=2000, got %q", snap.IdlePrompt)
	}

	// Newest completed request (reqIDs[3], 1000B <= 1500B) still returns binary image; older (reqIDs[2]) was stripped.
	_, imgsNewest, err := b.Wait(ctx, WaitInput{Action: "history", RequestID: reqIDs[3], Cursor: intPtr(0), MaxBytes: 512})
	if err != nil || len(imgsNewest) != 1 || len(imgsNewest[0].Data) != 1000 {
		t.Fatalf("expected newest terminal request to retain 1000B image, got err=%v imgs=%+v", err, imgsNewest)
	}
	hOlder, imgsOlder, err := b.Wait(ctx, WaitInput{Action: "history", RequestID: reqIDs[2], Cursor: intPtr(0), MaxBytes: 512})
	if err != nil || hOlder.Slice == nil || !strings.Contains(hOlder.Slice.Content, "[image:img_1 (image/png)]") {
		t.Fatalf("expected older terminal request text/marker preserved, got %v %+v", err, hOlder)
	}
	if len(imgsOlder) == 1 && len(imgsOlder[0].Data) != 0 {
		t.Fatalf("expected older terminal image bytes pruned beyond 1500B budget, got %d bytes", len(imgsOlder[0].Data))
	}
}

func TestCodexResponsesAdditionalToolsIncrementalContextAndSystemPrompt(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build a multi-chunk 12 KiB system prompt (> default 4096B slice size).
	var sysBuf strings.Builder
	for i := 0; i < 60; i++ {
		sysBuf.WriteString("You are Codex, collaborating in an AgentDock workspace. ")
		sysBuf.WriteString("Follow repository guidelines, verify edits with targeted tests, and never fabricate tool outputs.\n")
	}
	systemPrompt := sysBuf.String()

	additionalToolsItem := map[string]any{
		"type": "additional_tools",
		"role": "developer",
		"tools": []any{
			map[string]any{
				"type": "namespace",
				"name": "functions",
				"tools": []any{
					map[string]any{
						"type":        "custom",
						"name":        "exec",
						"description": "Execute command in workspace",
						"format":      map[string]any{"type": "text"},
					},
					map[string]any{
						"type":        "function",
						"name":        "read_file",
						"description": "Read repository file",
						"strict":      true,
						"parameters":  map[string]any{"type": "object"},
					},
				},
			},
		},
	}

	// Turn 1 mirrors a real Codex Responses payload: instructions + additional_tools + developer + user + namespaced context output without call_id.
	turn1Input := []any{
		additionalToolsItem,
		map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "workspace_ts=100"}}},
		map[string]any{"type": "function_call_output", "name": "open_page", "namespace": "agent", "output": `{"page_id":null}`},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Inspect repo status"}}},
	}
	b1, _ := json.Marshal(map[string]any{
		"model":            "gpt-5.4-codex",
		"stream":           true,
		"prompt_cache_key": "codex_replay_1",
		"instructions":     systemPrompt,
		"input":            turn1Input,
	})
	rec1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() { defer close(done1); b.ServeResponses(ctx, rec1, b1) }()

	w1, _, err := b.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || w1.Turn == nil {
		t.Fatalf("wait 1: %v %+v", err, w1)
	}
	// Verify additional_tools did not emit a bogus empty message and merged its tools cleanly,
	// and namespaced function_call_output without call_id kept Name="agent.open_page".
	if len(w1.Turn.NewMessages) != 3 {
		t.Fatalf("expected 3 messages (excluding additional_tools), got %d: %+v", len(w1.Turn.NewMessages), w1.Turn.NewMessages)
	}
	if w1.Turn.NewMessages[1].Name != "agent.open_page" {
		t.Fatalf("expected qualified tool output name agent.open_page, got %+v", w1.Turn.NewMessages[1])
	}
	if !w1.Turn.Context.HasSystemPrompt || !w1.Turn.Context.SystemPromptChanged || w1.Turn.Context.SystemPromptBytes != len(systemPrompt) {
		t.Fatalf("unexpected system prompt manifest: %+v", w1.Turn.Context)
	}
	if !w1.Turn.Context.ToolsChanged || w1.Turn.Context.ToolCount != 2 {
		t.Fatalf("expected 2 merged tools from additional_tools, got %+v", w1.Turn.Context)
	}

	// Page through the entire system prompt via llm_wait(action="prompt", include=["system_prompt"]).
	var reassembledSys strings.Builder
	offset := 0
	chunks := 0
	for {
		pOut, _, pErr := b.Wait(ctx, WaitInput{
			Action:      "prompt",
			RequestID:   w1.Turn.RequestID,
			Include:     []string{"system_prompt"},
			OffsetBytes: offset,
			MaxBytes:    4096,
		})
		if pErr != nil || pOut.Prompt == nil || pOut.Prompt.SystemPrompt == nil {
			t.Fatalf("prompt chunk offset=%d error: %v %+v", offset, pErr, pOut)
		}
		reassembledSys.WriteString(pOut.Prompt.SystemPrompt.Content)
		chunks++
		if pOut.Prompt.SystemPrompt.EOF {
			break
		}
		offset = pOut.Prompt.SystemPrompt.NextOffset
	}
	if chunks < 2 || reassembledSys.String() != systemPrompt {
		t.Fatalf("reassembled system prompt mismatch: chunks=%d got=%d want=%d", chunks, reassembledSys.Len(), len(systemPrompt))
	}

	// Emit custom tool call (exec) and complete Turn 1.
	if _, _, err := b.Submit(ctx, SubmitInput{
		RequestID: w1.Turn.RequestID,
		Events: []Event{
			{Type: "tool_call", Name: "exec", CallID: "call_codex_1", Arguments: "git status -s"},
			{Type: "done", Reason: "tool_calls"},
		},
	}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	<-done1

	// Turn 2 replays full Codex transcript with reasoning item + large list custom_tool_call_output + updated developer ts.
	largeCmdOut := strings.Repeat("modified: internal/manualllm/broker.go\n", 90)
	turn2Input := append(append([]any(nil), turn1Input...),
		map[string]any{"type": "reasoning", "encrypted_content": "opaque-encrypted-payload"},
		map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call_codex_1", "input": "git status -s"},
		map[string]any{
			"type":    "custom_tool_call_output",
			"call_id": "call_codex_1",
			"output":  []any{map[string]any{"type": "input_text", "text": largeCmdOut}},
		},
	)
	b2, _ := json.Marshal(map[string]any{
		"model":            "gpt-5.4-codex",
		"stream":           true,
		"prompt_cache_key": "codex_replay_1",
		"instructions":     systemPrompt,
		"input":            turn2Input,
	})
	rec2 := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() { defer close(done2); b.ServeResponses(ctx, rec2, b2) }()

	w2, _, err := b.Wait(ctx, WaitInput{SessionID: w1.Turn.SessionID, WaitMs: intPtr(3000)})
	if err != nil || w2.Turn == nil {
		t.Fatalf("wait 2: %v %+v", err, w2)
	}
	if w2.Turn.Context.SystemPromptChanged || w2.Turn.Context.ToolsChanged {
		t.Fatalf("expected warm system_prompt/tools hit on Turn 2, got %+v", w2.Turn.Context)
	}
	if len(w2.Turn.NewMessages) != 1 || w2.Turn.NewMessages[0].CallID != "call_codex_1" || !w2.Turn.NewMessages[0].Truncated {
		t.Fatalf("expected only truncated incremental custom_tool_call_output in Turn 2, got %+v", w2.Turn.NewMessages)
	}
	if w2.Turn.Context.OmittedMessages != 4 {
		t.Fatalf("expected 4 omitted prior messages, got %+v", w2.Turn.Context)
	}
	// Fetch full custom_tool_call_output via action="history" using call_id.
	histOut, _, err := b.Wait(ctx, WaitInput{
		Action:    "history",
		RequestID: w2.Turn.RequestID,
		CallID:    "call_codex_1",
		MaxBytes:  8192,
	})
	if err != nil || histOut.Slice == nil || histOut.Slice.Content != largeCmdOut {
		t.Fatalf("expected full history slice for call_codex_1: %v %+v", err, histOut)
	}
	if _, _, err := b.Submit(ctx, SubmitInput{
		RequestID: w2.Turn.RequestID,
		Events:    []Event{{Type: "text", Text: "完成验证"}, {Type: "done", Reason: "stop"}},
	}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}
	<-done2
	frames2 := readSSEFrames(t, rec2.Body)
	// 默认 tiktoken 模式下 "完成验证" 与输入均应按 cl100k_base 计算出正数 token。
	if len(frames2) < 2 || !strings.Contains(frames2[len(frames2)-2].Data, `"total_tokens"`) {
		t.Fatalf("expected usage in tiktoken mode, got %+v", frames2)
	}
	// 验证切换为 chars 模式后，4 个中文字符按字符数计数为 1 output_token。
	bChars := NewBrokerWithOptions(Options{TokenCountMode: observability.TokenCountModeChars})
	defer bChars.Close()
	cBody, _ := json.Marshal(map[string]any{"model": "manual", "stream": true, "input": "基于字符"})
	recChars := httptest.NewRecorder()
	doneChars := make(chan struct{})
	go func() { defer close(doneChars); bChars.ServeResponses(ctx, recChars, cBody) }()
	wc, _, err := bChars.Wait(ctx, WaitInput{WaitMs: intPtr(3000)})
	if err != nil || wc.Turn == nil {
		t.Fatalf("chars broker wait: %v", err)
	}
	if _, _, err := bChars.Submit(ctx, SubmitInput{
		RequestID: wc.Turn.RequestID,
		Events:    []Event{{Type: "text", Text: "完成验证"}, {Type: "done", Reason: "stop"}},
	}); err != nil {
		t.Fatalf("chars broker submit: %v", err)
	}
	<-doneChars
	framesChars := readSSEFrames(t, recChars.Body)
	if len(framesChars) < 2 || !strings.Contains(framesChars[len(framesChars)-2].Data, `"output_tokens":1`) {
		t.Fatalf("expected 4 CJK chars to count as 1 output_token in chars mode, got %+v", framesChars)
	}
}

func TestObservedModelRegistryCountsAndOrdering(t *testing.T) {
	b := NewBrokerWithOptions(Options{MaxPendingQueue: ConfigMaxPendingQueue})
	defer b.Close()

	// 无任何请求时注册表为空且为非 nil 空切片，保证 /v1/models 返回 data: []。
	if models := b.Models(); models == nil || len(models) != 0 {
		t.Fatalf("expected empty non-nil model registry, got %+v", models)
	}
	if snap := b.Snapshot(true, "/v1/responses", "/mcp/manual"); snap.Models == nil || len(snap.Models) != 0 {
		t.Fatalf("expected empty models in snapshot, got %+v", snap.Models)
	}

	enqueue := func(model string) {
		t.Helper()
		if _, err := b.enqueue(ResponsesInput{Model: model, Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
			t.Fatalf("enqueue %q: %v", model, err)
		}
	}

	// 单模型多请求：只保留一条记录，请求计数累加。
	for i := 0; i < 3; i++ {
		enqueue("gpt-5.4-codex")
	}
	models := b.Models()
	if len(models) != 1 {
		t.Fatalf("expected 1 tracked model, got %+v", models)
	}
	if models[0].ID != "gpt-5.4-codex" || models[0].Requests != 3 {
		t.Fatalf("expected gpt-5.4-codex with 3 requests, got %+v", models[0])
	}
	if models[0].Created <= 0 || models[0].LastSeen == "" {
		t.Fatalf("expected created unix + last_seen populated, got %+v", models[0])
	}
	if _, err := time.Parse(time.RFC3339, models[0].LastSeen); err != nil {
		t.Fatalf("expected RFC3339 last_seen, got %q (%v)", models[0].LastSeen, err)
	}

	// 多模型：按 last_seen 降序，最后出现的排最前。
	time.Sleep(2 * time.Millisecond)
	enqueue("claude-sonnet")
	time.Sleep(2 * time.Millisecond)
	enqueue("qwen3-coder")
	models = b.Models()
	if len(models) != 3 {
		t.Fatalf("expected 3 tracked models, got %+v", models)
	}
	if models[0].ID != "qwen3-coder" || models[1].ID != "claude-sonnet" || models[2].ID != "gpt-5.4-codex" {
		t.Fatalf("expected last_seen descending [qwen3-coder claude-sonnet gpt-5.4-codex], got %+v", models)
	}

	// Snapshot 必须复用同一份观测结果（并且不二次加锁导致死锁）。
	snap := b.Snapshot(true, "/v1/responses", "/mcp/manual")
	if len(snap.Models) != 3 || snap.Models[0].ID != "qwen3-coder" {
		t.Fatalf("expected snapshot models in last_seen order, got %+v", snap.Models)
	}
}

func TestObservedModelRegistryEvictsLeastRecentlySeen(t *testing.T) {
	b := NewBrokerWithOptions(Options{MaxPendingQueue: ConfigMaxPendingQueue})
	defer b.Close()

	if _, err := b.enqueue(ResponsesInput{Model: "model-oldest", Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("enqueue oldest: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	// 补满到上限（含已有的 model-oldest）。
	for i := 0; i < MaxTrackedModels-1; i++ {
		if _, err := b.enqueue(ResponsesInput{Model: fmt.Sprintf("model-%03d", i), Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if models := b.Models(); len(models) != MaxTrackedModels {
		t.Fatalf("expected exactly %d tracked models, got %d", MaxTrackedModels, len(models))
	}

	// 再观测一个全新模型名：最久未出现的 model-oldest 必须被淘汰。
	time.Sleep(2 * time.Millisecond)
	if _, err := b.enqueue(ResponsesInput{Model: "model-newest", Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("enqueue newest: %v", err)
	}
	models := b.Models()
	if len(models) != MaxTrackedModels {
		t.Fatalf("expected registry to stay bounded at %d, got %d", MaxTrackedModels, len(models))
	}
	if models[0].ID != "model-newest" {
		t.Fatalf("expected model-newest first after eviction, got %+v", models[0])
	}
	for _, model := range models {
		if model.ID == "model-oldest" {
			t.Fatalf("expected least recently seen model-oldest to be evicted, got %+v", models)
		}
	}

	// 重复观测已有模型名只累加计数，不占用新槽位，也不触发淘汰。
	if _, err := b.enqueue(ResponsesInput{Model: "model-000", Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("re-enqueue existing model: %v", err)
	}
	if got := len(b.Models()); got != MaxTrackedModels {
		t.Fatalf("expected repeat observation to keep %d models, got %d", MaxTrackedModels, got)
	}
}

func TestObservedModelRegistryNormalizesAndBoundsNames(t *testing.T) {
	b := NewBrokerWithOptions(Options{MaxPendingQueue: ConfigMaxPendingQueue})
	defer b.Close()

	// 空白模型名不入表（ParseResponsesRequest 会回退为 manual，这里验证兜底防御）。
	if _, err := b.enqueue(ResponsesInput{Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("enqueue blank model: %v", err)
	}
	if models := b.Models(); len(models) != 0 {
		t.Fatalf("expected blank model name to be ignored, got %+v", models)
	}

	// 前后空白被裁剪。
	if _, err := b.enqueue(ResponsesInput{Model: "  spaced-model  ", Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("enqueue spaced model: %v", err)
	}
	models := b.Models()
	if len(models) != 1 || models[0].ID != "spaced-model" {
		t.Fatalf("expected trimmed model name, got %+v", models)
	}

	// 超长名称按 UTF-8 边界截断到上限字节。
	longName := strings.Repeat("模", MaxModelNameBytes) // 3 字节/字符，远超字节上限
	if _, err := b.enqueue(ResponsesInput{Model: longName, Messages: []Message{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("enqueue long model: %v", err)
	}
	var truncated *ModelUsage
	for _, model := range b.Models() {
		if model.ID != "spaced-model" {
			entry := model
			truncated = &entry
		}
	}
	if truncated == nil || len(truncated.ID) > MaxModelNameBytes || !utf8.ValidString(truncated.ID) {
		t.Fatalf("expected long model name truncated to <=%d valid UTF-8 bytes, got %+v", MaxModelNameBytes, truncated)
	}
}
