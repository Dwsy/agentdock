package runtimeapi

import (
	"encoding/json"
	"errors"
	"io"
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
	Method      string
	Path        string // URL.EscapedPath()，用于校验路径转义
	RawQuery    string
	Query       url.Values
	AuthValues  []string // Authorization 头原值，用于校验 Bearer 存在/缺失
	ContentType string   // Content-Type 头原值（POST/DELETE 带 JSON 体时应为 application/json）
	Body        []byte   // 请求体原值，用于校验 JSON 字段逐字传递
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
		reqBody, _ := io.ReadAll(r.Body)
		rec.record(capturedRequest{
			Method:      r.Method,
			Path:        r.URL.EscapedPath(),
			RawQuery:    r.URL.RawQuery,
			Query:       r.URL.Query(),
			AuthValues:  r.Header.Values("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        reqBody,
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
		OK:      true,
		Source:  "memory",
		Tasks:   OverviewTasks{Active: 1, Completed: 2, Blocked: 3, ActiveRecent24h: 4},
		Skills:  OverviewCount{Count: 5},
		Plugins: OverviewPlugins{Count: 6, Available: true},
		MCP:     OverviewCount{Count: 7},
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

// ---------- 操作闭环契约：POST /internal/runtime/tasks/{id}/actions ----------

func TestClient_TaskActions(t *testing.T) {
	want := TaskActionResponse{
		OK: true, Source: "agentdock-api", Action: "checkpoint", TaskID: "t-7",
		Task: &TaskListItem{
			ID: "t-7", Title: "Ship", Goal: "G", Status: "active", Phase: "exec",
			CompletedStepCount: 2, StepCount: 3, ReviewStatus: "pending",
			UpdatedAt: ts, CreatedAt: ts, EventCount: 5,
		},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)
	c := NewClient(srv.URL, "tok-9")

	got, err := c.TaskActions("t-7", TaskActionsRequest{
		Action:           "checkpoint",
		CompletedStepIDs: []string{"s1", "s2"},
		CurrentStepID:    "s2",
	})
	if err != nil {
		t.Fatalf("TaskActions() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Task == nil || got.Task.ID != "t-7" {
		t.Errorf("TaskActions() task = %+v, want list item t-7", got.Task)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodPost, "/internal/runtime/tasks/t-7/actions")
	if len(req.AuthValues) != 1 || req.AuthValues[0] != "Bearer tok-9" {
		t.Errorf("Authorization = %v, want [Bearer tok-9]", req.AuthValues)
	}
	if req.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", req.ContentType)
	}
	const wantBody = `{"action":"checkpoint","completed_step_ids":["s1","s2"],"current_step_id":"s2"}`
	if string(req.Body) != wantBody {
		t.Errorf("body = %s, want %s", req.Body, wantBody)
	}
}

func TestClient_TaskActions_OptionalFieldsOmitted(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, TaskActionResponse{
		OK: true, Source: "agentdock-api", Action: "delete", TaskID: "t 1/2#3",
	})

	got, err := NewClient(srv.URL, "").TaskActions("t 1/2#3", TaskActionsRequest{Action: "delete"})
	if err != nil {
		t.Fatalf("TaskActions() error: %v", err)
	}
	if got.Action != "delete" || got.TaskID != "t 1/2#3" {
		t.Errorf("TaskActions() = %+v", got)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodPost, "/internal/runtime/tasks/t%201%2F2%233/actions")
	if string(req.Body) != `{"action":"delete"}` {
		t.Errorf("body = %s, want %s", req.Body, `{"action":"delete"}`)
	}
}

func TestClient_TaskActions_InvalidStatus(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusBadRequest, map[string]any{
		"ok": false, "code": "INVALID_STATUS", "error": "任务已完成，不能再保存检查点",
	})

	_, err := NewClient(srv.URL, "").TaskActions("t-1", TaskActionsRequest{Action: "checkpoint"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest || apiErr.Code != "INVALID_STATUS" {
		t.Errorf("APIError = %+v, want StatusCode=400 Code=INVALID_STATUS", apiErr)
	}
	if apiErr.Message != "任务已完成，不能再保存检查点" {
		t.Errorf("Message = %q, want 可读原因", apiErr.Message)
	}
}

func TestClient_TaskActions_Conflict(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusConflict, map[string]any{
		"ok": false, "code": "CONFLICT", "message": "任务已被其他进程更新",
	})

	_, err := NewClient(srv.URL, "").TaskActions("t-1", TaskActionsRequest{Action: "complete"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusConflict || apiErr.Code != "CONFLICT" {
		t.Errorf("APIError = %+v, want StatusCode=409 Code=CONFLICT", apiErr)
	}
	if apiErr.Message != "任务已被其他进程更新" {
		t.Errorf("Message = %q, want message 字段内容", apiErr.Message)
	}
}

// ---------- 操作闭环契约：GET /internal/runtime/tasks/{id}/activity ----------

func TestClient_TaskActivity(t *testing.T) {
	want := TaskActivity{
		OK: true, Source: "agentdock-api", TaskID: "t-7", Mode: "disk",
		Calls: []ActivityCall{{
			ID: 11, Tool: "bash", Source: "cli", UserAgent: "ua/1.0",
			StartedAt: ts, DurationMS: 12.5, Success: false,
			ErrorCode: "E_EXEC", TraceID: "tr-1",
		}},
		FileChanges: []ActivityChange{{
			TraceID: "tr-1", Tool: "write_file", StartedAt: ts,
			Path: "a.go", Action: "modify", Insertions: 3, Deletions: 1,
			Diff: "@@ -1 +1 @@", Truncated: true,
		}},
		CallCount: 1, ChangeCount: 1,
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").TaskActivity("t-7")
	if err != nil {
		t.Fatalf("TaskActivity() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Mode != "disk" || got.CallCount != 1 || got.ChangeCount != 1 {
		t.Errorf("TaskActivity() = %+v", got)
	}
	if got.Calls[0].ID != 11 || got.FileChanges[0].Path != "a.go" {
		t.Errorf("TaskActivity() entries = %+v / %+v", got.Calls, got.FileChanges)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodGet, "/internal/runtime/tasks/t-7/activity")
}

func TestClient_TaskActivity_Off(t *testing.T) {
	want := TaskActivity{
		OK: true, Source: "agentdock-api", TaskID: "t-7", Mode: "off",
		Calls: []ActivityCall{}, FileChanges: []ActivityChange{},
		CallCount: 0, ChangeCount: 0,
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").TaskActivity("t-7")
	if err != nil {
		t.Fatalf("TaskActivity() error: %v", err)
	}
	assertDecoded(t, got, &want)
	if got.Mode != "off" || len(got.Calls) != 0 || len(got.FileChanges) != 0 {
		t.Errorf("TaskActivity() = %+v, want mode=off 且两数组空", got)
	}
	assertRequest(t, rec.get(), http.MethodGet, "/internal/runtime/tasks/t-7/activity")
}

// ---------- 操作闭环契约：DELETE /internal/runtime/artifacts/{id} ----------

func TestClient_ArtifactRevoke(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, map[string]any{
		"ok": true, "source": "agentdock-api", "action": "revoke", "artifact_id": "a-1",
	})

	if err := NewClient(srv.URL, "").ArtifactRevoke("a-1"); err != nil {
		t.Fatalf("ArtifactRevoke() error: %v", err)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodDelete, "/internal/runtime/artifacts/a-1")
	if len(req.Body) != 0 {
		t.Errorf("body = %s, want empty", req.Body)
	}
}

func TestClient_ArtifactRevoke_NotFound(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusNotFound, map[string]any{
		"ok": false, "code": "NOT_FOUND", "error": "artifact not found",
	})

	err := NewClient(srv.URL, "").ArtifactRevoke("missing")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Code != "NOT_FOUND" {
		t.Errorf("APIError = %+v, want StatusCode=404 Code=NOT_FOUND", apiErr)
	}
}

// ---------- 操作闭环契约：POST /internal/runtime/artifacts/{id}/extend ----------

func TestClient_ArtifactExtend(t *testing.T) {
	want := ArtifactAction{
		OK: true, Source: "agentdock-api", Action: "extend",
		Artifact: &Artifact{
			ArtifactID: "a-1", Filename: "shot.png", MimeType: "image/png",
			SizeBytes: 10, SHA256: "sha:3", CreatedAt: ts, ExpiresAt: ts.Add(24 * time.Hour),
			URL: "https://pub.example/a-1",
		},
	}
	srv, rec := serveJSON(t, http.StatusOK, want)

	got, err := NewClient(srv.URL, "").ArtifactExtend("a-1", 24)
	if err != nil {
		t.Fatalf("ArtifactExtend() error: %v", err)
	}
	assertDecoded(t, got, want.Artifact)
	if got.URL != "https://pub.example/a-1" || !got.ExpiresAt.Equal(ts.Add(24*time.Hour)) {
		t.Errorf("ArtifactExtend() = %+v", got)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodPost, "/internal/runtime/artifacts/a-1/extend")
	if string(req.Body) != `{"hours":24}` {
		t.Errorf("body = %s, want %s", req.Body, `{"hours":24}`)
	}
	if req.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", req.ContentType)
	}
}

// ---------- 操作闭环契约：GET /internal/runtime/traces/export ----------

func TestClient_TracesExport_JSON(t *testing.T) {
	// 带 Content-Disposition 的导出响应（serve 只设 Content-Type）。
	rec2 := &requestRecorder{}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec2.record(capturedRequest{
			Method: r.Method, Path: r.URL.EscapedPath(),
			RawQuery: r.URL.RawQuery, Query: r.URL.Query(),
			AuthValues: r.Header.Values("Authorization"), Body: body,
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="traces-2024.json"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"records":[]}`))
	}))
	t.Cleanup(srv2.Close)

	data, contentType, filename, err := NewClient(srv2.URL, "tok").TracesExport("json", "go code", "bash", "cli", "error", 25)
	if err != nil {
		t.Fatalf("TracesExport() error: %v", err)
	}
	if string(data) != `{"records":[]}` {
		t.Errorf("data = %s, want raw stream", data)
	}
	if contentType != "application/json" {
		t.Errorf("contentType = %q, want application/json（去掉 charset 参数）", contentType)
	}
	if filename != "traces-2024.json" {
		t.Errorf("filename = %q, want traces-2024.json（Content-Disposition 解析）", filename)
	}
	req := rec2.get()
	assertRequest(t, req, http.MethodGet, "/internal/runtime/traces/export")
	for key, wantVal := range map[string]string{
		"format": "json", "q": "go code", "tool": "bash", "source": "cli", "status": "error", "limit": "25",
	} {
		if gotVal := req.Query.Get(key); gotVal != wantVal {
			t.Errorf("%s = %q, want %q", key, gotVal, wantVal)
		}
	}
	if len(req.AuthValues) != 1 || req.AuthValues[0] != "Bearer tok" {
		t.Errorf("Authorization = %v, want [Bearer tok]", req.AuthValues)
	}
}

func TestClient_TracesExport_CSV(t *testing.T) {
	rec := &requestRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(capturedRequest{
			Method: r.Method, Path: r.URL.EscapedPath(),
			RawQuery: r.URL.RawQuery, Query: r.URL.Query(),
		})
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=traces.csv")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("id,tool\n1,bash\n"))
	}))
	t.Cleanup(srv.Close)

	data, contentType, filename, err := NewClient(srv.URL, "").TracesExport("csv", "", "", "", "", 0)
	if err != nil {
		t.Fatalf("TracesExport() error: %v", err)
	}
	if string(data) != "id,tool\n1,bash\n" {
		t.Errorf("data = %q, want csv 原始流", data)
	}
	if contentType != "text/csv" {
		t.Errorf("contentType = %q, want text/csv", contentType)
	}
	if filename != "traces.csv" {
		t.Errorf("filename = %q, want traces.csv（无引号 filename 解析）", filename)
	}
	req := rec.get()
	if req.Query.Get("format") != "csv" {
		t.Errorf("format = %q, want csv", req.Query.Get("format"))
	}
	if req.RawQuery != "format=csv" {
		t.Errorf("raw query = %q, want 只带 format", req.RawQuery)
	}
}

func TestClient_TracesExport_Error(t *testing.T) {
	srv, _ := serveJSON(t, http.StatusBadRequest, map[string]any{
		"ok": false, "code": "BAD_FORMAT", "error": "unsupported format",
	})

	_, _, _, err := NewClient(srv.URL, "").TracesExport("xml", "", "", "", "", 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != "BAD_FORMAT" || apiErr.Message != "unsupported format" {
		t.Errorf("APIError = %+v", apiErr)
	}
}

// ---------- 操作闭环契约：DELETE /internal/runtime/traces ----------

func TestClient_TracesClear(t *testing.T) {
	srv, rec := serveJSON(t, http.StatusOK, map[string]any{
		"ok": true, "source": "agentdock-api", "action": "clear", "cleared": 12,
	})

	n, err := NewClient(srv.URL, "").TracesClear()
	if err != nil {
		t.Fatalf("TracesClear() error: %v", err)
	}
	if n != 12 {
		t.Errorf("cleared = %d, want 12", n)
	}
	req := rec.get()
	assertRequest(t, req, http.MethodDelete, "/internal/runtime/traces")
	if len(req.Body) != 0 {
		t.Errorf("body = %s, want empty", req.Body)
	}
}
