package runtimeapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 测试脚手架 ----------

type capturedRequest struct {
	Method     string
	Path       string // URL.EscapedPath()，用于校验路径转义
	RawQuery   string
	Query      url.Values
	AuthValues []string // Authorization 头原值，用于校验 Bearer 存在/缺失
}

type requestRecorder struct {
	mu   sync.Mutex
	last capturedRequest
}

func (r *requestRecorder) record(req capturedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = req
}

func (r *requestRecorder) get() capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// serve 起一个固定响应的 httptest 服务并记录最后一个请求。
func serve(t *testing.T, status int, body []byte, contentType string) (*httptest.Server, *requestRecorder) {
	t.Helper()
	rec := &requestRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(capturedRequest{
			Method:     r.Method,
			Path:       r.URL.EscapedPath(),
			RawQuery:   r.URL.RawQuery,
			Query:      r.URL.Query(),
			AuthValues: r.Header.Values("Authorization"),
		})
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// serveJSON 用结构体 Marshal 生成响应体，保证 JSON 字段名与 types.go 的 json tag 一致。
func serveJSON(t *testing.T, status int, payload any) (*httptest.Server, *requestRecorder) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return serve(t, status, body, "application/json")
}

func assertRequest(t *testing.T, req capturedRequest, method, path string) {
	t.Helper()
	if req.Method != method {
		t.Errorf("method = %q, want %q", req.Method, method)
	}
	if req.Path != path {
		t.Errorf("path = %q, want %q", req.Path, path)
	}
}

// assertDecoded 比较整体解码结果（经 JSON 归一化，规避 time.Time 内部表示差异）。
func assertDecoded(t *testing.T, got, want any) {
	t.Helper()
	gb, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wb, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if string(gb) != string(wb) {
		t.Errorf("decoded value mismatch:\n got: %s\nwant: %s", gb, wb)
	}
}

var ts = time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

// ---------- 各端点成功解码 ----------

func TestClient_Status(t *testing.T) {
	want := Status{
		OK:                  true,
		Source:              "memory",
		Service:             "agentdock",
		Version:             "1.2.3",
		AgentDockHome:       "/tmp/home",
		AgentDockDefaultDir: "/tmp/home/.agentdock",
		PathModel:           "flat",
		AuthEnabled:         true,
		BrowserEnabled:      true,
		MemoryEnabled:       true,
		ToolCount:           2,
		Tools:               []string{"bash", "read_file"},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Service != "agentdock" || got.ToolCount != 2 {
		t.Errorf("Status() = %+v, want service=agentdock tool_count=2", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/status")
}

func TestClient_Overview(t *testing.T) {
	want := Overview{
		OK:     true,
		Source: "memory",
		Tasks:  OverviewTasks{Active: 1, Completed: 2, Blocked: 3, ActiveRecent24h: 4},
		Skills: OverviewCount{Count: 5},
		Plugins: OverviewPlugins{Count: 6, Available: true},
		MCP:    OverviewCount{Count: 7},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Overview()
	if err != nil {
		t.Fatalf("Overview() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Tasks.Active != 1 || !got.Plugins.Available {
		t.Errorf("Overview() = %+v", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/overview")
}

func TestClient_Analytics(t *testing.T) {
	want := Analytics{
		OK:             true,
		Source:         "recorder",
		StartedAt:      ts,
		RecentCapacity: 10,
		WindowCalls:    3,
		TotalCalls:     42,
		TotalErrors:    1,
		ActiveCalls:    0,
		ToolStats: []ToolStats{{
			Tool: "bash", Count: 10, ErrorCount: 1, ErrorRate: 0.1,
			P50DurationMS: 12.5, P95DurationMS: 30, P99DurationMS: 44,
		}},
		RecentCalls: []ExecutionRecord{{
			ID: 7, Tool: "bash", Source: "cli", UserAgent: "ua", TraceID: "tr", SpanID: "sp",
			StartedAt: ts, DurationMS: 12.5, Success: true,
			Stages: []StageRecord{{Name: "exec", DurationMS: 1}},
		}},
		Process: ProcessSnapshot{Goroutines: 8, HeapAllocBytes: 1024},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Analytics()
	if err != nil {
		t.Fatalf("Analytics() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.TotalCalls != 42 || got.Process.Goroutines != 8 {
		t.Errorf("Analytics() = %+v", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/analytics")
}

func TestClient_Catalog(t *testing.T) {
	destructive := false
	want := Catalog{
		OK:     true,
		Source: "catalog",
		Tools: []CatalogTool{{
			Name:        "bash",
			Title:       "Bash",
			Description: "run shell",
			InputSchema: map[string]any{"type": "object"},
			Annotations: &ToolAnnotations{
				ReadOnlyHint:    false,
				DestructiveHint: &destructive,
				IdempotentHint:  false,
				OpenWorldHint:   nil,
			},
		}},
		ToolCount:     1,
		Workdir:       "/w",
		WorkspaceRoot: "/w/root",
		Rules: []InstructionFile{{
			Scope: "project", Path: "/w/AGENTS.md", Status: "ok",
			Content: "rules", SHA256: "abc", SizeBytes: 5,
		}},
		CommonSkills: CommonSkillIndex{
			Root: "/skills", Total: 1, Truncated: false,
			Items: []CommonSkillItem{{
				Name: "review", Description: "d", File: "SKILL.md",
				SkillRef: "skill://review", SourceType: "common",
			}},
		},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Catalog()
	if err != nil {
		t.Fatalf("Catalog() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Tools[0].Annotations == nil || *got.Tools[0].Annotations.DestructiveHint {
		t.Errorf("Catalog() annotations = %+v", got.Tools[0].Annotations)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/catalog")
}

func TestClient_Skills(t *testing.T) {
	want := Skills{
		OK: true, Source: "skills", Action: "list", Count: 1,
		Skills: []SkillItem{{
			Skill: "review", Name: "Review", Description: "review code",
			SkillRef: "skill://review", SourceType: "plugin",
			ContentDigest: "sha:1", FileCount: 2, PluginName: "devtools",
		}},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Skills()
	if err != nil {
		t.Fatalf("Skills() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Count != 1 || got.Skills[0].PluginName != "devtools" {
		t.Errorf("Skills() = %+v", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/skills")
}

func TestClient_Plugins(t *testing.T) {
	want := Plugins{
		OK: true, Source: "plugins", Count: 1,
		Plugins: []PluginItem{{
			Name: "devtools", Version: "1.0.0", Enabled: true,
			PackageDigest: "sha:2", Provenance: "local", Format: "zip",
			SkillCount: 3, MCPCount: 1,
		}},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Plugins()
	if err != nil {
		t.Fatalf("Plugins() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if !got.Plugins[0].Enabled || got.Plugins[0].MCPCount != 1 {
		t.Errorf("Plugins() = %+v", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/plugins")
}

func TestClient_Artifacts(t *testing.T) {
	want := Artifacts{
		OK: true, Source: "artifacts",
		PublicBaseURL: "https://pub.example", Configured: true,
		Artifacts: []Artifact{{
			ArtifactID: "a-1", Filename: "shot.png", MimeType: "image/png",
			SizeBytes: 10, SHA256: "sha:3", CreatedAt: ts, ExpiresAt: ts.Add(time.Hour),
			Archive: false, Width: 2, Height: 3, URL: "https://pub.example/a-1",
		}},
		Count: 1,
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Artifacts()
	if err != nil {
		t.Fatalf("Artifacts() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Artifacts[0].URL != "https://pub.example/a-1" || got.Count != 1 {
		t.Errorf("Artifacts() = %+v", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/artifacts")
}

// ---------- query 参数传递 ----------

func TestClient_Tasks_Query(t *testing.T) {
	want := TaskList{
		OK: true, Source: "disk", Action: "list", Count: 1,
		Tasks: []TaskListItem{{
			ID: "t-1", Title: "Ship", Goal: "G", Status: "running", Phase: "exec",
			CompletedStepCount: 1, StepCount: 2, ReviewStatus: "pending",
			UpdatedAt: ts, CreatedAt: ts, EventCount: 3,
		}},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Tasks("running", 10)
	if err != nil {
		t.Fatalf("Tasks() error: %v", err)
	}
	assertDecoded(t, got, &want)
	req := rec.get()
	assertRequest(t, req, http.MethodGet, "/internal/runtime/tasks")
	if req.Query.Get("status") != "running" {
		t.Errorf("status = %q, want %q", req.Query.Get("status"), "running")
	}
	if req.Query.Get("limit") != "10" {
		t.Errorf("limit = %q, want %q", req.Query.Get("limit"), "10")
	}
}

func TestClient_Tasks_DefaultLimit(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, TaskList{OK: true, Source: "disk", Action: "list"})
	c := NewClient(srv.URL, "")

	if _, err := c.Tasks("", 0); err != nil {
		t.Fatalf("Tasks() error: %v", err)
	}
	req := rec.get()
	if req.Query.Get("limit") != "50" {
		t.Errorf("limit = %q, want default %q", req.Query.Get("limit"), "50")
	}
	if req.Query.Has("status") {
		t.Errorf("status = %q, want omitted when empty", req.Query.Get("status"))
	}

	if _, err := c.Tasks("", -3); err != nil {
		t.Fatalf("Tasks() error: %v", err)
	}
	if rec.get().Query.Get("limit") != "50" {
		t.Errorf("limit for negative input = %q, want %q", rec.get().Query.Get("limit"), "50")
	}
}

func TestClient_Traces_Query(t *testing.T) {
	want := Traces{
		OK: true, Source: "buffer", Mode: "buffer",
		Status: &TraceStatus{Mode: "buffer", MaxRecords: 100, Count: 1, Buffered: 1},
		Records: []TraceRecord{{
			ID: 1, Tool: "bash", Source: "cli", StartedAt: ts, DurationMS: 1.5,
			Success: true, TraceID: "t-1", Arguments: "{}", Result: "ok",
			FileChanges: []FileChange{{Path: "a.go", Action: "modify", Insertions: 2, Deletions: 1, Diff: "@@@"}},
		}},
		Count: 1,
		Tools: []string{"bash"},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Traces("go code", "bash", "cli", "error", 25)
	if err != nil {
		t.Fatalf("Traces() error: %v", err)
	}
	assertDecoded(t, got, &want)
	req := rec.get()
	assertRequest(t, req, http.MethodGet, "/internal/runtime/traces")
	for key, wantVal := range map[string]string{
		"q": "go code", "tool": "bash", "source": "cli", "status": "error", "limit": "25",
	} {
		if gotVal := req.Query.Get(key); gotVal != wantVal {
			t.Errorf("%s = %q, want %q", key, gotVal, wantVal)
		}
	}
}

func TestClient_Traces_NoParams(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, Traces{OK: true, Source: "buffer", Records: []TraceRecord{}})

	if _, err := NewClient(srv.URL, "").Traces("", "", "", "", 0); err != nil {
		t.Fatalf("Traces() error: %v", err)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodGet, "/internal/runtime/traces")
	if req.RawQuery != "" {
		t.Errorf("raw query = %q, want empty", req.RawQuery)
	}
}

func TestClient_FileChanges_Query(t *testing.T) {
	want := FileChanges{
		OK: true, Source: "buffer", Mode: "file-changes",
		Changes: []FileChangeEntry{{
			ID: 9, TraceID: "t-9", Tool: "write_file", Source: "cli", StartedAt: ts,
			Success: true, Path: "a.go", Action: "create", Insertions: 3, Diff: "+pkg",
		}},
		Count: 1,
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").FileChanges("patch me", 5)
	if err != nil {
		t.Fatalf("FileChanges() error: %v", err)
	}
	assertDecoded(t, got, &want)
	req := rec.get()
	assertRequest(t, req, http.MethodGet, "/internal/runtime/file-changes")
	if req.Query.Get("q") != "patch me" {
		t.Errorf("q = %q, want %q", req.Query.Get("q"), "patch me")
	}
	if req.Query.Get("limit") != "5" {
		t.Errorf("limit = %q, want %q", req.Query.Get("limit"), "5")
	}
}

// ---------- 任务详情与路径转义 ----------

func TestClient_Task_Detail(t *testing.T) {
	want := TaskDetail{
		OK: true, Source: "disk", Action: "get",
		Task: &Task{
			SchemaVersion: 1, ID: "task-42", Title: "Ship", Goal: "G",
			Status: "active", Phase: "exec",
			Conditions: []Condition{{ID: "c1", Text: "cond", CreatedAt: ts}},
			Steps:      []TaskStep{{ID: "s1", Title: "step1", Phase: "exec"}},
			Events:     []Event{{Type: "created", Summary: "s", CreatedAt: ts}},
			CreatedAt:  ts, UpdatedAt: ts,
		},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").Task("task-42")
	if err != nil {
		t.Fatalf("Task() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Task == nil || got.Task.ID != "task-42" {
		t.Errorf("Task() = %+v", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/tasks/task-42")
}

func TestClient_Task_PathEscaping(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, TaskDetail{OK: true, Source: "disk", Action: "get", Task: &Task{ID: "x"}})

	id := "a/b c#d?e"
	if _, err := NewClient(srv.URL, "").Task(id); err != nil {
		t.Fatalf("Task() error: %v", err)
	}
	req := rec.get()
	wantPath := "/internal/runtime/tasks/" + url.PathEscape(id)
	assertRequest(t, req, http.MethodGet, wantPath)
	if req.Path != "/internal/runtime/tasks/a%2Fb%20c%23d%3Fe" {
		t.Errorf("escaped path = %q, want %q", req.Path, "/internal/runtime/tasks/a%2Fb%20c%23d%3Fe")
	}
}

// ---------- 鉴权头 ----------

func TestAuth_BearerToken(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, Status{OK: true, Source: "memory"})

	if _, err := NewClient(srv.URL, "tok-123").Status(); err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	got := rec.get().AuthValues
	if len(got) != 1 || got[0] != "Bearer tok-123" {
		t.Errorf("Authorization = %v, want [Bearer tok-123]", got)
	}
}

func TestAuth_NoToken(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, Status{OK: true, Source: "memory"})

	if _, err := NewClient(srv.URL, "").Status(); err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if got := rec.get().AuthValues; len(got) != 0 {
		t.Errorf("Authorization = %v, want header absent", got)
	}
}

// ---------- 错误映射 ----------

func TestError_OKFalseBody(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusOK, map[string]any{
		"ok": false, "code": "not_ready", "error": "warming up",
	})

	_, err := NewClient(srv.URL, "").Status()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusOK || apiErr.Code != "not_ready" || apiErr.Message != "warming up" {
		t.Errorf("APIError = %+v, want StatusCode=200 Code=not_ready Message=warming up", apiErr)
	}
	if err.Error() != "not_ready: warming up" {
		t.Errorf("Error() = %q, want %q", err.Error(), "not_ready: warming up")
	}
}

func TestError_HTTP4xx(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusNotFound, map[string]any{
		"ok": false, "code": "not_found", "error": "no such task",
	})

	_, err := NewClient(srv.URL, "").Task("missing")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Code != "not_found" || apiErr.Message != "no such task" {
		t.Errorf("APIError = %+v, want StatusCode=404 Code=not_found Message=no such task", apiErr)
	}
}

func TestError_HTTP5xxPlainText(t *testing.T) {
	srv, _ := serve(t, http.StatusInternalServerError, []byte("boom"), "text/plain")

	_, err := NewClient(srv.URL, "").Overview()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if apiErr.Code != "" {
		t.Errorf("Code = %q, want empty", apiErr.Code)
	}
	if apiErr.Message != "status 500: boom" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "status 500: boom")
	}
}

func TestError_DecodeFailure(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, []byte("{not-json"), "application/json")

	_, err := NewClient(srv.URL, "").Status()
	if err == nil {
		t.Fatal("Status() error = nil, want decode error")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want plain error (not *APIError)", err)
	}
	if !strings.Contains(err.Error(), pathStatus) {
		t.Errorf("err = %v, want endpoint %q in message", err, pathStatus)
	}
}

func TestError_ConnectionFailure(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusOK, Status{OK: true})
	base := srv.URL
	srv.Close()

	_, err := NewClient(base, "").Status()
	if err == nil {
		t.Fatal("Status() error = nil, want connection error")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want plain error (not *APIError)", err)
	}
	if !strings.Contains(err.Error(), pathStatus) {
		t.Errorf("err = %v, want endpoint %q in message", err, pathStatus)
	}
}

// ---------- LoadConfig ----------

func clearRuntimeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AGENTDOCK_HOST", "AGENTDOCK_PORT", "AGENTDOCK_AUTH_TOKEN", "AGENTDOCK_ENV_FILE"} {
		t.Setenv(k, "")
	}
}

func writeEnvFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
}

func defaultEnvPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "AgentDock", "agentdock.env")
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("HOME", t.TempDir()) // 空目录：默认 env 文件不存在

	baseURL, token, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if baseURL != "http://127.0.0.1:8765" {
		t.Errorf("baseURL = %q, want %q", baseURL, "http://127.0.0.1:8765")
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestLoadConfig_EnvVars(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTDOCK_HOST", "10.0.0.5")
	t.Setenv("AGENTDOCK_PORT", "9000")
	t.Setenv("AGENTDOCK_AUTH_TOKEN", "sekret")

	baseURL, token, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if baseURL != "http://10.0.0.5:9000" {
		t.Errorf("baseURL = %q, want %q", baseURL, "http://10.0.0.5:9000")
	}
	if token != "sekret" {
		t.Errorf("token = %q, want %q", token, "sekret")
	}
}

func TestLoadConfig_DefaultEnvFileFillsMissing(t *testing.T) {
	clearRuntimeEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeEnvFile(t, defaultEnvPath(home),
		"# AgentDock env\nAGENTDOCK_HOST=192.168.1.9\nAGENTDOCK_PORT=9999\nAGENTDOCK_AUTH_TOKEN=\"filetok\"\n")

	baseURL, token, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if baseURL != "http://192.168.1.9:9999" {
		t.Errorf("baseURL = %q, want %q", baseURL, "http://192.168.1.9:9999")
	}
	if token != "filetok" {
		t.Errorf("token = %q, want %q (quotes stripped)", token, "filetok")
	}
}

func TestLoadConfig_EnvVarsWinOverEnvFile(t *testing.T) {
	clearRuntimeEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeEnvFile(t, defaultEnvPath(home),
		"export AGENTDOCK_HOST=192.168.1.9\nAGENTDOCK_PORT=9999\nAGENTDOCK_AUTH_TOKEN=filetok\n")
	t.Setenv("AGENTDOCK_HOST", "realhost")

	baseURL, token, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if baseURL != "http://realhost:9999" {
		t.Errorf("baseURL = %q, want %q (env host wins, file fills port)", baseURL, "http://realhost:9999")
	}
	if token != "filetok" {
		t.Errorf("token = %q, want %q (file fills missing token)", token, "filetok")
	}
}

func TestLoadConfig_ExplicitEnvFile(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "custom.env")
	writeEnvFile(t, path, "AGENTDOCK_HOST=env-file-host\nAGENTDOCK_PORT=7000\nAGENTDOCK_AUTH_TOKEN=tok\n")
	t.Setenv("AGENTDOCK_ENV_FILE", path)

	baseURL, token, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if baseURL != "http://env-file-host:7000" || token != "tok" {
		t.Errorf("LoadConfig() = %q, %q; want http://env-file-host:7000, tok", baseURL, token)
	}
}

func TestLoadConfig_ExplicitEnvFileMissing(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTDOCK_ENV_FILE", filepath.Join(t.TempDir(), "nope.env"))

	baseURL, token, err := LoadConfig()
	if err == nil {
		t.Fatalf("LoadConfig() = %q, %q, nil; want error for missing explicit env file", baseURL, token)
	}
	if baseURL != "" || token != "" {
		t.Errorf("LoadConfig() = %q, %q, want empty values on error", baseURL, token)
	}
}

func TestLoadConfig_EnvFileInvalidLine(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "custom.env")
	writeEnvFile(t, path, "AGENTDOCK_HOST=h\nNOT A KEY VALUE\n")
	t.Setenv("AGENTDOCK_ENV_FILE", path)

	_, _, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "NOT A KEY VALUE") {
		t.Errorf("err = %v, want offending line in message", err)
	}
}
