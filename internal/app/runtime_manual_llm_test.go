package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestRuntimeManualLLMToggleAndConfigUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTDOCK_HOME", filepath.Join(home, ".agentdock"))

	cfgOff := config.Config{
		AgentDockDefaultDir: home,
		AgentDockHome:       filepath.Join(home, ".agentdock"),
		LogLevel:            "info",
		MCPAppsMode:         config.MCPAppsModeFull,
	}
	if err := cfgOff.Normalize(); err != nil {
		t.Fatalf("Normalize off config: %v", err)
	}
	rtOff, err := NewRuntime(cfgOff)
	if err != nil {
		t.Fatalf("NewRuntime off: %v", err)
	}
	defer rtOff.Close()

	if rtOff.ManualLLM() != nil {
		t.Fatal("expected nil ManualLLM broker when disabled")
	}
	statusOff := rtOff.RuntimeStatus()
	if enabled, _ := statusOff["manual_llm_enabled"].(bool); enabled {
		t.Fatal("expected status manual_llm_enabled=false")
	}
	queueOff := rtOff.RuntimeManualLLMQueue()
	if enabled, _ := queueOff["enabled"].(bool); enabled {
		t.Fatal("expected queue enabled=false")
	}
	if _, cancelErr := rtOff.RuntimeManualLLMCancel("req_missing", ""); cancelErr == nil {
		t.Fatal("expected cancel error when manual LLM disabled")
	}

	// Verify ConfigRuntime validates and persists AGENTDOCK_MANUAL_LLM_ENABLED.
	if _, badErr := rtOff.RuntimeConfigUpdate(map[string]string{"AGENTDOCK_MANUAL_LLM_ENABLED": "not-bool"}); badErr == nil {
		t.Fatal("expected invalid boolean error for AGENTDOCK_MANUAL_LLM_ENABLED")
	}
	updated, updErr := rtOff.RuntimeConfigUpdate(map[string]string{"AGENTDOCK_MANUAL_LLM_ENABLED": "true"})
	if updErr != nil {
		t.Fatalf("RuntimeConfigUpdate error = %v", updErr)
	}
	if saved, _ := updated["updated"].(int); saved != 1 {
		t.Fatalf("unexpected updated result: %#v", updated)
	}

	cfgOn := cfgOff
	cfgOn.ManualLLMEnabled = true
	rtOn, err := NewRuntime(cfgOn)
	if err != nil {
		t.Fatalf("NewRuntime on: %v", err)
	}
	defer rtOn.Close()

	if rtOn.ManualLLM() == nil {
		t.Fatal("expected non-nil ManualLLM broker when enabled")
	}
	statusOn := rtOn.RuntimeStatus()
	if enabled, _ := statusOn["manual_llm_enabled"].(bool); !enabled {
		t.Fatal("expected status manual_llm_enabled=true")
	}
	cfgMap := rtOn.RuntimeConfig()
	vals, _ := cfgMap["values"].(map[string]string)
	if vals["AGENTDOCK_MANUAL_LLM_ENABLED"] != "true" {
		t.Fatalf("RuntimeConfig AGENTDOCK_MANUAL_LLM_ENABLED = %q, want true", vals["AGENTDOCK_MANUAL_LLM_ENABLED"])
	}
	queueOn := rtOn.RuntimeManualLLMQueue()
	if enabled, _ := queueOn["enabled"].(bool); !enabled {
		t.Fatal("expected queue enabled=true")
	}
	if prompt, _ := queueOn["idle_prompt"].(string); !strings.Contains(prompt, "llm_wait") {
		t.Fatalf("unexpected idle_prompt: %q", prompt)
	}
}
