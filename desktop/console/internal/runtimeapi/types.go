// Package runtimeapi 是 AgentDock Console 的数据层：只依赖本机 Runtime HTTP API
// (/internal/runtime/*)，不 import Core 的 internal 包，保持跨进程契约边界。
//
// JSON 字段与 Core 侧结构逐字段对齐，来源（镜像契约，勿凭记忆改名）：
//   - internal/observability/{recorder,trace}.go   ExecutionRecord / TraceRecord / FileChange
//   - internal/taskstate/model.go                  Task / Condition / Event / FinalReview
//   - internal/publicartifacts/store.go            Metadata / PublishResult
//   - internal/agentinstructions/instructions.go   File / Snapshot
//   - internal/app/{runtime_api,runtime_catalog,runtime_artifacts,runtime_trace_api}.go
//   - internal/tool/task/{runtime_api,result}.go   列表项 compactTaskListItem
//   - internal/tool/skill/runtime_api.go、internal/tool/plugin/service.go
package runtimeapi

import "time"

// Backend 是页面层消费的只读契约；Client 是真实实现，测试用桩替换。
type Backend interface {
	Status() (*Status, error)
	Overview() (*Overview, error)
	Analytics() (*Analytics, error)
	Catalog() (*Catalog, error)
	Skills() (*Skills, error)
	Plugins() (*Plugins, error)
	Tasks(status string, limit int) (*TaskList, error)
	Task(id string) (*TaskDetail, error)
	Traces(query, tool, source, status string, limit int) (*Traces, error)
	FileChanges(query string, limit int) (*FileChanges, error)
	Artifacts() (*Artifacts, error)
}

// APIError 是 runtime API 的错误体 {"ok":false,"code":...,"error":...}。
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"error"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return e.Code + ": " + e.Message
	}
	return e.Message
}

// ---------- GET /internal/runtime/status ----------

type Status struct {
	OK                 bool     `json:"ok"`
	Source             string   `json:"source"`
	Service            string   `json:"service"`
	Version            string   `json:"version"`
	AgentDockHome      string   `json:"agentdock_home"`
	AgentDockDefaultDir string  `json:"agentdock_default_dir"`
	PathModel          string   `json:"path_model"`
	AuthEnabled        bool     `json:"auth_enabled"`
	BrowserEnabled     bool     `json:"browser_enabled"`
	MemoryEnabled      bool     `json:"memory_enabled"`
	NexusEnabled       bool     `json:"nexus_enabled"`
	ToolCount          int      `json:"tool_count"`
	Tools              []string `json:"tools"`
}

// ---------- GET /internal/runtime/overview ----------

type Overview struct {
	OK      bool           `json:"ok"`
	Source  string         `json:"source"`
	Tasks   OverviewTasks  `json:"tasks"`
	Skills  OverviewCount  `json:"skills"`
	Plugins OverviewPlugins `json:"plugins"`
	MCP     OverviewCount  `json:"mcp"`
}

type OverviewTasks struct {
	Active          int `json:"active"`
	Completed       int `json:"completed"`
	Blocked         int `json:"blocked"`
	ActiveRecent24h int `json:"active_recent_24h"`
}

type OverviewCount struct {
	Count int `json:"count"`
}

type OverviewPlugins struct {
	Count     int  `json:"count"`
	Available bool `json:"available"`
}

// ---------- GET /internal/runtime/analytics ----------

type Analytics struct {
	OK             bool            `json:"ok"`
	Source         string          `json:"source"`
	StartedAt      time.Time       `json:"started_at"`
	RecentCapacity int             `json:"recent_capacity"`
	WindowCalls    int             `json:"window_calls"`
	TotalCalls     uint64          `json:"total_calls"`
	TotalErrors    uint64          `json:"total_errors"`
	ActiveCalls    int             `json:"active_calls"`
	ToolStats      []ToolStats     `json:"tool_stats"`
	RecentCalls    []ExecutionRecord `json:"recent_calls"`
	Process        ProcessSnapshot `json:"process"`
}

type ToolStats struct {
	Tool          string  `json:"tool"`
	Count         int     `json:"count"`
	ErrorCount    int     `json:"error_count"`
	ErrorRate     float64 `json:"error_rate"`
	P50DurationMS float64 `json:"p50_duration_ms"`
	P95DurationMS float64 `json:"p95_duration_ms"`
	P99DurationMS float64 `json:"p99_duration_ms"`
}

// ExecutionRecord 是 Analytics 最近调用（无 Payload 投影）。
type ExecutionRecord struct {
	ID            uint64        `json:"id"`
	Tool          string        `json:"tool"`
	Source        string        `json:"source"`
	UserAgent     string        `json:"user_agent,omitempty"`
	TraceID       string        `json:"trace_id,omitempty"`
	SpanID        string        `json:"span_id,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	DurationMS    float64       `json:"duration_ms"`
	Success       bool          `json:"success"`
	ErrorCode     string        `json:"error_code,omitempty"`
	ErrorCategory string        `json:"error_category,omitempty"`
	Stages        []StageRecord `json:"stages,omitempty"`
}

// StageRecord 对齐 observability.StageRecord；字段以源码为准补齐。
type StageRecord struct {
	Name       string  `json:"name"`
	DurationMS float64 `json:"duration_ms,omitempty"`
}

// ProcessSnapshot 对齐 observability.ProcessSnapshot。
type ProcessSnapshot struct {
	Goroutines     int    `json:"goroutines"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
}

// ---------- GET /internal/runtime/catalog ----------

type Catalog struct {
	OK            bool              `json:"ok"`
	Source        string            `json:"source"`
	Tools         []CatalogTool     `json:"tools"`
	ToolCount     int               `json:"tool_count"`
	Workdir       string            `json:"workdir"`
	WorkspaceRoot string            `json:"workspace_root"`
	Rules         []InstructionFile `json:"rules"`
	CommonSkills  CommonSkillIndex  `json:"common_skills"`
}

type CatalogTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
}

type ToolAnnotations struct {
	ReadOnlyHint    bool  `json:"read_only_hint"`
	DestructiveHint *bool `json:"destructive_hint,omitempty"`
	IdempotentHint  bool  `json:"idempotent_hint"`
	OpenWorldHint   *bool `json:"open_world_hint,omitempty"`
}

type InstructionFile struct {
	Scope       string `json:"scope"`
	Path        string `json:"path"`
	Status      string `json:"status"`
	Content     string `json:"content,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	Reason      string `json:"reason,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

type CommonSkillIndex struct {
	Root      string            `json:"root"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
	Items     []CommonSkillItem `json:"items"`
}

type CommonSkillItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
	SkillRef    string `json:"skill_ref"`
	SourceType  string `json:"source_type"`
}

// ---------- GET /internal/runtime/skills ----------

type Skills struct {
	OK     bool        `json:"ok"`
	Source string      `json:"source"`
	Action string      `json:"action"`
	Count  int         `json:"count"`
	Skills []SkillItem `json:"skills"`
}

type SkillItem struct {
	Skill         string `json:"skill"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	SkillRef      string `json:"skill_ref"`
	SourceType    string `json:"source_type"`
	ContentDigest string `json:"content_digest"`
	FileCount     int    `json:"file_count"`
	PluginName    string `json:"plugin_name,omitempty"`
}

// ---------- GET /internal/runtime/plugins ----------

type Plugins struct {
	OK      bool         `json:"ok"`
	Source  string       `json:"source"`
	Count   int          `json:"count"`
	Plugins []PluginItem `json:"plugins"`
}

type PluginItem struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Enabled       bool   `json:"enabled"`
	PackageDigest string `json:"package_digest"`
	Provenance    string `json:"provenance"`
	Format        string `json:"format"`
	SkillCount    int    `json:"skill_count"`
	MCPCount      int    `json:"mcp_count"`
}

// ---------- GET /internal/runtime/tasks ----------

type TaskList struct {
	OK     bool           `json:"ok"`
	Source string         `json:"source"`
	Action string         `json:"action"`
	Count  int            `json:"count"`
	Tasks  []TaskListItem `json:"tasks"`
}

// TaskListItem 对齐 compactTaskListItem 的输出。
type TaskListItem struct {
	ID                 string             `json:"id"`
	Title              string             `json:"title"`
	Goal               string             `json:"goal"`
	Status             string             `json:"status"`
	Phase              string             `json:"phase"`
	CompletedStepCount int                `json:"completed_step_count"`
	StepCount          int                `json:"step_count"`
	ReviewStatus       string             `json:"review_status"`
	UpdatedAt          time.Time          `json:"updated_at"`
	Project            string             `json:"project,omitempty"`
	Device             string             `json:"device,omitempty"`
	CurrentStep        string             `json:"current_step,omitempty"`
	Summary            string             `json:"summary,omitempty"`
	Blocker            string             `json:"blocker,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	EventCount         int                `json:"event_count"`
	CompletedAt        *time.Time         `json:"completed_at,omitempty"`
	SourceTemplates    []TemplateReference `json:"source_templates,omitempty"`
}

type TemplateReference struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

type TaskDetail struct {
	OK     bool   `json:"ok"`
	Source string `json:"source"`
	Action string `json:"action"`
	Task   *Task  `json:"task"`
}

// Task 对齐 taskstate.Task（省略 guidance/evolution 字段；需要时按源码补齐）。
type Task struct {
	SchemaVersion   int                `json:"schema_version"`
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Goal            string             `json:"goal"`
	Project         string             `json:"project,omitempty"`
	Device          string             `json:"device,omitempty"`
	Status          string             `json:"status"`
	Phase           string             `json:"phase"`
	Conditions      []Condition        `json:"conditions"`
	SourceTemplates []TemplateReference `json:"source_templates,omitempty"`
	Steps           []TaskStep         `json:"steps,omitempty"`
	Events          []Event            `json:"events"`
	Blocker         string             `json:"blocker,omitempty"`
	Summary         string             `json:"summary,omitempty"`
	FinalReview     *FinalReview       `json:"final_review,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	CompletedAt     *time.Time         `json:"completed_at,omitempty"`
}

// TaskStep 对齐 taskstate.TaskStep；字段以源码为准补齐（title/phase/…）。
type TaskStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Phase string `json:"phase,omitempty"`
}

type Condition struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type Event struct {
	Type      string    `json:"type"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

type FinalReview struct {
	Status         string   `json:"status"`
	Summary        string   `json:"summary"`
	VerifiedFacts  []string `json:"verified_facts,omitempty"`
	OpenRisks      []string `json:"open_risks,omitempty"`
	MissingChecks  []string `json:"missing_checks,omitempty"`
	ReviewRevision string   `json:"review_revision"`
	ReviewedAt     time.Time `json:"reviewed_at"`
}

// ---------- GET /internal/runtime/traces ----------

type Traces struct {
	OK      bool         `json:"ok"`
	Source  string       `json:"source"`
	Mode    string       `json:"mode,omitempty"`
	Status  *TraceStatus `json:"status,omitempty"`
	Records []TraceRecord `json:"records"`
	Count   int          `json:"count"`
	Tools   []string     `json:"tools"`
}

type TraceStatus struct {
	Mode          string `json:"mode"`
	Root          string `json:"root,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
	MaxRecords    int    `json:"max_records"`
	Count         int    `json:"count"`
	Buffered      int    `json:"buffered"`
}

type TraceRecord struct {
	ID            uint64       `json:"id"`
	Tool          string       `json:"tool"`
	Source        string       `json:"source"`
	UserAgent     string       `json:"user_agent,omitempty"`
	StartedAt     time.Time    `json:"started_at"`
	DurationMS    float64      `json:"duration_ms"`
	Success       bool         `json:"success"`
	ErrorCode     string       `json:"error_code,omitempty"`
	ErrorCategory string       `json:"error_category,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	Arguments     string       `json:"arguments,omitempty"`
	Result        string       `json:"result,omitempty"`
	Error         string       `json:"error,omitempty"`
	Truncated     bool         `json:"truncated,omitempty"`
	FileChanges   []FileChange `json:"file_changes,omitempty"`
}

type FileChange struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	Insertions int    `json:"insertions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
	Diff       string `json:"diff,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// ---------- GET /internal/runtime/file-changes ----------

type FileChanges struct {
	OK      bool             `json:"ok"`
	Source  string           `json:"source"`
	Mode    string           `json:"mode,omitempty"`
	Status  *TraceStatus     `json:"status,omitempty"`
	Changes []FileChangeEntry `json:"changes"`
	Count   int              `json:"count"`
}

type FileChangeEntry struct {
	ID         uint64    `json:"id"`
	TraceID    string    `json:"trace_id,omitempty"`
	Tool       string    `json:"tool"`
	Source     string    `json:"source"`
	UserAgent  string    `json:"user_agent,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	Success    bool      `json:"success"`
	Path       string    `json:"path"`
	Action     string    `json:"action"`
	Insertions int       `json:"insertions,omitempty"`
	Deletions  int       `json:"deletions,omitempty"`
	Diff       string    `json:"diff,omitempty"`
	Truncated  bool      `json:"truncated,omitempty"`
}

// ---------- GET /internal/runtime/artifacts ----------

type Artifacts struct {
	OK            bool       `json:"ok"`
	Source        string     `json:"source"`
	PublicBaseURL string     `json:"public_base_url"`
	Configured    bool       `json:"configured"`
	Artifacts     []Artifact `json:"artifacts"`
	Count         int        `json:"count"`
}

// Artifact 对齐 publicartifacts.PublishResult（Metadata + url）。
type Artifact struct {
	ArtifactID string    `json:"artifact_id"`
	Filename   string    `json:"filename"`
	MimeType   string    `json:"mime_type"`
	SizeBytes  int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Archive    bool      `json:"archive"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	URL        string    `json:"url"`
}
