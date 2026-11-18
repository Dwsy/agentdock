package manualllm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultWaitTimeoutMs bounds a single llm_wait call so HTTP MCP clients do
	// not hit 60s reverse-proxy or transport timeouts while waiting for input.
	DefaultWaitTimeoutMs = 30_000
	// MaxWaitTimeoutMs caps long-polling duration on llm_wait and llm_submit.
	MaxWaitTimeoutMs = 55_000

	// MaxPendingQueue prevents unbounded memory growth if no LLM operator
	// claims queued /v1/responses requests.
	MaxPendingQueue = 32
	// MaxStoredResponses retains recent completed turns so multi-turn callers
	// referencing previous_response_id or fetching late history continue working.
	MaxStoredResponses = 256
	// CompletedRetention bounds how long completed request snapshots remain
	// addressable by request_id or previous_response_id.
	CompletedRetention = 30 * time.Minute
	// SSEKeepaliveInterval emits periodic progress/comment frames so upstream
	// clients keep the streaming /v1/responses socket open during manual work.
	SSEKeepaliveInterval = 15 * time.Second

	// DefaultPreviewBytes limits inline message text returned by llm_wait(wait)
	// so large file dumps or command outputs do not flood the LLM context.
	DefaultPreviewBytes = 1800
	// DefaultHistorySliceBytes is the default chunk size for byte-sliced reads.
	DefaultHistorySliceBytes = 4096
	// MaxHistorySliceBytes caps single-call byte slices in llm_wait(history/prompt).
	MaxHistorySliceBytes = 16 * 1024
	// DefaultHistoryLimit is the default message page size in action="history".
	DefaultHistoryLimit = 10
	// MaxHistoryLimit caps message page size in action="history".
	MaxHistoryLimit = 50
)

// RequestStatus tracks the lifecycle of one /v1/responses request in the queue.
type RequestStatus string

const (
	StatusQueued    RequestStatus = "queued"
	StatusClaimed   RequestStatus = "claimed"
	StatusStreaming RequestStatus = "streaming"
	StatusCompleted RequestStatus = "completed"
	StatusFailed    RequestStatus = "failed"
	StatusCancelled RequestStatus = "cancelled"
)

// ImageRef stores decoded or external image references outside JSON text
// payloads so raw base64 never consumes model context tokens.
type ImageRef struct {
	Ref       string `json:"ref"`
	MIMEType  string `json:"mime_type,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Data      []byte `json:"-"`
}

// Message is the normalized internal representation of a Responses API item.
type Message struct {
	Role   string     `json:"role"`
	Kind   string     `json:"kind,omitempty"`
	Name   string     `json:"name,omitempty"`
	CallID string     `json:"call_id,omitempty"`
	Text   string     `json:"text,omitempty"`
	Images []ImageRef `json:"images,omitempty"`
}

// ToolDef represents a tool definition declared on /v1/responses.
type ToolDef struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Format      json.RawMessage `json:"format,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ResponsesInput is the normalized request parsed from POST /v1/responses.
type ResponsesInput struct {
	Model              string
	Instructions       string
	Messages           []Message
	Tools              []ToolDef
	ToolChoice         any
	TextFormat         any
	PreviousResponseID string
	ConversationKey    string
	ThreadID           string
}

// Event is one incremental or terminal output frame passed to llm_submit.
type Event struct {
	Type      string `json:"type" jsonschema:"Event type: thinking, text, tool_call, done, or error."`
	Text      string `json:"text,omitempty" jsonschema:"Delta text for thinking or text events."`
	Delta     string `json:"delta,omitempty" jsonschema:"Optional alias for text delta."`
	Name      string `json:"name,omitempty" jsonschema:"Tool name when type is tool_call."`
	CallID    string `json:"call_id,omitempty" jsonschema:"Optional tool call id; server generates one if omitted."`
	Arguments any    `json:"arguments,omitempty" jsonschema:"Tool arguments object, JSON string, or custom input string when type is tool_call."`
	Input     string `json:"input,omitempty" jsonschema:"Custom tool text payload alias when type is tool_call."`
	Reason    string `json:"reason,omitempty" jsonschema:"Stop reason for done (stop, tool_calls, length, cancelled) or error detail."`
	Error     string `json:"error,omitempty" jsonschema:"Error message when type is error."`
}

// WaitInput is the MCP schema input for llm_wait.
type WaitInput struct {
	Action      string   `json:"action,omitempty" jsonschema:"Mode: wait (default, claim/poll next input), history (fetch earlier messages or byte slice by call_id/cursor), or prompt (load system_prompt, tools, guide)."`
	RequestID   string   `json:"request_id,omitempty" jsonschema:"Target request id returned by the UI prompt or previous call."`
	SessionID   string   `json:"session_id,omitempty" jsonschema:"Session id for multi-turn follow-up polling and history lookup."`
	CallID      string   `json:"call_id,omitempty" jsonschema:"Exact call_id to inspect full output in action=history."`
	Cursor      *int     `json:"cursor,omitempty" jsonschema:"0-based message index for action=history or tool offset for action=prompt."`
	OffsetBytes int      `json:"offset_bytes,omitempty" jsonschema:"Byte offset when reading a truncated item or system_prompt slice."`
	MaxBytes    int      `json:"max_bytes,omitempty" jsonschema:"Maximum UTF-8 bytes per slice in history/prompt (default 4096, max 16384)."`
	Limit       int      `json:"limit,omitempty" jsonschema:"Maximum items to return per page (history default 10, prompt tools default all matching)."`
	Include     []string `json:"include,omitempty" jsonschema:"Sections to return when action=prompt: system_prompt, tools, guide."`
	ToolNames   []string `json:"tool_names,omitempty" jsonschema:"Optional tool name filter when action=prompt includes tools."`
	WaitMs      *int     `json:"wait_ms,omitempty" jsonschema:"Long-poll milliseconds for action=wait (0 for non-blocking, default 30000, max 55000)."`
}

// SubmitInput is the MCP schema input for llm_submit.
type SubmitInput struct {
	RequestID  string  `json:"request_id,omitempty" jsonschema:"Active request id to stream events into."`
	SessionID  string  `json:"session_id,omitempty" jsonschema:"Session id bound to the request (optional if request_id uniquely identifies it)."`
	ResponseID string  `json:"response_id,omitempty" jsonschema:"Optional caller idempotency key for batch retries."`
	Events     []Event `json:"events" jsonschema:"Ordered output events: thinking, text, tool_call, done, error."`
	WaitMs     *int    `json:"wait_ms,omitempty" jsonschema:"When events ends with done, optionally wait this many ms (max 55000) and return the next follow-up turn inline."`
}

// CompactMessage is the low-token projection returned in wait and history pages.
type CompactMessage struct {
	Index     int      `json:"index"`
	Role      string   `json:"role"`
	Kind      string   `json:"kind,omitempty"`
	Name      string   `json:"name,omitempty"`
	CallID    string   `json:"call_id,omitempty"`
	Preview   string   `json:"preview,omitempty"`
	TextBytes int      `json:"text_bytes"`
	Truncated bool     `json:"truncated,omitempty"`
	ImageRefs []string `json:"image_refs,omitempty"`
}

// ContextManifest describes omitted prompts, tools, and oversized outputs.
type ContextManifest struct {
	TotalMessages       int      `json:"total_messages"`
	OmittedMessages     int      `json:"omitted_messages,omitempty"`
	HasSystemPrompt     bool     `json:"has_system_prompt,omitempty"`
	SystemPromptBytes   int      `json:"system_prompt_bytes,omitempty"`
	SystemPromptChanged bool     `json:"system_prompt_changed,omitempty"`
	ToolCount           int      `json:"tool_count,omitempty"`
	ToolNames           []string `json:"tool_names,omitempty"`
	ToolsChanged        bool     `json:"tools_changed,omitempty"`
	ToolChoice          any      `json:"tool_choice,omitempty"`
	TextFormat          any      `json:"text_format,omitempty"`
	TruncatedItems      int      `json:"truncated_items,omitempty"`
}

// ClaimedTurn is the compact payload returned when a queued turn is claimed.
type ClaimedTurn struct {
	RequestID          string           `json:"request_id"`
	ResponseID         string           `json:"response_id"`
	SessionID          string           `json:"session_id"`
	Turn               int              `json:"turn"`
	Model              string           `json:"model"`
	PreviousResponseID string           `json:"previous_response_id,omitempty"`
	NewMessages        []CompactMessage `json:"new_messages"`
	Context            ContextManifest  `json:"context"`
	Guide              string           `json:"guide,omitempty"`
}

// SliceResult carries a UTF-8-safe byte chunk of a message or system prompt.
type SliceResult struct {
	Target     string `json:"target"`
	Index      *int   `json:"index,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Role       string `json:"role,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content"`
	StartByte  int    `json:"start_byte"`
	EndByte    int    `json:"end_byte"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	EOF        bool   `json:"eof"`
}

// ToolSummary describes one tool returned by action="prompt".
type ToolSummary struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Format      json.RawMessage `json:"format,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// PromptPayload holds progressively fetched prompt/tool sections.
type PromptPayload struct {
	SystemPrompt   *SliceResult  `json:"system_prompt,omitempty"`
	Tools          []ToolSummary `json:"tools,omitempty"`
	TotalTools     int           `json:"total_tools,omitempty"`
	NextToolCursor *int          `json:"next_tool_cursor,omitempty"`
	Guide          string        `json:"guide,omitempty"`
}

// WaitOutput is the JSON response inside CallToolResult for llm_wait.
type WaitOutput struct {
	Status       string           `json:"status"`
	Action       string           `json:"action"`
	PendingCount int              `json:"pending_count,omitempty"`
	Hint         string           `json:"hint,omitempty"`
	Turn         *ClaimedTurn     `json:"turn,omitempty"`
	History      []CompactMessage `json:"history,omitempty"`
	NextCursor   *int             `json:"next_cursor,omitempty"`
	TotalItems   int              `json:"total_items,omitempty"`
	Slice        *SliceResult     `json:"slice,omitempty"`
	Prompt       *PromptPayload   `json:"prompt,omitempty"`
}

// SubmitOutput is the JSON response inside CallToolResult for llm_submit.
type SubmitOutput struct {
	Status      string       `json:"status"`
	RequestID   string       `json:"request_id"`
	SessionID   string       `json:"session_id"`
	Applied     int          `json:"applied"`
	ToolCalls   []string     `json:"tool_calls,omitempty"`
	NextRequest *ClaimedTurn `json:"next_request,omitempty"`
}

// QueueEntryView summarizes a request for Web Dashboard monitoring.
type QueueEntryView struct {
	RequestID       string `json:"request_id"`
	ResponseID      string `json:"response_id"`
	SessionID       string `json:"session_id"`
	Model           string `json:"model"`
	Status          string `json:"status"`
	Turn            int    `json:"turn"`
	CreatedAt       string `json:"created_at"`
	ClaimedAt       string `json:"claimed_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	MessageCount    int    `json:"message_count"`
	NewMessageCount int    `json:"new_message_count"`
	ToolCount       int    `json:"tool_count"`
	Preview         string `json:"preview,omitempty"`
	CopyPrompt      string `json:"copy_prompt"`
	ErrorMessage    string `json:"error_message,omitempty"`
}

// QueueStats aggregates live queue metrics for Overview/ManualLLM pages.
type QueueStats struct {
	QueuedCount    int `json:"queued_count"`
	ActiveCount    int `json:"active_count"`
	CompletedCount int `json:"completed_count"`
	SessionsCount  int `json:"sessions_count"`
}

// QueueSnapshot is returned by GET /internal/runtime/manual-llm.
type QueueSnapshot struct {
	Enabled       bool             `json:"enabled"`
	ResponsesPath string           `json:"responses_path"`
	MCPEndpoint   string           `json:"mcp_endpoint"`
	IdlePrompt    string           `json:"idle_prompt"`
	Stats         QueueStats       `json:"stats"`
	Requests      []QueueEntryView `json:"requests"`
}

// BuildManualPrompt produces the single ultra-compact copyable entry prompt
// mentioning only llm_wait; further rules are revealed progressively via calls.
func BuildManualPrompt(requestID, sessionID string) string {
	var args []string
	targeted := false
	if strings.TrimSpace(requestID) != "" {
		args = append(args, fmt.Sprintf("request_id=%q", strings.TrimSpace(requestID)))
		targeted = true
	}
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, fmt.Sprintf("session_id=%q", strings.TrimSpace(sessionID)))
		targeted = true
	}
	args = append(args, fmt.Sprintf("wait_ms=%d", DefaultWaitTimeoutMs))
	if targeted {
		return fmt.Sprintf("Call llm_wait(%s). Follow its returned instructions and turn data strictly.", strings.Join(args, ", "))
	}
	return fmt.Sprintf("Call llm_wait(%s). Follow its returned instructions and turn data strictly; if idle, wait again.", strings.Join(args, ", "))
}

// CompactPreview truncates oversized text into head + tail UTF-8 slices so
// callers see opening identifiers and trailing errors without context bloat.
func CompactPreview(s string, maxBytes int) (preview string, totalBytes int, truncated bool) {
	totalBytes = len(s)
	if maxBytes <= 0 {
		maxBytes = DefaultPreviewBytes
	}
	if totalBytes <= maxBytes {
		return s, totalBytes, false
	}
	// Reserve ~80 bytes for the omitted-byte divider so total stays predictable.
	budget := maxBytes - 80
	if budget < 64 {
		slice, _, _, _, _ := SliceUTF8Bytes(s, 0, maxBytes)
		return slice + "...", totalBytes, true
	}
	headBudget := (budget * 60) / 100
	tailBudget := budget - headBudget
	head, _, headEnd, _, _ := SliceUTF8Bytes(s, 0, headBudget)
	tailStartTarget := max(headEnd, totalBytes-tailBudget)
	tail, tailStart, _, _, _ := SliceUTF8Bytes(s, tailStartTarget, tailBudget+utf8.UTFMax)
	omitted := max(0, tailStart-headEnd)
	if omitted == 0 {
		return s, totalBytes, false
	}
	return fmt.Sprintf("%s\n...[omitted %d bytes; call llm_wait(action=\"history\") for full slice]...\n%s", head, omitted, tail), totalBytes, true
}

// SliceUTF8Bytes extracts a rune-aligned byte range [startByte, endByte) from s.
// If maxBytes is smaller than a single multi-byte rune at offsetBytes, at least
// one full rune is included so byte pagination always advances.
func SliceUTF8Bytes(s string, offsetBytes, maxBytes int) (slice string, startByte, endByte, totalBytes int, eof bool) {
	totalBytes = len(s)
	if totalBytes == 0 {
		return "", 0, 0, 0, true
	}
	if offsetBytes < 0 {
		offsetBytes = 0
	}
	if offsetBytes >= totalBytes {
		return "", totalBytes, totalBytes, totalBytes, true
	}
	// Advance to the start of the next rune if offset lands inside continuation bytes.
	startByte = offsetBytes
	for startByte < totalBytes && !utf8.RuneStart(s[startByte]) {
		startByte++
	}
	if startByte >= totalBytes {
		return "", totalBytes, totalBytes, totalBytes, true
	}
	if maxBytes <= 0 {
		maxBytes = DefaultHistorySliceBytes
	}
	limit := min(totalBytes, startByte+maxBytes)
	// Walk backward from limit to a rune boundary.
	endByte = limit
	for endByte > startByte && endByte < totalBytes && !utf8.RuneStart(s[endByte]) {
		endByte--
	}
	if endByte <= startByte {
		_, size := utf8.DecodeRuneInString(s[startByte:])
		endByte = min(totalBytes, startByte+max(1, size))
	}
	return s[startByte:endByte], startByte, endByte, totalBytes, endByte >= totalBytes
}
