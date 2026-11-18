package manualllm

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const manualMCPInstructions = "Manual /v1/responses bridge. Claim turns and load context/history via llm_wait; stream outputs via llm_submit."

// NewMCPServer creates the compact 2-tool MCP server for manual LLM bridging.
func NewMCPServer(broker *Broker, version string) *mcpsdk.Server {
	if version == "" {
		version = "dev"
	}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "agentdock-manual-llm",
		Title:   "AgentDock Manual LLM Bridge",
		Version: version,
	}, &mcpsdk.ServerOptions{
		Instructions: manualMCPInstructions,
		Capabilities: &mcpsdk.ServerCapabilities{
			Tools: &mcpsdk.ToolCapabilities{},
		},
	})

	openWorldFalse := false
	destructiveFalse := false

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "llm_wait",
		Description: "Claim/poll turn (action=wait), slice history/output (action=history), or load system_prompt/tool schemas (action=prompt).",
		InputSchema: waitInputSchema(),
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Wait or inspect manual turn",
			ReadOnlyHint:    false,
			DestructiveHint: &destructiveFalse,
			OpenWorldHint:   &openWorldFalse,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input WaitInput) (*mcpsdk.CallToolResult, any, error) {
		out, imgs, err := broker.Wait(ctx, input)
		if err != nil {
			return errorToolResult(err), nil, nil
		}
		return jsonToolResult(out, imgs), nil, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "llm_submit",
		Description: "Stream output events (thinking, text, tool_call, done, error) to /v1/responses SSE; optional wait_ms on done returns next turn inline.",
		InputSchema: submitInputSchema(),
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Submit manual LLM events",
			ReadOnlyHint:    false,
			DestructiveHint: &destructiveFalse,
			OpenWorldHint:   &openWorldFalse,
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input SubmitInput) (*mcpsdk.CallToolResult, any, error) {
		out, imgs, err := broker.Submit(ctx, input)
		if err != nil {
			return errorToolResult(err), nil, nil
		}
		return jsonToolResult(out, imgs), nil, nil
	})

	return srv
}

func floatPtr(v float64) *float64 { return &v }

func waitInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		PropertyOrder: []string{
			"action", "request_id", "session_id", "call_id", "cursor",
			"offset_bytes", "max_bytes", "limit", "include", "tool_names", "wait_ms",
		},
		Properties: map[string]*jsonschema.Schema{
			"action": {
				Type:        "string",
				Enum:        []any{"wait", "history", "prompt"},
				Description: "wait (default) | history | prompt",
			},
			"request_id":   {Type: "string", Description: "Target request id"},
			"session_id":   {Type: "string", Description: "Session id"},
			"call_id":      {Type: "string", Description: "Tool call_id for history slice"},
			"cursor":       {Type: "integer", Minimum: floatPtr(0), Description: "0-based message/tool index"},
			"offset_bytes": {Type: "integer", Minimum: floatPtr(0), Description: "UTF-8 byte offset"},
			"max_bytes":    {Type: "integer", Minimum: floatPtr(1), Maximum: floatPtr(MaxHistorySliceBytes), Description: "Max bytes per slice (default 4096)"},
			"limit":        {Type: "integer", Minimum: floatPtr(1), Maximum: floatPtr(MaxHistoryLimit), Description: "Page item limit"},
			"include": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "string",
					Enum: []any{"system_prompt", "tools", "guide"},
				},
				Description: "Sections for action=prompt",
			},
			"tool_names": {
				Type:        "array",
				Items:       &jsonschema.Schema{Type: "string"},
				Description: "Filter tools by name",
			},
			"wait_ms": {
				Type:        "integer",
				Minimum:     floatPtr(0),
				Maximum:     floatPtr(MaxWaitTimeoutMs),
				Description: "Long-poll ms (0=instant, default 30000)",
			},
		},
	}
}

func submitInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:          "object",
		Required:      []string{"events"},
		PropertyOrder: []string{"request_id", "session_id", "response_id", "events", "wait_ms"},
		Properties: map[string]*jsonschema.Schema{
			"request_id":  {Type: "string", Description: "Target request id"},
			"session_id":  {Type: "string", Description: "Bound session id"},
			"response_id": {Type: "string", Description: "Idempotency key"},
			"events": {
				Type:        "array",
				Description: "Ordered SSE output events",
				Items: &jsonschema.Schema{
					Type:          "object",
					Required:      []string{"type"},
					PropertyOrder: []string{"type", "text", "name", "call_id", "arguments", "reason"},
					Properties: map[string]*jsonschema.Schema{
						"type": {
							Type: "string",
							Enum: []any{"thinking", "text", "tool_call", "done", "error"},
						},
						"text":      {Type: "string", Description: "Thinking/text delta or error message"},
						"name":      {Type: "string", Description: "Declared tool name"},
						"call_id":   {Type: "string", Description: "Optional call id"},
						"arguments": {Description: "JSON args object/string, or custom input string"},
						"reason":    {Type: "string", Description: "stop | tool_calls | length"},
					},
				},
			},
			"wait_ms": {
				Type:        "integer",
				Minimum:     floatPtr(0),
				Maximum:     floatPtr(MaxWaitTimeoutMs),
				Description: "On done, wait ms for next turn",
			},
		},
	}
}

// NewMCPHTTPHandler builds the stateless Streamable HTTP handler for /mcp/manual.
func NewMCPHTTPHandler(broker *Broker, version string) http.Handler {
	srv := NewMCPServer(broker, version)
	return mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return srv
	}, &mcpsdk.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
	})
}

func jsonToolResult(payload any, imgs []ImageRef) *mcpsdk.CallToolResult {
	raw, err := json.Marshal(payload)
	if err != nil {
		return errorToolResult(err)
	}
	content := []mcpsdk.Content{
		&mcpsdk.TextContent{Text: string(raw)},
	}
	for _, img := range imgs {
		if len(img.Data) == 0 {
			continue
		}
		mimeType := img.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		content = append(content, &mcpsdk.ImageContent{
			Data:     img.Data,
			MIMEType: mimeType,
		})
	}
	return &mcpsdk.CallToolResult{
		Content: content,
	}
}

func errorToolResult(err error) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: err.Error()},
		},
	}
}
