package app

import (
	"fmt"

	"github.com/uvwt/agentdock/internal/manualllm"
)

// RuntimeManualLLMQueue returns the current manual LLM queue snapshot and copy prompt.
func (r *Runtime) RuntimeManualLLMQueue() Result {
	if r == nil || r.manualLLM == nil {
		return Result{
			"ok":             true,
			"source":         runtimeAPISource,
			"enabled":        false,
			"responses_path": "/v1/responses",
			"mcp_endpoint":   "/mcp/manual",
			"idle_prompt":    manualllm.BuildManualPrompt("", ""),
			"stats":          manualllm.QueueStats{},
			"requests":       []manualllm.QueueEntryView{},
		}
	}
	snap := r.manualLLM.Snapshot(true, "/v1/responses", "/mcp/manual")
	return Result{
		"ok":             true,
		"source":         runtimeAPISource,
		"enabled":        snap.Enabled,
		"responses_path": snap.ResponsesPath,
		"mcp_endpoint":   snap.MCPEndpoint,
		"idle_prompt":    snap.IdlePrompt,
		"stats":          snap.Stats,
		"requests":       snap.Requests,
	}
}

// RuntimeManualLLMCancel cancels a queued or active manual LLM request.
func (r *Runtime) RuntimeManualLLMCancel(requestID, reason string) (Result, error) {
	if r == nil || r.manualLLM == nil {
		return nil, fmt.Errorf("manual LLM mode is disabled")
	}
	entry, err := r.manualLLM.CancelRequest(requestID, reason)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"request": entry,
	}, nil
}
