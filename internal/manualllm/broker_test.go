package manualllm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
