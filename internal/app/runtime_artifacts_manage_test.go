package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func newArtifactTestRuntime(t *testing.T, mode config.TraceMode) *Runtime {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		OAuthServerURL:      "https://example.test",
		TraceMode:           mode,
		TraceMaxRecords:     100,
		TraceBufferSize:     1,
		TraceFlushMS:        100,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})
	return runtime
}

func publishArtifactFixture(t *testing.T, rt *Runtime) publicartifacts.PublishResult {
	t.Helper()
	source := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(source, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := rt.artifacts.Publish(publicartifacts.PublishRequest{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRuntimeArtifactRevokeRemovesArtifact(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeMemory)
	published := publishArtifactFixture(t, rt)

	result, err := rt.RuntimeArtifactRevoke(published.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["source"] != runtimeAPISource || result["action"] != "revoke" || result["artifact_id"] != published.ArtifactID {
		t.Fatalf("revoke result = %#v", result)
	}
	list, err := rt.RuntimeArtifacts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if list["count"] != 0 {
		t.Fatalf("artifacts after revoke = %#v", list)
	}

	_, err = rt.RuntimeArtifactRevoke(published.ArtifactID)
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "NOT_FOUND" || toolErr.Category != "not_found" {
		t.Fatalf("second revoke error = %#v", err)
	}
}

func TestRuntimeArtifactRevokeRejectsTraversalID(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeOff)
	_, err := rt.RuntimeArtifactRevoke("../../secrets")
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "NOT_FOUND" {
		t.Fatalf("traversal revoke error = %#v", err)
	}
}

func TestRuntimeArtifactExtendValidatesHours(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeOff)
	published := publishArtifactFixture(t, rt)
	for _, hours := range []int{0, -1, 721, 1000} {
		_, err := rt.RuntimeArtifactExtend(published.ArtifactID, hours)
		var toolErr *toolcore.ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ARGUMENT" || toolErr.Category != "validation" {
			t.Fatalf("hours=%d error = %#v", hours, err)
		}
	}
}

func TestRuntimeArtifactExtendReturnsResignedArtifact(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeOff)
	published := publishArtifactFixture(t, rt)
	before := published.ExpiresAt

	result, err := rt.RuntimeArtifactExtend(published.ArtifactID, 48)
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["source"] != runtimeAPISource || result["action"] != "extend" {
		t.Fatalf("extend result = %#v", result)
	}
	artifact, ok := result["artifact"].(publicartifacts.PublishResult)
	if !ok {
		t.Fatalf("artifact type = %T", result["artifact"])
	}
	if !artifact.ExpiresAt.After(before) {
		t.Fatalf("expires not extended: %v -> %v", before, artifact.ExpiresAt)
	}
	if artifact.URL == "" || !strings.HasPrefix(artifact.URL, "https://example.test/artifacts/public/") || artifact.URL == published.URL {
		t.Fatalf("resigned url = %q (old %q)", artifact.URL, published.URL)
	}
}

func TestRuntimeArtifactExtendUnknownReturnsNotFound(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeOff)
	_, err := rt.RuntimeArtifactExtend("0123456789abcdef0123456789abcdef", 24)
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "NOT_FOUND" || toolErr.Category != "not_found" {
		t.Fatalf("unknown extend error = %#v", err)
	}
}

func TestRuntimeArtifactMutationsEnterTraceAsInternal(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeMemory)
	published := publishArtifactFixture(t, rt)
	if _, err := rt.RuntimeArtifactExtend(published.ArtifactID, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RuntimeArtifactRevoke(published.ArtifactID); err != nil {
		t.Fatal(err)
	}

	records := rt.traces.Query(observability.TraceQuery{Tool: "artifact_extend"})
	if len(records) != 1 || records[0].Source != "internal" || !records[0].Success {
		t.Fatalf("extend trace = %#v", records)
	}
	if !strings.Contains(records[0].Arguments, published.ArtifactID) {
		t.Fatalf("extend trace arguments = %q", records[0].Arguments)
	}
	records = rt.traces.Query(observability.TraceQuery{Tool: "artifact_revoke"})
	if len(records) != 1 || records[0].Source != "internal" || !records[0].Success {
		t.Fatalf("revoke trace = %#v", records)
	}

	// 失败也进 trace：NOT_FOUND 会带上可识别的错误码。
	if _, err := rt.RuntimeArtifactRevoke(published.ArtifactID); err == nil {
		t.Fatal("expected not found error")
	}
	records = rt.traces.Query(observability.TraceQuery{Tool: "artifact_revoke", Status: "error"})
	if len(records) != 1 || records[0].ErrorCode != "NOT_FOUND" {
		t.Fatalf("failed revoke trace = %#v", records)
	}
}

func TestRuntimeArtifactExtendClampDoesNotExceedRetention(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeOff)
	published := publishArtifactFixture(t, rt)
	result, err := rt.RuntimeArtifactExtend(published.ArtifactID, 720)
	if err != nil {
		t.Fatal(err)
	}
	artifact := result["artifact"].(publicartifacts.PublishResult)
	if max := published.CreatedAt.Add(publicartifacts.MaxRetention); artifact.ExpiresAt.After(max) {
		t.Fatalf("expires %v exceeds created_at+MaxRetention %v", artifact.ExpiresAt, max)
	}
	if artifact.ExpiresAt.Before(time.Now().UTC()) {
		t.Fatalf("extended expires already in the past: %v", artifact.ExpiresAt)
	}
}
