package app

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
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

	// Verify RuntimeManualLLMToggle dynamically creates/closes broker + persists + notifies hook.
	var hooked atomic.Int32
	rtOff.SetManualLLMChangeHook(func() {
		hooked.Add(1)
	})
	toggledOn, err := rtOff.RuntimeManualLLMToggle(true, true)
	if err != nil {
		t.Fatalf("RuntimeManualLLMToggle(true, true) error = %v", err)
	}
	if enabled, _ := toggledOn["enabled"].(bool); !enabled || rtOff.ManualLLM() == nil {
		t.Fatalf("expected live broker created: %#v", toggledOn)
	}
	if !rtOff.ManualLLMEnabled() {
		t.Fatal("expected ManualLLMEnabled() == true after live toggle")
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for hooked.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hooked.Load() != 1 {
		t.Fatalf("expected change hook fired once, got %d", hooked.Load())
	}

	if _, err := rtOff.RuntimeManualLLMToggle(false, true); err != nil {
		t.Fatalf("RuntimeManualLLMToggle(false, true) error = %v", err)
	}
	if rtOff.ManualLLM() != nil || rtOff.ManualLLMEnabled() {
		t.Fatal("expected live broker closed and disabled")
	}

	// Verify tuning parameters propagate to RuntimeConfig, broker Options, Queue snapshot, and cross-field validation.
	if _, err := rtOff.RuntimeConfigUpdate(map[string]string{
		"AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES": "2048",
		"AGENTDOCK_MANUAL_LLM_WAIT_MS":       "25000",
		"AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS":   "60000",
	}); err != nil {
		t.Fatalf("RuntimeConfigUpdate manual tuning error = %v", err)
	}
	if _, err := rtOff.RuntimeConfigUpdate(map[string]string{
		"AGENTDOCK_MANUAL_LLM_WAIT_MS":     "70000",
		"AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS": "50000",
	}); err == nil {
		t.Fatal("expected cross-field error when WAIT_MS > MAX_WAIT_MS")
	}
	if _, err := rtOff.RuntimeConfigUpdate(map[string]string{
		"AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC": "10",
	}); err == nil {
		t.Fatal("expected out-of-range error for AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC=10")
	}

	cfgTuned := cfgOff
	cfgTuned.ManualLLMEnabled = true
	cfgTuned.ManualLLMPreviewBytes = 2048
	cfgTuned.ManualLLMWaitMs = 25000
	cfgTuned.ManualLLMMaxWaitMs = 60000
	cfgTuned.ManualLLMCacheMaxImageMB = 12
	rtTuned, err := NewRuntime(cfgTuned)
	if err != nil {
		t.Fatalf("NewRuntime(tuned) error = %v", err)
	}
	defer rtTuned.Close()

	opts := rtTuned.ManualLLM().Options()
	if opts.PreviewBytes != 2048 || opts.DefaultWaitMs != 25000 || opts.MaxWaitMs != 60000 || opts.MaxRetainedImageBytes != 12*1024*1024 {
		t.Fatalf("unexpected wired broker options: %+v", opts)
	}
	cfgTunedVals, _ := rtTuned.RuntimeConfig()["values"].(map[string]string)
	if cfgTunedVals["AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES"] != "2048" || cfgTunedVals["AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS"] != "60000" {
		t.Fatalf("missing manual LLM tuning values in RuntimeConfig: %+v", cfgTunedVals)
	}
	queueTuned := rtTuned.RuntimeManualLLMQueue()
	if prompt, _ := queueTuned["idle_prompt"].(string); !strings.Contains(prompt, "wait_ms=25000") {
		t.Fatalf("expected configured wait_ms in idle_prompt, got %q", prompt)
	}

	// Verify explicit AGENTDOCK_ENV_FILE takes precedence as persistence target.
	customEnvPath := filepath.Join(t.TempDir(), "custom-agentdock.env")
	t.Setenv("AGENTDOCK_ENV_FILE", customEnvPath)
	if cfgCustomFile, _ := rtTuned.RuntimeConfig()["config_file"].(string); cfgCustomFile != customEnvPath {
		t.Fatalf("config_file = %q, want %q", cfgCustomFile, customEnvPath)
	}
	if _, err := rtTuned.RuntimeConfigUpdate(map[string]string{
		"AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES": "3072",
	}); err != nil {
		t.Fatalf("RuntimeConfigUpdate custom env file error = %v", err)
	}
	parsedCustom, err := envstore.ParseFile(customEnvPath)
	if err != nil || parsedCustom["AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES"] != "3072" {
		t.Fatalf("expected persisted custom env value, got %+v err=%v", parsedCustom, err)
	}
}
