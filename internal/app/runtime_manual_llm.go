package app

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/uvwt/agentdock/internal/manualllm"
)

// RuntimeManualLLMQueue returns the current manual LLM queue snapshot and copy prompt.
func (r *Runtime) RuntimeManualLLMQueue() Result {
	opts := r.manualLLMOptions()
	broker := r.ManualLLM()
	if broker == nil {
		return Result{
			"ok":             true,
			"source":         runtimeAPISource,
			"enabled":        false,
			"responses_path": "/v1/responses",
			"mcp_endpoint":   "/mcp/manual",
			"idle_prompt":    manualllm.BuildManualPromptWithOptions("", "", opts.DefaultWaitMs, false),
			"tuning":         opts.TuningView(),
			"stats":          manualllm.QueueStats{},
			"requests":       []manualllm.QueueEntryView{},
		}
	}
	snap := broker.Snapshot(true, "/v1/responses", "/mcp/manual")
	return Result{
		"ok":             true,
		"source":         runtimeAPISource,
		"enabled":        snap.Enabled,
		"responses_path": snap.ResponsesPath,
		"mcp_endpoint":   snap.MCPEndpoint,
		"idle_prompt":    snap.IdlePrompt,
		"tuning":         snap.Tuning,
		"stats":          snap.Stats,
		"requests":       snap.Requests,
	}
}

// RuntimeManualLLMCancel cancels a queued or active manual LLM request.
func (r *Runtime) RuntimeManualLLMCancel(requestID, reason string) (Result, error) {
	broker := r.ManualLLM()
	if broker == nil {
		return nil, fmt.Errorf("manual LLM mode is disabled")
	}
	entry, err := broker.CancelRequest(requestID, reason)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"request": entry,
	}, nil
}

// RuntimeManualLLMToggle dynamically enables or disables the manual LLM bridge
// without restarting Core, optionally persisting AGENTDOCK_MANUAL_LLM_ENABLED.
func (r *Runtime) RuntimeManualLLMToggle(enabled, persist bool) (Result, error) {
	if r == nil {
		return nil, errors.New("runtime is not initialized")
	}

	r.manualLLMMu.Lock()
	changed := r.manualLLMEnabled != enabled
	var toClose *manualllm.Broker
	if enabled {
		if r.manualLLM == nil {
			r.manualLLM = manualllm.NewBrokerWithOptions(r.manualLLMOptions())
		}
		r.manualLLMEnabled = true
	} else {
		toClose = r.manualLLM
		r.manualLLM = nil
		r.manualLLMEnabled = false
	}
	hook := r.onManualLLMChange
	r.manualLLMMu.Unlock()

	if toClose != nil {
		toClose.Close()
	}

	persisted := false
	persistNote := ""
	if persist {
		if _, err := r.RuntimeConfigUpdate(map[string]string{
			"AGENTDOCK_MANUAL_LLM_ENABLED": strconv.FormatBool(enabled),
		}); err != nil {
			if errors.Is(err, errConfigUnsupported) {
				persistNote = err.Error()
			} else {
				return nil, err
			}
		} else {
			persisted = true
		}
	}

	// Delay bridge re-handshake briefly so the caller receives this toggle
	// response cleanly before NexusBridge reconnects with updated tools.
	if changed && hook != nil {
		time.AfterFunc(80*time.Millisecond, hook)
	}

	out := Result{
		"ok":        true,
		"source":    runtimeAPISource,
		"enabled":   enabled,
		"changed":   changed,
		"persisted": persisted,
	}
	if persistNote != "" {
		out["persist_note"] = persistNote
	}
	return out, nil
}
