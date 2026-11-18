package manualllm

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ResponsesError describes a structured OpenAI Responses API HTTP error.
type ResponsesError struct {
	StatusCode int
	Type       string
	Code       string
	Message    string
}

func (e *ResponsesError) Error() string {
	return e.Message
}

// WriteJSON sends the OpenAI-shaped JSON error response body.
func (e *ResponsesError) WriteJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := e.StatusCode
	if status == 0 {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": e.Message,
			"type":    e.Type,
			"code":    e.Code,
		},
	})
}

type rawResponsesRequest struct {
	Model              string            `json:"model"`
	Stream             *bool             `json:"stream"`
	Instructions       string            `json:"instructions"`
	Input              json.RawMessage   `json:"input"`
	Tools              []json.RawMessage `json:"tools"`
	ToolChoice         any               `json:"tool_choice"`
	Text               *rawTextOptions   `json:"text"`
	PreviousResponseID string            `json:"previous_response_id"`
	Conversation       json.RawMessage   `json:"conversation"`
	PromptCacheKey     string            `json:"prompt_cache_key"`
	ClientMetadata     map[string]string `json:"client_metadata"`
}

type rawTextOptions struct {
	Format any `json:"format"`
}

type rawInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Content   json.RawMessage `json:"content"`
	Output    json.RawMessage `json:"output"`
}

type rawContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Refusal  string          `json:"refusal"`
	ImageURL json.RawMessage `json:"image_url"`
	FileURL  string          `json:"file_url"`
}

type rawToolDef struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  json.RawMessage   `json:"parameters"`
	Format      json.RawMessage   `json:"format"`
	Strict      *bool             `json:"strict"`
	Tools       []json.RawMessage `json:"tools"`
}

// ParseResponsesRequest validates and normalizes a streaming POST /v1/responses payload.
func ParseResponsesRequest(body []byte) (ResponsesInput, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return ResponsesInput{}, &ResponsesError{
			StatusCode: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "empty_body",
			Message:    "request body must not be empty",
		}
	}
	var raw rawResponsesRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return ResponsesInput{}, &ResponsesError{
			StatusCode: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "invalid_json",
			Message:    fmt.Sprintf("invalid JSON body: %v", err),
		}
	}
	// Streaming SSE only per manual bridge design contract.
	if raw.Stream == nil || !*raw.Stream {
		return ResponsesInput{}, &ResponsesError{
			StatusCode: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "stream_required",
			Message:    "manual mode supports streaming SSE only: set stream=true on POST /v1/responses",
		}
	}
	model := strings.TrimSpace(raw.Model)
	if model == "" {
		model = "manual"
	}

	imageSeq := 0
	messages, err := parseResponsesInputItems(raw.Input, &imageSeq)
	if err != nil {
		return ResponsesInput{}, err
	}
	tools, err := parseResponsesTools(raw.Tools)
	if err != nil {
		return ResponsesInput{}, err
	}

	var textFormat any
	if raw.Text != nil && raw.Text.Format != nil {
		textFormat = raw.Text.Format
	}

	convKey, threadID := resolveConversationRouting(raw)
	return ResponsesInput{
		Model:              model,
		Instructions:       raw.Instructions,
		Messages:           messages,
		Tools:              tools,
		ToolChoice:         raw.ToolChoice,
		TextFormat:         textFormat,
		PreviousResponseID: strings.TrimSpace(raw.PreviousResponseID),
		ConversationKey:    convKey,
		ThreadID:           threadID,
	}, nil
}

func resolveConversationRouting(raw rawResponsesRequest) (convKey string, threadID string) {
	threadID = strings.TrimSpace(raw.ClientMetadata["thread_id"])
	convID := parseConversationID(raw.Conversation)
	if convID != "" {
		if threadID != "" && threadID != convID {
			return "conv:" + convID + ":thread:" + threadID, threadID
		}
		return "conv:" + convID, threadID
	}
	cacheKey := strings.TrimSpace(raw.PromptCacheKey)
	if cacheKey != "" {
		// Codex Desktop shares prompt_cache_key across sub-threads unless isolated by thread_id.
		if threadID != "" && threadID != cacheKey {
			return "cache:" + cacheKey + ":thread:" + threadID, threadID
		}
		return "cache:" + cacheKey, threadID
	}
	if threadID != "" {
		return "thread:" + threadID, threadID
	}
	return "", ""
}

func parseConversationID(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asObj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &asObj); err == nil {
		return strings.TrimSpace(asObj.ID)
	}
	return ""
}

func parseResponsesInputItems(raw json.RawMessage, imageSeq *int) ([]Message, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, &ResponsesError{
			StatusCode: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "missing_input",
			Message:    "input is required",
		}
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []Message{{Role: "user", Kind: "message", Text: single}}, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, &ResponsesError{
			StatusCode: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       "invalid_input",
			Message:    "input must be a string or an array of input items",
		}
	}

	out := make([]Message, 0, len(items))
	for _, rawItem := range items {
		var item rawInputItem
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return nil, &ResponsesError{
				StatusCode: http.StatusBadRequest,
				Type:       "invalid_request_error",
				Code:       "invalid_input_item",
				Message:    fmt.Sprintf("invalid input item: %v", err),
			}
		}
		itemType := strings.TrimSpace(item.Type)
		if itemType == "" && item.Role != "" {
			itemType = "message"
		}
		switch itemType {
		case "message":
			role := normalizeRole(item.Role)
			text, imgs, err := parseMessageContent(item.Content, imageSeq)
			if err != nil {
				return nil, err
			}
			out = append(out, Message{
				Role:   role,
				Kind:   "message",
				Name:   strings.TrimSpace(item.Name),
				Text:   text,
				Images: imgs,
			})
		case "function_call":
			out = append(out, Message{
				Role:   "assistant",
				Kind:   "tool_call",
				Name:   strings.TrimSpace(item.Name),
				CallID: strings.TrimSpace(item.CallID),
				Text:   item.Arguments,
			})
		case "function_call_output":
			text, imgs, err := parseMessageContent(item.Output, imageSeq)
			if err != nil {
				return nil, err
			}
			out = append(out, Message{
				Role:   "tool",
				Kind:   "tool_output",
				CallID: strings.TrimSpace(item.CallID),
				Text:   text,
				Images: imgs,
			})
		case "custom_tool_call":
			out = append(out, Message{
				Role:   "assistant",
				Kind:   "custom_tool_call",
				Name:   strings.TrimSpace(item.Name),
				CallID: strings.TrimSpace(item.CallID),
				Text:   item.Input,
			})
		case "custom_tool_call_output":
			text, imgs, err := parseMessageContent(item.Output, imageSeq)
			if err != nil {
				return nil, err
			}
			out = append(out, Message{
				Role:   "tool",
				Kind:   "custom_tool_call_output",
				CallID: strings.TrimSpace(item.CallID),
				Text:   text,
				Images: imgs,
			})
		case "reasoning", "compaction", "item_reference", "tool_search_call", "tool_search_output":
			// Replay metadata from multi-turn Responses transcripts adds tokens without
			// representing actionable turn messages.
			continue
		default:
			// Ignore unrecognized replay/annotation items cleanly if role is empty.
			if item.Role != "" {
				text, imgs, err := parseMessageContent(item.Content, imageSeq)
				if err != nil {
					return nil, err
				}
				out = append(out, Message{
					Role:   normalizeRole(item.Role),
					Kind:   itemType,
					Name:   strings.TrimSpace(item.Name),
					Text:   text,
					Images: imgs,
				})
			}
		}
	}
	return out, nil
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return "system"
	case "developer":
		return "developer"
	case "assistant":
		return "assistant"
	case "tool":
		return "tool"
	default:
		return "user"
	}
}

func parseMessageContent(raw json.RawMessage, imageSeq *int) (string, []ImageRef, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil, nil
	}

	var parts []rawContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		// Structured JSON outputs from custom/tool wrappers preserve their raw JSON string.
		return trimmed, nil, nil
	}

	var texts []string
	var imgs []ImageRef
	for _, part := range parts {
		switch part.Type {
		case "", "input_text", "output_text", "text", "summary_text":
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		case "refusal":
			if part.Refusal != "" {
				texts = append(texts, part.Refusal)
			}
		case "input_image", "image_url":
			rawURL := extractImageURL(part.ImageURL)
			if rawURL == "" {
				rawURL = strings.TrimSpace(part.FileURL)
			}
			if rawURL == "" {
				continue
			}
			*imageSeq++
			ref := fmt.Sprintf("img_%d", *imageSeq)
			img := decodeImageRef(ref, rawURL)
			imgs = append(imgs, img)
			if img.MIMEType != "" {
				texts = append(texts, fmt.Sprintf("[image:%s (%s)]", ref, img.MIMEType))
			} else {
				texts = append(texts, fmt.Sprintf("[image:%s]", ref))
			}
		default:
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
	}
	return strings.Join(texts, "\n"), imgs, nil
}

func extractImageURL(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.URL)
	}
	return ""
}

func decodeImageRef(ref, rawURL string) ImageRef {
	if !strings.HasPrefix(rawURL, "data:") {
		return ImageRef{Ref: ref, SourceURL: rawURL}
	}
	rest := strings.TrimPrefix(rawURL, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma <= 0 {
		return ImageRef{Ref: ref, SourceURL: rawURL}
	}
	header := rest[:comma]
	payload := rest[comma+1:]
	mimeType := strings.SplitN(header, ";", 2)[0]
	if strings.Contains(header, ";base64") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
		if err == nil {
			return ImageRef{Ref: ref, MIMEType: mimeType, Data: decoded}
		}
	}
	return ImageRef{Ref: ref, MIMEType: mimeType, SourceURL: rawURL}
}

func parseResponsesTools(rawTools []json.RawMessage) ([]ToolDef, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}
	out := make([]ToolDef, 0, len(rawTools))
	for _, rawTool := range rawTools {
		var rt rawToolDef
		if err := json.Unmarshal(rawTool, &rt); err != nil {
			return nil, &ResponsesError{
				StatusCode: http.StatusBadRequest,
				Type:       "invalid_request_error",
				Code:       "invalid_tool",
				Message:    fmt.Sprintf("invalid tool definition: %v", err),
			}
		}
		toolType := strings.TrimSpace(rt.Type)
		if toolType == "namespace" && len(rt.Tools) > 0 {
			ns := strings.TrimSpace(rt.Name)
			children, err := parseResponsesTools(rt.Tools)
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				child.Namespace = ns
				out = append(out, child)
			}
			continue
		}
		name := strings.TrimSpace(rt.Name)
		if name == "" {
			if toolType != "" && toolType != "function" && toolType != "custom" {
				// Codex/OpenAI built-in tool markers such as tool_search/web_search have no name.
				name = toolType
			} else {
				return nil, &ResponsesError{
					StatusCode: http.StatusBadRequest,
					Type:       "invalid_request_error",
					Code:       "invalid_tool",
					Message:    "tool definition must include a non-empty name",
				}
			}
		}
		if toolType == "" {
			toolType = "function"
		}
		out = append(out, ToolDef{
			Type:        toolType,
			Name:        name,
			Description: strings.TrimSpace(rt.Description),
			Parameters:  compactJSONRaw(rt.Parameters),
			Format:      compactJSONRaw(rt.Format),
			Strict:      rt.Strict,
		})
	}
	return out, nil
}

func compactJSONRaw(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return json.RawMessage(trimmed)
}

func hashText(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func hashTools(tools []ToolDef) string {
	if len(tools) == 0 {
		return ""
	}
	raw, _ := json.Marshal(tools)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func hashToolsMap(tools []ToolDef) map[string]string {
	if len(tools) == 0 {
		return nil
	}
	names := canonicalToolNames(tools)
	out := make(map[string]string, len(tools))
	for i, t := range tools {
		raw, _ := json.Marshal(t)
		sum := sha256.Sum256(raw)
		out[names[i]] = hex.EncodeToString(sum[:8])
	}
	return out
}

func canonicalToolNames(tools []ToolDef) []string {
	nameCounts := make(map[string]int, len(tools))
	for _, t := range tools {
		nameCounts[t.Name]++
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Namespace != "" && nameCounts[t.Name] > 1 {
			out = append(out, displayToolName(t))
		} else {
			out = append(out, t.Name)
		}
	}
	return out
}

// LiveSSEWriter renders incremental manual LLM events as OpenAI Responses SSE frames.
// All methods must be called from the single HTTP response goroutine.
type LiveSSEWriter struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	responseID string
	model      string
	createdAt  int64
	seq        int

	inputBytes  int
	outputBytes int
	outputItems []map[string]any
	openReason  *openReasoningState
	openMessage *openMessageState
	finished    bool
	toolCallSeq int
	toolTypes   map[string]string
}

type openReasoningState struct {
	itemID      string
	outputIndex int
	text        strings.Builder
}

type openMessageState struct {
	itemID      string
	outputIndex int
	text        strings.Builder
}

// NewLiveSSEWriter configures SSE headers and prepares Responses frame emission.
func NewLiveSSEWriter(w http.ResponseWriter, responseID, model string, createdAt int64, tools []ToolDef, inputBytes int) *LiveSSEWriter {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	toolTypes := make(map[string]string, len(tools))
	for _, t := range tools {
		if _, exists := toolTypes[t.Name]; !exists {
			toolTypes[t.Name] = t.Type
		}
		if t.Namespace != "" {
			toolTypes[t.Namespace+"."+t.Name] = t.Type
		}
	}
	flusher, _ := w.(http.Flusher)
	return &LiveSSEWriter{
		w:          w,
		flusher:    flusher,
		responseID: responseID,
		model:      model,
		createdAt:  createdAt,
		inputBytes: inputBytes,
		toolTypes:  toolTypes,
	}
}

func (s *LiveSSEWriter) nextSeq() int {
	n := s.seq
	s.seq++
	return n
}

func (s *LiveSSEWriter) writeEvent(eventType string, payload map[string]any) {
	payload["type"] = eventType
	payload["sequence_number"] = s.nextSeq()
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", eventType, data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *LiveSSEWriter) baseResponse(status string, output []map[string]any, errObj map[string]any, incomplete map[string]any) map[string]any {
	if output == nil {
		output = []map[string]any{}
	}
	resp := map[string]any{
		"id":         s.responseID,
		"object":     "response",
		"created_at": s.createdAt,
		"status":     status,
		"model":      s.model,
		"output":     output,
	}
	if status == "completed" || status == "incomplete" {
		inTokens := (s.inputBytes + 3) / 4
		outTokens := (s.outputBytes + 3) / 4
		resp["usage"] = map[string]any{
			"input_tokens":  inTokens,
			"output_tokens": outTokens,
			"total_tokens":  inTokens + outTokens,
		}
	}
	if errObj != nil {
		resp["error"] = errObj
	}
	if incomplete != nil {
		resp["incomplete_details"] = incomplete
	}
	return resp
}

// Start emits the initial response.created and response.in_progress events.
func (s *LiveSSEWriter) Start() {
	s.writeEvent("response.created", map[string]any{
		"response": s.baseResponse("in_progress", nil, nil, nil),
	})
	s.writeEvent("response.in_progress", map[string]any{
		"response": s.baseResponse("in_progress", nil, nil, nil),
	})
}

// Keepalive sends an SSE comment and lightweight in_progress heartbeat.
func (s *LiveSSEWriter) Keepalive() {
	if s.finished {
		return
	}
	_, _ = fmt.Fprint(s.w, ": keepalive\n\n")
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// Apply streams a validated manual event into OpenAI Responses SSE frames.
func (s *LiveSSEWriter) Apply(ev Event) (committed []Message, stopReason string, errMsg string) {
	if s.finished {
		return nil, "", ""
	}
	deltaText := ev.Text
	if deltaText == "" {
		deltaText = ev.Delta
	}
	switch ev.Type {
	case "thinking":
		if msg := s.closeMessageItem(); msg != nil {
			committed = append(committed, *msg)
		}
		if deltaText != "" {
			s.appendThinking(deltaText)
		}
		return committed, "", ""
	case "text":
		s.closeReasoningItem()
		if deltaText != "" {
			s.appendText(deltaText)
		}
		return nil, "", ""
	case "tool_call":
		s.closeReasoningItem()
		if msg := s.closeMessageItem(); msg != nil {
			committed = append(committed, *msg)
		}
		callMsg := s.emitToolCall(ev)
		committed = append(committed, callMsg)
		return committed, "", ""
	case "done":
		s.closeReasoningItem()
		if msg := s.closeMessageItem(); msg != nil {
			committed = append(committed, *msg)
		}
		reason := strings.TrimSpace(ev.Reason)
		if reason == "" {
			reason = "stop"
		}
		return committed, reason, ""
	case "error":
		s.closeReasoningItem()
		if msg := s.closeMessageItem(); msg != nil {
			committed = append(committed, *msg)
		}
		message := firstNonEmptyTrimmed(ev.Error, ev.Text, ev.Delta, ev.Reason, "manual LLM returned an error")
		return committed, "", message
	default:
		return nil, "", ""
	}
}

func (s *LiveSSEWriter) appendThinking(delta string) {
	if s.openReason == nil {
		idx := len(s.outputItems)
		itemID := fmt.Sprintf("rs_%s_%d", s.responseID, idx)
		item := map[string]any{
			"id":      itemID,
			"type":    "reasoning",
			"status":  "in_progress",
			"summary": []any{},
		}
		s.outputItems = append(s.outputItems, item)
		s.openReason = &openReasoningState{itemID: itemID, outputIndex: idx}
		s.writeEvent("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         item,
		})
		s.writeEvent("response.reasoning_summary_part.added", map[string]any{
			"item_id":       itemID,
			"output_index":  idx,
			"summary_index": 0,
			"part": map[string]any{
				"type": "summary_text",
				"text": "",
			},
		})
	}
	s.openReason.text.WriteString(delta)
	s.outputBytes += len(delta)
	s.writeEvent("response.reasoning_summary_text.delta", map[string]any{
		"item_id":       s.openReason.itemID,
		"output_index":  s.openReason.outputIndex,
		"summary_index": 0,
		"delta":         delta,
	})
}

func (s *LiveSSEWriter) closeReasoningItem() {
	if s.openReason == nil {
		return
	}
	st := s.openReason
	s.openReason = nil
	full := st.text.String()
	s.writeEvent("response.reasoning_summary_text.done", map[string]any{
		"item_id":       st.itemID,
		"output_index":  st.outputIndex,
		"summary_index": 0,
		"text":          full,
	})
	part := map[string]any{
		"type": "summary_text",
		"text": full,
	}
	s.writeEvent("response.reasoning_summary_part.done", map[string]any{
		"item_id":       st.itemID,
		"output_index":  st.outputIndex,
		"summary_index": 0,
		"part":          part,
	})
	item := map[string]any{
		"id":      st.itemID,
		"type":    "reasoning",
		"status":  "completed",
		"summary": []any{part},
	}
	s.outputItems[st.outputIndex] = item
	s.writeEvent("response.output_item.done", map[string]any{
		"output_index": st.outputIndex,
		"item":         item,
	})
}

func (s *LiveSSEWriter) appendText(delta string) {
	if s.openMessage == nil {
		idx := len(s.outputItems)
		itemID := fmt.Sprintf("msg_%s_%d", s.responseID, idx)
		item := map[string]any{
			"id":      itemID,
			"type":    "message",
			"role":    "assistant",
			"status":  "in_progress",
			"content": []any{},
		}
		s.outputItems = append(s.outputItems, item)
		s.openMessage = &openMessageState{itemID: itemID, outputIndex: idx}
		s.writeEvent("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         item,
		})
		s.writeEvent("response.content_part.added", map[string]any{
			"item_id":       itemID,
			"output_index":  idx,
			"content_index": 0,
			"part": map[string]any{
				"type":        "output_text",
				"text":        "",
				"annotations": []any{},
			},
		})
	}
	s.openMessage.text.WriteString(delta)
	s.outputBytes += len(delta)
	s.writeEvent("response.output_text.delta", map[string]any{
		"item_id":       s.openMessage.itemID,
		"output_index":  s.openMessage.outputIndex,
		"content_index": 0,
		"delta":         delta,
	})
}

func (s *LiveSSEWriter) closeMessageItem() *Message {
	if s.openMessage == nil {
		return nil
	}
	st := s.openMessage
	s.openMessage = nil
	full := st.text.String()
	s.writeEvent("response.output_text.done", map[string]any{
		"item_id":       st.itemID,
		"output_index":  st.outputIndex,
		"content_index": 0,
		"text":          full,
	})
	part := map[string]any{
		"type":        "output_text",
		"text":        full,
		"annotations": []any{},
	}
	s.writeEvent("response.content_part.done", map[string]any{
		"item_id":       st.itemID,
		"output_index":  st.outputIndex,
		"content_index": 0,
		"part":          part,
	})
	item := map[string]any{
		"id":      st.itemID,
		"type":    "message",
		"role":    "assistant",
		"status":  "completed",
		"content": []any{part},
	}
	s.outputItems[st.outputIndex] = item
	s.writeEvent("response.output_item.done", map[string]any{
		"output_index": st.outputIndex,
		"item":         item,
	})
	return &Message{
		Role: "assistant",
		Kind: "message",
		Text: full,
	}
}

// FlushOpenAssistantMessage closes any active assistant text item before another output item starts.
func (s *LiveSSEWriter) FlushOpenAssistantMessage() *Message {
	s.closeReasoningItem()
	return s.closeMessageItem()
}

func (s *LiveSSEWriter) emitToolCall(ev Event) Message {
	s.toolCallSeq++
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" {
		callID = fmt.Sprintf("call_%s_%d", s.responseID, s.toolCallSeq)
	}
	idx := len(s.outputItems)
	name := strings.TrimSpace(ev.Name)
	toolType := s.toolTypes[name]

	if toolType == "custom" {
		inputStr := formatCustomToolInput(ev)
		s.outputBytes += len(inputStr)
		itemID := fmt.Sprintf("ctc_%s_%d", s.responseID, idx)
		added := map[string]any{
			"id":      itemID,
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    name,
			"input":   "",
			"status":  "in_progress",
		}
		s.outputItems = append(s.outputItems, added)
		s.writeEvent("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         added,
		})
		if inputStr != "" {
			s.writeEvent("response.custom_tool_call_input.delta", map[string]any{
				"item_id":      itemID,
				"output_index": idx,
				"delta":        inputStr,
			})
		}
		s.writeEvent("response.custom_tool_call_input.done", map[string]any{
			"item_id":      itemID,
			"output_index": idx,
			"input":        inputStr,
		})
		doneItem := map[string]any{
			"id":      itemID,
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    name,
			"input":   inputStr,
			"status":  "completed",
		}
		s.outputItems[idx] = doneItem
		s.writeEvent("response.output_item.done", map[string]any{
			"output_index": idx,
			"item":         doneItem,
		})
		return Message{
			Role:   "assistant",
			Kind:   "custom_tool_call",
			Name:   name,
			CallID: callID,
			Text:   inputStr,
		}
	}

	argsStr := formatFunctionArguments(ev)
	s.outputBytes += len(argsStr)
	itemID := fmt.Sprintf("fc_%s_%d", s.responseID, idx)
	added := map[string]any{
		"id":        itemID,
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": "",
		"status":    "in_progress",
	}
	s.outputItems = append(s.outputItems, added)
	s.writeEvent("response.output_item.added", map[string]any{
		"output_index": idx,
		"item":         added,
	})
	if argsStr != "" {
		s.writeEvent("response.function_call_arguments.delta", map[string]any{
			"item_id":      itemID,
			"output_index": idx,
			"delta":        argsStr,
		})
	}
	s.writeEvent("response.function_call_arguments.done", map[string]any{
		"item_id":      itemID,
		"output_index": idx,
		"name":         name,
		"arguments":    argsStr,
	})
	doneItem := map[string]any{
		"id":        itemID,
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": argsStr,
		"status":    "completed",
	}
	s.outputItems[idx] = doneItem
	s.writeEvent("response.output_item.done", map[string]any{
		"output_index": idx,
		"item":         doneItem,
	})
	return Message{
		Role:   "assistant",
		Kind:   "tool_call",
		Name:   name,
		CallID: callID,
		Text:   argsStr,
	}
}

// Complete writes the terminal response.completed or response.incomplete frame.
func (s *LiveSSEWriter) Complete(reason string) {
	if s.finished {
		return
	}
	s.closeReasoningItem()
	s.closeMessageItem()
	s.finished = true
	if reason == "length" {
		s.writeEvent("response.incomplete", map[string]any{
			"response": s.baseResponse("incomplete", s.outputItems, nil, map[string]any{
				"reason": "max_output_tokens",
			}),
		})
	} else {
		s.writeEvent("response.completed", map[string]any{
			"response": s.baseResponse("completed", s.outputItems, nil, nil),
		})
	}
	_, _ = fmt.Fprint(s.w, "data: [DONE]\n\n")
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// Fail writes a terminal response.failed frame and SSE termination sentinel.
func (s *LiveSSEWriter) Fail(code, message string) {
	if s.finished {
		return
	}
	s.closeReasoningItem()
	s.closeMessageItem()
	s.finished = true
	if code == "" {
		code = "manual_llm_error"
	}
	s.writeEvent("response.failed", map[string]any{
		"response": s.baseResponse("failed", s.outputItems, map[string]any{
			"code":    code,
			"message": message,
		}, nil),
	})
	_, _ = fmt.Fprint(s.w, "data: [DONE]\n\n")
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func formatFunctionArguments(ev Event) string {
	if ev.Arguments == nil {
		if ev.Input != "" {
			raw, _ := json.Marshal(map[string]string{"input": ev.Input})
			return string(raw)
		}
		return "{}"
	}
	if s, ok := ev.Arguments.(string); ok {
		if strings.TrimSpace(s) == "" {
			return "{}"
		}
		return s
	}
	raw, err := json.Marshal(ev.Arguments)
	if err != nil || len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func formatCustomToolInput(ev Event) string {
	if ev.Input != "" {
		return ev.Input
	}
	if ev.Arguments == nil {
		return ""
	}
	if s, ok := ev.Arguments.(string); ok {
		return s
	}
	if m, ok := ev.Arguments.(map[string]any); ok {
		if raw, exists := m["input"]; exists {
			if s, ok := raw.(string); ok {
				return s
			}
		}
		if raw, exists := m["raw"]; exists {
			if s, ok := raw.(string); ok {
				return s
			}
		}
	}
	raw, err := json.Marshal(ev.Arguments)
	if err != nil {
		return ""
	}
	return string(raw)
}
