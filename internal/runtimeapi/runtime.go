package runtimeapi

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/mcp/oauthclient"
	"github.com/uvwt/agentdock/internal/observability"
)

// Runtime 定义 Runtime API 路由真正需要的应用能力。
// HTTP、Nexus Bridge 等传输层都只依赖这份传输无关契约。
type Runtime interface {
	RuntimeStatus() app.Result
	RuntimeAnalytics() app.Result
	RuntimeOverview() (app.Result, error)
	RuntimeSkills() (app.Result, error)
	RuntimeSkill(skill string) (app.Result, error)
	RuntimeSkillFiles(skill string) (app.Result, error)
	RuntimeSkillFile(skill, path string) (app.Result, error)
	RuntimePlugins(context.Context) (app.Result, error)
	RuntimePlugin(context.Context, string) (app.Result, error)
	RuntimeTasks(status string, limit int) (app.Result, error)
	RuntimeTask(id string) (app.Result, error)
	RuntimeTaskDelete(id string) (app.Result, error)
	RuntimeCapabilities(context.Context, bool) (app.Result, error)
	RuntimeMCPServers(context.Context) (app.Result, error)
	RuntimeMCPServer(context.Context, string) (app.Result, error)
	RuntimeMCPManage(context.Context, map[string]any) (app.Result, error)
	RuntimeEvolve(context.Context, map[string]any) (app.Result, error)
}

// MCPOAuthRuntime 是可选能力：只有支持 Remote MCP OAuth 的 Runtime 才实现。
// 保持它独立于 Runtime 主接口，避免 callback relay 把所有测试替身和只读调用方一起扩展。
// DiagnosticsRuntime 是增量只读能力。保持它独立于 Runtime 主接口，
// 让旧实现或测试替身能明确返回“不支持”，而不是被迫实现空方法。
type DiagnosticsRuntime interface {
	RuntimeDiagnostics() app.Result
}

// CatalogRuntime 是桌面控制面板的只读能力：工具定义与规则扫描结果。
// 它不参与 MCP 工具契约，也不从 Nexus 暴露。
type CatalogRuntime interface {
	RuntimeCatalog(context.Context) (app.Result, error)
}

// TraceRuntime 是可选的可观测能力；trace 关闭时实现仍返回 mode=off。
// 查询条件用 observability.TraceQuery 结构携带，让 list 摘要视图和后续过滤
// 条件可以增量扩展，不再给每个传输层重复改一遍参数列表。
type TraceRuntime interface {
	RuntimeTraces(observability.TraceQuery) (app.Result, error)
}

// TraceDetailRuntime 是 trace 详情能力：列表只带摘要，选中记录后按 ID 拉取
// 完整预览；full=true 时再附带旁路全文副本。独立于 TraceRuntime，旧实现可以
// 明确报“不支持”而不是返回空记录。
type TraceDetailRuntime interface {
	RuntimeTraceDetail(id uint64, full bool) (app.Result, error)
}

// FileChangeRuntime 是 trace 之上的文件历史投影，供控制面板构建目录树。
type FileChangeRuntime interface {
	RuntimeFileChanges(query string, limit int) (app.Result, error)
}

// CaptureSource 是网络抓包环的持有者。采集器（HTTP 中间件、Nexus 桥接）与
// 查询 API 必须共用同一实例，否则面板看到的不是自己抓到的流量。
type CaptureSource interface {
	NetworkCapture() *observability.CaptureStore
}

// NetworkRuntime 是网络抓包面板的只读列表能力；采集关闭时实现仍返回
// enabled=false。过滤条件沿用 observability.CaptureQuery，与 trace 同理：
// 不给每个传输层重复改一遍参数列表。
type NetworkRuntime interface {
	RuntimeNetwork(observability.CaptureQuery) (app.Result, error)
}

// NetworkDetailRuntime 是抓包详情能力：列表只带摘要，选中记录后按 ID 拉取
// 完整 header 与 body。独立于列表能力，旧实现可以明确报“不支持”。
type NetworkDetailRuntime interface {
	RuntimeNetworkDetail(id uint64) (app.Result, error)
}

// NetworkClearRuntime 是抓包清空能力：只清内存环。
type NetworkClearRuntime interface {
	RuntimeNetworkClear() (app.Result, error)
}

// ArtifactRuntime 是公开文件发布记录的只读列表。
type ArtifactRuntime interface {
	RuntimeArtifacts(context.Context) (app.Result, error)
}

// ArtifactManageRuntime 是发布物写操作能力：撤销与延期。
// 与只读的 ArtifactRuntime 分开，让旧实现明确不支持写操作，
// 而不是被迫实现空方法把删除解释成空操作。
type ArtifactManageRuntime interface {
	RuntimeArtifactRevoke(id string) (app.Result, error)
	RuntimeArtifactExtend(id string, hours int) (app.Result, error)
}

// TraceExportRuntime 是 trace 导出能力：按与查询相同的过滤语义输出
// JSON/CSV 文本，附带内容类型与建议文件名。
type TraceExportRuntime interface {
	RuntimeTracesExport(format, query, tool, source, status string, limit int) (body []byte, contentType string, filename string, err error)
}

// TraceClearRuntime 是 trace 清空能力：清内存索引与磁盘保留文件。
type TraceClearRuntime interface {
	RuntimeTracesClear() (app.Result, error)
}

type MCPOAuthRuntime interface {
	RuntimeMCPOAuthCallback(context.Context, oauthclient.CallbackResult) error
}

// TaskActivityRuntime 是任务活动聚合能力：从 trace 投影单个任务的调用与文件变更。
// trace 关闭时实现仍返回 mode=off，不视为错误。
type TaskActivityRuntime interface {
	RuntimeTaskActivity(id string) (app.Result, error)
}

// TaskActionRuntime 是任务写操作能力。正文保持为原始 JSON，与
// ResourceLibraryRuntime 同理由：避免 app 为了实现接口反过来导入 runtimeapi。
type TaskActionRuntime interface {
	RuntimeTaskAction(id string, body []byte) (app.Result, error)
}

// AlertRuntime 是阈值告警能力：惰性评估当前活跃告警与进程内去重表。
type AlertRuntime interface {
	RuntimeAlerts() (app.Result, error)
}

// EventStreamRuntime 是 SSE 事件流能力：返回进程内事件订阅通道。
// 流式响应无法装进 Dispatch 的 map 返回值，由 HTTP 传输直接写流；
// 其它传输拿不到该能力时应明确报“不支持”。
type EventStreamRuntime interface {
	RuntimeEventsSubscribe(ctx context.Context) <-chan observability.Event
}

// NexusOAuthCallbackRuntime 由节点 Bridge 在握手后提供 Nexus 公网 callback origin。
type NexusOAuthCallbackRuntime interface {
	SetNexusOAuthCallback(publicURL, nodeID string) error
}

// ResourceLibraryRuntime 是可选能力。旧 Runtime 不实现它时，资源库路由失败关闭，
// 不能把“未声明”解释成允许远程安装。
// 参数保持为方法和正文，避免 app 为了实现接口反过来导入 runtimeapi。
type ResourceLibraryRuntime interface {
	RuntimeResourceLibrary(context.Context, string, []byte) (app.Result, error)
}

const ResourceLibraryPath = "/internal/runtime/resource-library"

// Request 是 HTTP 与 Nexus Bridge 共用的 Runtime API 请求表示。
type Request struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Query  url.Values      `json:"query,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

func (r Request) queryValue(key string) string {
	if r.Query == nil {
		return ""
	}
	return r.Query.Get(key)
}
