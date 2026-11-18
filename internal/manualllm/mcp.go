package manualllm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	opts := DefaultOptions()
	if broker != nil {
		opts = broker.Options()
	}

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "llm_wait",
		Description: "Claim/poll turn (action=wait), slice history/output (action=history), or load system_prompt/tool schemas (action=prompt).",
		InputSchema: waitInputSchemaForOptions(opts),
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Wait or inspect manual turn",
			ReadOnlyHint:    false,
			DestructiveHint: &destructiveFalse,
			OpenWorldHint:   &openWorldFalse,
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, input WaitInput) (*mcpsdk.CallToolResult, any, error) {
		input.CallerKey = callerKeyFromMCPRequest(req)
		out, imgs, err := broker.Wait(ctx, input)
		if err != nil {
			return errorToolResult(err), nil, nil
		}
		return jsonToolResult(out, imgs), nil, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "llm_submit",
		Description: "Stream output events (thinking, text, tool_call, done, error) to /v1/responses SSE; optional wait_ms on done returns next turn inline.",
		InputSchema: submitInputSchemaForOptions(opts),
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Submit manual LLM events",
			ReadOnlyHint:    false,
			DestructiveHint: &destructiveFalse,
			OpenWorldHint:   &openWorldFalse,
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, input SubmitInput) (*mcpsdk.CallToolResult, any, error) {
		input.CallerKey = callerKeyFromMCPRequest(req)
		out, imgs, err := broker.Submit(ctx, input)
		if err != nil {
			return errorToolResult(err), nil, nil
		}
		return jsonToolResult(out, imgs), nil, nil
	})

	return srv
}

func callerKeyFromMCPRequest(req *mcpsdk.CallToolRequest) string {
	if req == nil {
		return ""
	}
	if req.Params != nil {
		if meta := req.Params.GetMeta(); len(meta) > 0 {
			for _, key := range []string{"openai/session", "openai/subject", "conversationId", "conversation_id", "chatId", "chat_id"} {
				if raw, ok := meta[key]; ok {
					if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
						return "meta:" + key + ":" + strings.TrimSpace(s)
					}
				}
			}
		}
	}
	if req.Session != nil {
		if id := strings.TrimSpace(req.Session.ID()); id != "" {
			return "session:" + id
		}
	}
	return ""
}

func floatPtr(v float64) *float64 { return &v }

func waitInputSchema() *jsonschema.Schema {
	return waitInputSchemaForOptions(DefaultOptions())
}

func waitInputSchemaForOptions(opts Options) *jsonschema.Schema {
	opts = opts.Normalized()
	return &jsonschema.Schema{
		Type: "object",
		PropertyOrder: []string{
			"action", "request_id", "session_id", "call_id", "cursor",
			"offset_bytes", "max_bytes", "limit", "include", "tool_names", "fresh", "wait_ms",
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
			"max_bytes":    {Type: "integer", Minimum: floatPtr(1), Maximum: floatPtr(float64(opts.MaxSliceBytes)), Description: fmt.Sprintf("Max bytes per slice (default %d)", opts.SliceBytes)},
			"limit":        {Type: "integer", Minimum: floatPtr(1), Maximum: floatPtr(float64(opts.MaxHistoryLimit)), Description: "Page item limit"},
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
			"fresh": {
				Type:        "boolean",
				Description: "True when resuming in a new chat to re-emit guide/changes",
			},
			"wait_ms": {
				Type:        "integer",
				Minimum:     floatPtr(0),
				Maximum:     floatPtr(float64(opts.MaxWaitMs)),
				Description: fmt.Sprintf("Long-poll ms (0=instant, default %d)", opts.DefaultWaitMs),
			},
		},
	}
}

func submitInputSchema() *jsonschema.Schema {
	return submitInputSchemaForOptions(DefaultOptions())
}

func submitInputSchemaForOptions(opts Options) *jsonschema.Schema {
	opts = opts.Normalized()
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
				Maximum:     floatPtr(float64(opts.MaxWaitMs)),
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

// BridgeToolNames lists manual-mode MCP tool names exposed over NexusBridge when enabled.
func BridgeToolNames() []string {
	return []string{"llm_wait", "llm_submit"}
}

// BridgeToolDescriptors builds Nexus node.hello descriptors using default options.
func BridgeToolDescriptors() []map[string]any {
	return BridgeToolDescriptorsForOptions(DefaultOptions())
}

// BridgeToolDescriptorsForOptions builds Nexus node.hello descriptors matching /mcp/manual schemas.
func BridgeToolDescriptorsForOptions(opts Options) []map[string]any {
	waitSchema := schemaToMap(waitInputSchemaForOptions(opts))
	submitSchema := schemaToMap(submitInputSchemaForOptions(opts))
	openWorldFalse := false
	destructiveFalse := false
	idempotentFalse := false

	return []map[string]any{
		{
			"name":        "llm_wait",
			"title":       "Wait or inspect manual turn",
			"description": "Claim/poll turn (action=wait), slice history/output (action=history), or load system_prompt/tool schemas (action=prompt).",
			"inputSchema": waitSchema,
			"annotations": map[string]any{
				"title":           "Wait or inspect manual turn",
				"readOnlyHint":    false,
				"destructiveHint": &destructiveFalse,
				"idempotentHint":  &idempotentFalse,
				"openWorldHint":   &openWorldFalse,
			},
		},
		{
			"name":        "llm_submit",
			"title":       "Submit manual LLM events",
			"description": "Stream output events (thinking, text, tool_call, done, error) to /v1/responses SSE; optional wait_ms on done returns next turn inline.",
			"inputSchema": submitSchema,
			"annotations": map[string]any{
				"title":           "Submit manual LLM events",
				"readOnlyHint":    false,
				"destructiveHint": &destructiveFalse,
				"idempotentHint":  &idempotentFalse,
				"openWorldHint":   &openWorldFalse,
			},
		},
	}
}

// InvokeBridgeTool executes llm_wait or llm_submit on behalf of NexusBridge tool.call.
func InvokeBridgeTool(ctx context.Context, broker *Broker, name string, args map[string]any) (map[string]any, error) {
	if broker == nil {
		return nil, fmt.Errorf("manual LLM broker is unavailable")
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return bridgeErrorEnvelope(err), nil
	}
	switch name {
	case "llm_wait":
		var in WaitInput
		if len(rawArgs) > 0 && string(rawArgs) != "null" {
			if err := json.Unmarshal(rawArgs, &in); err != nil {
				return bridgeErrorEnvelope(fmt.Errorf("invalid llm_wait arguments: %w", err)), nil
			}
		}
		out, imgs, callErr := broker.Wait(ctx, in)
		if callErr != nil {
			return bridgeErrorEnvelope(callErr), nil
		}
		return bridgeJSONEnvelope(out, imgs), nil
	case "llm_submit":
		var in SubmitInput
		if len(rawArgs) > 0 && string(rawArgs) != "null" {
			if err := json.Unmarshal(rawArgs, &in); err != nil {
				return bridgeErrorEnvelope(fmt.Errorf("invalid llm_submit arguments: %w", err)), nil
			}
		}
		out, imgs, callErr := broker.Submit(ctx, in)
		if callErr != nil {
			return bridgeErrorEnvelope(callErr), nil
		}
		return bridgeJSONEnvelope(out, imgs), nil
	default:
		return nil, fmt.Errorf("unsupported manual bridge tool %q", name)
	}
}

func schemaToMap(schema *jsonschema.Schema) map[string]any {
	raw, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{"type": "object"}
	}
	return out
}

func bridgeJSONEnvelope(payload any, imgs []ImageRef) map[string]any {
	raw, err := json.Marshal(payload)
	if err != nil {
		return bridgeErrorEnvelope(err)
	}
	content := []map[string]any{
		{"type": "text", "text": string(raw)},
	}
	for _, img := range imgs {
		if len(img.Data) == 0 {
			continue
		}
		mimeType := img.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		content = append(content, map[string]any{
			"type":     "image",
			"data":     base64.StdEncoding.EncodeToString(img.Data),
			"mimeType": mimeType,
		})
	}
	return map[string]any{
		"isError": false,
		"content": content,
	}
}

func bridgeErrorEnvelope(err error) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{
			{"type": "text", "text": err.Error()},
		},
	}
}
