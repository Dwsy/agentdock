package app

import (
	"errors"
	"io"
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

func TestRuntimeArtifactPublishPathDirectoryAndUpload(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeMemory)

	// 1. 本机单文件发布
	filePath := filepath.Join(rt.cfg.AgentDockDefaultDir, "notes.md")
	if err := os.WriteFile(filePath, []byte("# Title\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := rt.RuntimeArtifactPublish(t.Context(), ArtifactPublishInput{Path: "notes.md", RetentionSeconds: 7200})
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != true || res["action"] != "publish" {
		t.Fatalf("publish file result = %#v", res)
	}
	publishedFile := res["artifact"].(publicartifacts.PublishResult)
	if publishedFile.Filename != "notes.md" || publishedFile.Archive || !strings.HasPrefix(publishedFile.URL, "https://example.test/artifacts/public/") {
		t.Fatalf("published file = %#v", publishedFile)
	}

	// 2. 本机目录发布（打包为 tar.gz）
	bundleDir := filepath.Join(rt.cfg.AgentDockDefaultDir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirRes, err := rt.RuntimeArtifactPublish(t.Context(), ArtifactPublishInput{Path: bundleDir})
	if err != nil {
		t.Fatal(err)
	}
	publishedDir := dirRes["artifact"].(publicartifacts.PublishResult)
	if !publishedDir.Archive || publishedDir.Filename != "bundle.tar.gz" {
		t.Fatalf("published dir = %#v", publishedDir)
	}

	// 3. 上传字节发布（不把内容写入 trace 参数）
	uploadRes, err := rt.RuntimeArtifactPublish(t.Context(), ArtifactPublishInput{
		Filename: "secret-check.json",
		MimeType: "application/json",
		Data:     []byte(`{"hello":"SECRET_BODY_MARKER"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	publishedUpload := uploadRes["artifact"].(publicartifacts.PublishResult)
	if publishedUpload.Filename != "secret-check.json" || publishedUpload.Size == 0 {
		t.Fatalf("published upload = %#v", publishedUpload)
	}

	// 验证 RuntimeArtifactOpen 可读取上传内容。
	meta, file, err := rt.RuntimeArtifactOpen(publishedUpload.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, readErr := io.ReadAll(file)
	_ = file.Close()
	if readErr != nil || meta.ArtifactID != publishedUpload.ArtifactID || string(gotBytes) != `{"hello":"SECRET_BODY_MARKER"}` {
		t.Fatalf("RuntimeArtifactOpen = meta=%#v data=%q err=%v", meta, gotBytes, readErr)
	}

	// 验证 trace 记录了 artifact_publish 且不泄露上传正文。
	records := rt.traces.Query(observability.TraceQuery{Tool: "artifact_publish"})
	if len(records) != 3 {
		t.Fatalf("artifact_publish trace count = %d, want 3", len(records))
	}
	for _, rec := range records {
		if rec.Source != "internal" || !rec.Success {
			t.Fatalf("unexpected publish trace: %#v", rec)
		}
		if strings.Contains(rec.Arguments, "SECRET_BODY_MARKER") {
			t.Fatalf("upload bytes leaked into trace arguments: %s", rec.Arguments)
		}
	}
}

func TestRuntimeArtifactPublishValidationAndSecurityBoundaries(t *testing.T) {
	rt := newArtifactTestRuntime(t, config.TraceModeMemory)

	existing := filepath.Join(rt.cfg.AgentDockDefaultDir, "ok.txt")
	if err := os.WriteFile(existing, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(rt.cfg.AgentDockDefaultDir, "agentdock.env")
	if err := os.WriteFile(envFile, []byte("AGENTDOCK_AUTH_TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(rt.cfg.AgentDockHome, "secrets", "public-url-secret")
	oauthDir := filepath.Join(rt.cfg.AgentDockHome, "oauth")
	if err := os.MkdirAll(oauthDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oauthFile := filepath.Join(oauthDir, "state-v1.json")
	if err := os.WriteFile(oauthFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		input    ArtifactPublishInput
		wantCode string
	}{
		{name: "empty input", input: ArtifactPublishInput{}, wantCode: "INVALID_ARGUMENT"},
		{name: "both path and upload", input: ArtifactPublishInput{Path: existing, Filename: "a.txt", Data: []byte("a")}, wantCode: "INVALID_ARGUMENT"},
		{name: "empty upload bytes", input: ArtifactPublishInput{Filename: "empty.txt"}, wantCode: "INVALID_ARGUMENT"},
		{name: "oversize upload", input: ArtifactPublishInput{Filename: "big.bin", Data: make([]byte, MaxDashboardPublishBytes+1)}, wantCode: "INVALID_ARGUMENT"},
		{name: "negative retention", input: ArtifactPublishInput{Path: existing, RetentionSeconds: -1}, wantCode: "INVALID_ARGUMENT"},
		{name: "over max retention", input: ArtifactPublishInput{Path: existing, RetentionSeconds: int(publicartifacts.MaxRetention/time.Second) + 1}, wantCode: "INVALID_ARGUMENT"},
		{name: "missing path", input: ArtifactPublishInput{Path: filepath.Join(rt.cfg.AgentDockDefaultDir, "missing.txt")}, wantCode: "NOT_FOUND"},
		{name: "reject agentdock.env", input: ArtifactPublishInput{Path: envFile}, wantCode: "INVALID_ARGUMENT"},
		{name: "reject secrets file", input: ArtifactPublishInput{Path: secretFile}, wantCode: "INVALID_ARGUMENT"},
		{name: "reject oauth file", input: ArtifactPublishInput{Path: oauthFile}, wantCode: "INVALID_ARGUMENT"},
		{name: "reject agentdock home dir", input: ArtifactPublishInput{Path: rt.cfg.AgentDockHome}, wantCode: "INVALID_ARGUMENT"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rt.RuntimeArtifactPublish(t.Context(), tc.input)
			var toolErr *toolcore.ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != tc.wantCode {
				t.Fatalf("%s error = %#v, want code %s", tc.name, err, tc.wantCode)
			}
		})
	}

	_, _, err := rt.RuntimeArtifactOpen("../bad")
	var toolErr *toolcore.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "NOT_FOUND" {
		t.Fatalf("RuntimeArtifactOpen(bad) error = %#v", err)
	}
}
