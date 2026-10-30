import AppKit
import Foundation
import ServiceManagement

struct HealthPayload: Decodable {
    let ok: Bool
    let version: String
}

struct DesktopServiceStatusPayload: Decodable {
    let nexusConnected: Bool

    private enum CodingKeys: String, CodingKey {
        case nexusConnected = "nexus_connected"
    }
}

// Runtime API 路径集中在一处：dashboard 计数只允许走 overview，
// 能力目录/任务/trace 是独立只读端点，两者不能互相替代。
private enum RuntimeAPIPath {
    static let skills = "/internal/runtime/skills"
    static let plugins = "/internal/runtime/plugins"
    static let catalog = "/internal/runtime/catalog"
    static let tasks = "/internal/runtime/tasks"
    static let traces = "/internal/runtime/traces"
    static let traceDetail = "/internal/runtime/traces/detail"
    static let networkCapture = "/internal/runtime/network"
    static let networkCaptureDetail = "/internal/runtime/network/detail"
    static let fileChanges = "/internal/runtime/file-changes"
    static let artifacts = "/internal/runtime/artifacts"
}

// MARK: - Runtime capability catalog

/// 任意 JSON 值。工具 Schema 是开放结构，控制面板只需要把它格式化成可读文本，
/// 因此不能为每个工具声明静态字段。
enum RuntimeJSONValue: Decodable {
    case object([String: RuntimeJSONValue])
    case array([RuntimeJSONValue])
    case string(String)
    case number(Double)
    case bool(Bool)
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
            return
        }
        if let value = try? container.decode(Bool.self) {
            self = .bool(value)
            return
        }
        if let value = try? container.decode(Double.self) {
            self = .number(value)
            return
        }
        if let value = try? container.decode(String.self) {
            self = .string(value)
            return
        }
        if let value = try? container.decode([String: RuntimeJSONValue].self) {
            self = .object(value)
            return
        }
        if let value = try? container.decode([RuntimeJSONValue].self) {
            self = .array(value)
            return
        }
        throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value")
    }

    var foundationValue: Any {
        switch self {
        case .object(let value): return value.mapValues(\.foundationValue)
        case .array(let value): return value.map(\.foundationValue)
        case .string(let value): return value
        case .number(let value): return value
        case .bool(let value): return value
        case .null: return NSNull()
        }
    }

    var prettyText: String {
        guard JSONSerialization.isValidJSONObject(foundationValue),
              let data = try? JSONSerialization.data(
                withJSONObject: foundationValue,
                options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
              ),
              let text = String(data: data, encoding: .utf8) else {
            return ""
        }
        return text
    }
}

struct RuntimeToolAnnotations: Decodable {
    let readOnlyHint: Bool
    let destructiveHint: Bool?
    let idempotentHint: Bool
    let openWorldHint: Bool?

    private enum CodingKeys: String, CodingKey {
        case readOnlyHint = "read_only_hint"
        case destructiveHint = "destructive_hint"
        case idempotentHint = "idempotent_hint"
        case openWorldHint = "open_world_hint"
    }
}

struct RuntimeCatalogTool: Decodable, Identifiable {
    let name: String
    let title: String?
    let description: String
    let inputSchema: RuntimeJSONValue?
    let outputSchema: RuntimeJSONValue?
    let annotations: RuntimeToolAnnotations?

    var id: String { name }

    private enum CodingKeys: String, CodingKey {
        case name, title, description, annotations
        case inputSchema = "input_schema"
        case outputSchema = "output_schema"
    }
}

struct RuntimeCatalogRule: Decodable, Identifiable {
    let scope: String
    let path: String
    let status: String
    let content: String?
    let sha256: String?
    let sizeBytes: Int64?
    let reason: String?
    let duplicateOf: String?

    var id: String { path }

    private enum CodingKeys: String, CodingKey {
        case scope, path, status, content, sha256, reason
        case sizeBytes = "size_bytes"
        case duplicateOf = "duplicate_of"
    }
}

struct RuntimeCommonSkillItem: Decodable, Identifiable {
    let name: String
    let description: String
    let file: String
    let skillRef: String
    let sourceType: String

    var id: String { skillRef }

    private enum CodingKeys: String, CodingKey {
        case name, description, file
        case skillRef = "skill_ref"
        case sourceType = "source_type"
    }
}

struct RuntimeCommonSkillIndex: Decodable {
    let root: String
    let total: Int
    let truncated: Bool
    let items: [RuntimeCommonSkillItem]
}

struct RuntimeCatalogPayload: Decodable {
    let tools: [RuntimeCatalogTool]
    let rules: [RuntimeCatalogRule]
    let workdir: String
    let workspaceRoot: String
    let commonSkills: RuntimeCommonSkillIndex?

    private enum CodingKeys: String, CodingKey {
        case tools, rules, workdir
        case workspaceRoot = "workspace_root"
        case commonSkills = "common_skills"
    }
}

struct RuntimeSkillItem: Decodable, Identifiable {
    let name: String
    let description: String
    let skillRef: String
    let sourceType: String
    let contentDigest: String
    let pluginName: String?
    let fileCount: Int

    var id: String { skillRef }

    private enum CodingKeys: String, CodingKey {
        case name, description, fileCount
        case skillRef = "skill_ref"
        case sourceType = "source_type"
        case contentDigest = "content_digest"
        case pluginName = "plugin_name"
    }
}

struct RuntimeSkillsPayload: Decodable {
    let count: Int
    let skills: [RuntimeSkillItem]
}

struct RuntimePluginProvenance: Decodable {
    let origin: String
    let ref: String?
    let revision: String?
    let subdir: String?

    var summary: String {
        var parts = [origin]
        if let ref, !ref.isEmpty { parts.append(ref) }
        if let revision, !revision.isEmpty { parts.append(revision) }
        if let subdir, !subdir.isEmpty { parts.append(subdir) }
        return parts.joined(separator: " · ")
    }
}

struct RuntimePluginItem: Decodable, Identifiable {
    let name: String
    let version: String
    let enabled: Bool
    let packageDigest: String
    let provenance: RuntimePluginProvenance?
    let format: String
    let skillCount: Int
    let mcpCount: Int

    var id: String { name }

    private enum CodingKeys: String, CodingKey {
        case name, version, enabled, provenance, format
        case packageDigest = "package_digest"
        case skillCount = "skill_count"
        case mcpCount = "mcp_count"
    }
}

struct RuntimePluginsPayload: Decodable {
    let count: Int
    let plugins: [RuntimePluginItem]
}

struct RuntimePluginSkill: Decodable, Identifiable {
    let name: String
    let description: String
    let path: String

    var id: String { name + "·" + path }

    private enum CodingKeys: String, CodingKey {
        case name, description
        case path = "path"
    }
}

struct RuntimePluginMCP: Decodable, Identifiable {
    let name: String
    let description: String
    let transport: String
    let url: String?
    let command: String?
    let runtimeName: String?

    var id: String { name }

    private enum CodingKeys: String, CodingKey {
        case name, description, transport, url, command
        case runtimeName = "runtime_name"
    }
}

struct RuntimePluginDetail: Decodable {
    let name: String
    let version: String
    let description: String?
    let enabled: Bool
    let packageDigest: String
    let format: String
    let skills: [RuntimePluginSkill]?
    let mcp: [RuntimePluginMCP]?
    let executables: [String]?
    let warnings: [String]?

    private enum CodingKeys: String, CodingKey {
        case name, version, description, enabled, format, skills, mcp, executables, warnings
        case packageDigest = "package_digest"
    }
}

struct RuntimePluginDetailPayload: Decodable {
    let plugin: RuntimePluginDetail
}

struct RuntimeTaskRow: Decodable, Identifiable {
    let id: String
    let title: String
    let status: String
    let phase: String
    let project: String?
    let device: String?
    let completedStepCount: Int
    let stepCount: Int
    let updatedAt: String
    let completedAt: String?
    let blocker: String?

    private enum CodingKeys: String, CodingKey {
        case id, title, status, phase, project, device, blocker
        case completedStepCount = "completed_step_count"
        case stepCount = "step_count"
        case updatedAt = "updated_at"
        case completedAt = "completed_at"
    }
}

struct RuntimeTasksPayload: Decodable {
    let count: Int
    let tasks: [RuntimeTaskRow]
}

struct RuntimeTaskCondition: Decodable, Identifiable {
    let id: String
    let text: String
}

struct RuntimeTaskStep: Decodable, Identifiable {
    let id: String
    let title: String
    let phase: String
    let status: String
    let updatedAt: String?

    private enum CodingKeys: String, CodingKey {
        case id, title, phase, status
        case updatedAt = "updated_at"
    }
}

struct RuntimeTaskEvent: Decodable, Identifiable {
    let type: String
    let summary: String
    let createdAt: String

    var id: String { createdAt + type + summary }

    private enum CodingKeys: String, CodingKey {
        case type, summary
        case createdAt = "created_at"
    }
}

struct RuntimeTaskReview: Decodable {
    let status: String
    let summary: String
    let verifiedFacts: [String]?
    let openRisks: [String]?
    let missingChecks: [String]?

    private enum CodingKeys: String, CodingKey {
        case status, summary
        case verifiedFacts = "verified_facts"
        case openRisks = "open_risks"
        case missingChecks = "missing_checks"
    }
}

struct RuntimeTaskDetail: Decodable, Identifiable {
    let id: String
    let title: String
    let goal: String
    let project: String?
    let device: String?
    let status: String
    let phase: String
    let conditions: [RuntimeTaskCondition]?
    let steps: [RuntimeTaskStep]?
    let events: [RuntimeTaskEvent]?
    let blocker: String?
    let summary: String?
    let finalReview: RuntimeTaskReview?
    let createdAt: String
    let updatedAt: String
    let completedAt: String?

    private enum CodingKeys: String, CodingKey {
        case id, title, goal, project, device, status, phase, conditions, steps, events, blocker, summary
        case finalReview = "final_review"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case completedAt = "completed_at"
    }
}

struct RuntimeTaskDetailPayload: Decodable {
    let task: RuntimeTaskDetail
}

struct RuntimeTraceRecord: Decodable, Identifiable {
    let id: UInt64
    let tool: String
    let source: String
    let userAgent: String?
    let startedAt: String
    let durationMS: Double
    let success: Bool
    let errorCode: String?
    let arguments: String?
    let result: String?
    let error: String?
    let truncated: Bool?
    /// 命令类工具的结构化入参/出参；其他工具为 nil。
    let command: RuntimeTraceCommand?
    /// file_edit 成功时的文件级变更摘要；其他工具为 nil。摘要投影不会剥离它，
    /// 所以列表行也能拿到变更计数。
    let fileChanges: [RuntimeFileChange]?

    private enum CodingKeys: String, CodingKey {
        case id, tool, source, success, arguments, result, error, truncated, command
        case userAgent = "user_agent"
        case startedAt = "started_at"
        case durationMS = "duration_ms"
        case errorCode = "error_code"
        case fileChanges = "file_changes"
    }
}

/// 单个文件的一次变更（与 Go 侧 observability.FileChange 对应）。
/// insertions/deletions 在 Go 侧是 omitempty 的 int，缺失时为 nil。
struct RuntimeFileChange: Decodable, Identifiable {
    let path: String
    let action: String
    let moveTo: String?
    let projectRoot: String?
    let insertions: Int?
    let deletions: Int?
    let diff: String?
    let truncated: Bool?

    var id: String { action + "\u{0}" + path + "\u{0}" + (moveTo ?? "") }

    private enum CodingKeys: String, CodingKey {
        case path, action, insertions, deletions, diff, truncated
        case moveTo = "move_to"
        case projectRoot = "project_root"
    }
}

struct RuntimeTraceStatus: Decodable {
    let mode: String
    let root: String?
    let retentionDays: Int?
    let maxRecords: Int
    let count: Int

    private enum CodingKeys: String, CodingKey {
        case mode, root, count
        case retentionDays = "retention_days"
        case maxRecords = "max_records"
    }
}

struct RuntimeArtifactItem: Decodable, Identifiable {
    let artifactID: String
    let filename: String
    let mimeType: String
    let sizeBytes: Int64
    let createdAt: String
    let expiresAt: String
    let archive: Bool
    let url: String

    var id: String { artifactID }

    private enum CodingKeys: String, CodingKey {
        case filename, archive, url
        case artifactID = "artifact_id"
        case mimeType = "mime_type"
        case sizeBytes = "size_bytes"
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }
}

struct RuntimeArtifactsPayload: Decodable {
    let count: Int
    let configured: Bool
    let publicBaseURL: String
    let artifacts: [RuntimeArtifactItem]

    private enum CodingKeys: String, CodingKey {
        case count, configured, artifacts
        case publicBaseURL = "public_base_url"
    }
}

struct RuntimeFileChangeEntry: Decodable, Identifiable {
    let rawID: UInt64
    let traceID: String?
    let tool: String
    let source: String
    let startedAt: String
    let success: Bool
    let path: String
    let action: String
    let moveTo: String?
    let projectRoot: String?
    let insertions: Int?
    let deletions: Int?
    let diff: String?
    let truncated: Bool?

    /// 同一次 file_edit 可能影响多个文件，调用 ID 不足以区分条目。
    var id: String { "\(rawID)-\(path)" }

    private enum CodingKeys: String, CodingKey {
        case tool, source, success, path, action, insertions, deletions, diff, truncated
        case moveTo = "move_to"
        case projectRoot = "project_root"
        case rawID = "id"
        case traceID = "trace_id"
        case startedAt = "started_at"
    }
}

struct RuntimeFileChangesPayload: Decodable {
    let mode: String?
    let status: RuntimeTraceStatus?
    let count: Int
    let changes: [RuntimeFileChangeEntry]

    var effectiveMode: String { status?.mode ?? mode ?? "off" }
}

struct RuntimeTracesPayload: Decodable {
    let mode: String?
    let status: RuntimeTraceStatus?
    let count: Int
    let records: [RuntimeTraceRecord]
    let tools: [String]

    var effectiveMode: String { status?.mode ?? mode ?? "off" }
}

// MARK: - Network capture

struct RuntimeNetworkHeader: Decodable {
    let name: String
    let value: String
}

/// 请求或响应的一侧快照。body 是原始文本投影，超限时 truncated 为 true。
struct RuntimeNetworkMessage: Decodable {
    let headers: [RuntimeNetworkHeader]?
    let bytes: Int64
    let body: String?
    let truncated: Bool?
}

struct RuntimeNetworkRecord: Decodable, Identifiable {
    let id: UInt64
    /// http = 本地 HTTP 服务入站流量；nexus = 桥接 WebSocket 上的节点操作。
    let channel: String
    let startedAt: String
    let durationMS: Double
    let method: String
    let host: String
    let path: String
    let query: String?
    let rawURL: String
    let httpProtocol: String
    let remoteAddr: String
    let userAgent: String?
    let requestID: String?
    /// HTTP 状态码；nexus 通道为 0，结果看 outcome。
    let status: Int
    /// 两通道统一的结果键：ok / error。
    let outcome: String
    let errorCode: String?
    let request: RuntimeNetworkMessage
    let response: RuntimeNetworkMessage

    var isNexus: Bool { channel == "nexus" }

    private enum CodingKeys: String, CodingKey {
        case id, channel, method, host, path, query, status, outcome, request, response
        case startedAt = "started_at"
        case durationMS = "duration_ms"
        case rawURL = "raw_url"
        case httpProtocol = "proto"
        case remoteAddr = "remote_addr"
        case userAgent = "user_agent"
        case requestID = "request_id"
        case errorCode = "error_code"
    }
}

struct RuntimeNetworkStatus: Decodable {
    let enabled: Bool
    let maxRecords: Int
    let count: Int

    private enum CodingKeys: String, CodingKey {
        case enabled, count
        case maxRecords = "max_records"
    }
}

struct RuntimeNetworkPayload: Decodable {
    let enabled: Bool
    let status: RuntimeNetworkStatus?
    let count: Int
    let records: [RuntimeNetworkRecord]
}

struct RuntimeNetworkDetailPayload: Decodable {
    let record: RuntimeNetworkRecord
}

struct RuntimeNetworkClearPayload: Decodable {
    let enabled: Bool
    let cleared: Int
}

/// 任务与 trace 的时间戳可能带 6~9 位小数，ISO8601DateFormatter 只稳定支持 3 位，
/// 因此统一先截断再解析；解析失败时原样展示，不编造时间。
enum RuntimeTimestamp {
    private static let withFraction: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let plain: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    private static let display: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd HH:mm"
        return formatter
    }()

    private static let exact: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss Z"
        return formatter
    }()

    static func parse(_ raw: String?) -> Date? {
        guard let raw, !raw.isEmpty else { return nil }
        let normalized = raw.replacingOccurrences(
            of: #"\.(\d{3})\d+"#,
            with: ".$1",
            options: .regularExpression
        )
        return withFraction.date(from: normalized) ?? plain.date(from: normalized)
    }

    static func minute(_ raw: String?) -> String {
        guard let date = parse(raw) else { return raw ?? "—" }
        return display.string(from: date)
    }

    static func precise(_ raw: String?) -> String {
        guard let date = parse(raw) else { return raw ?? "" }
        return exact.string(from: date)
    }
}

struct RuntimeExtensionOverview: Equatable {
    let available: Bool
    let skillCount: Int
    let pluginCount: Int
    let pluginsAvailable: Bool
    let mcpCount: Int

    static let unavailable = RuntimeExtensionOverview(
        available: false,
        skillCount: 0,
        pluginCount: 0,
        pluginsAvailable: false,
        mcpCount: 0
    )
}

private struct RuntimeOverviewCountPayload: Decodable {
    let count: Int
}

private struct RuntimeOverviewPluginsPayload: Decodable {
    let count: Int
    let available: Bool
}

private struct RuntimeOverviewPayload: Decodable {
    let skills: RuntimeOverviewCountPayload
    let plugins: RuntimeOverviewPluginsPayload
    let mcp: RuntimeOverviewCountPayload
}

struct DesktopUpdateRegistrationState: Sendable {
    let core: String
    let tunnel: String
}

private struct TunnelStatusPayload: Decodable {
    let mode: String
    let running: Bool
    let ready: Bool
    let publicURL: String?

    private enum CodingKeys: String, CodingKey {
        case mode, running, ready
        case publicURL = "public_url"
    }
}

struct CloudflaredComponentStatus: Decodable, Equatable, Sendable {
    let state: String
    let installed: Bool
    let ready: Bool
    let version: String?
    let detail: String?

    static let unavailable = CloudflaredComponentStatus(
        state: "broken",
        installed: false,
        ready: false,
        version: nil,
        detail: nil
    )
}

struct CloudflaredComponentProgress: Decodable, Equatable, Sendable {
    let type: String
    let stage: String?
    let bytes: Int64?
    let total: Int64?
}

private struct CloudflaredComponentProcessResult {
    let exitStatus: Int32
    let status: CloudflaredComponentStatus?
}


enum NexusConnectionState: Equatable {
    case unconfigured
    case connected
    case disconnected
    case configurationError

    static func resolve(device: NexusDeviceStatus, connected: Bool) -> NexusConnectionState {
        if device.error != nil {
            return .configurationError
        }
        guard device.paired else {
            return .unconfigured
        }
        return connected ? .connected : .disconnected
    }
}

struct DesktopUpdateCheck: Decodable {
    let currentVersion: String?
    let latestVersion: String?
    let updateAvailable: Bool
    let message: String

    private enum CodingKeys: String, CodingKey {
        case currentVersion = "current_version"
        case latestVersion = "latest_version"
        case updateAvailable = "update_available"
        case message
    }

    static func decode(_ output: String) throws -> DesktopUpdateCheck {
        guard let data = output.data(using: .utf8) else {
            throw ValidationError(L10n.text("Unable to read the AgentDock update check result."))
        }
        do {
            return try JSONDecoder().decode(DesktopUpdateCheck.self, from: data)
        } catch {
            throw ValidationError(L10n.text("Unable to parse the AgentDock update check result."))
        }
    }
}

struct ServiceStatus {
    let installed: Bool
    let loaded: Bool
    let healthy: Bool
    let version: String?
    let configuration: ServiceConfiguration?
    let autostartEnabled: Bool
    let requiresApproval: Bool
    let migrationRequired: Bool
    let nexusConnection: NexusConnectionState

    static let missing = ServiceStatus(
        installed: false,
        loaded: false,
        healthy: false,
        version: nil,
        configuration: nil,
        autostartEnabled: false,
        requiresApproval: false,
        migrationRequired: false,
        nexusConnection: .unconfigured
    )
}

struct RuntimeDiagnosticCall: Decodable, Identifiable {
    let id: String
    let tool: String
    let source: String
    let startedAt: String
    let durationMS: Double
    let success: Bool
    let errorCode: String?

    private enum CodingKeys: String, CodingKey {
        case id, tool, source, success
        case startedAt = "started_at"
        case durationMS = "duration_ms"
        case errorCode = "error_code"
    }
}

struct RuntimeDiagnosticsPayload: Decodable {
    let recentCalls: [RuntimeDiagnosticCall]

    private enum CodingKeys: String, CodingKey {
        case recentCalls = "recent_calls"
    }
}

struct RuntimeAnalyticsStage: Decodable, Identifiable {
    let name: String
    let startedOffsetMS: Double
    let durationMS: Double
    let success: Bool

    var id: String { "\(name)-\(startedOffsetMS)-\(durationMS)" }

    private enum CodingKeys: String, CodingKey {
        case name, success
        case startedOffsetMS = "started_offset_ms"
        case durationMS = "duration_ms"
    }
}

struct RuntimeAnalyticsCall: Decodable, Identifiable {
    let id: UInt64
    let tool: String
    let source: String
    let userAgent: String?
    let startedAt: String
    let durationMS: Double
    let success: Bool
    let errorCode: String?
    let errorCategory: String?
    let stages: [RuntimeAnalyticsStage]

    private enum CodingKeys: String, CodingKey {
        case id, tool, source, success, stages
        case userAgent = "user_agent"
        case startedAt = "started_at"
        case durationMS = "duration_ms"
        case errorCode = "error_code"
        case errorCategory = "error_category"
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UInt64.self, forKey: .id)
        tool = try container.decode(String.self, forKey: .tool)
        source = try container.decode(String.self, forKey: .source)
        userAgent = try container.decodeIfPresent(String.self, forKey: .userAgent)
        startedAt = try container.decode(String.self, forKey: .startedAt)
        durationMS = try container.decode(Double.self, forKey: .durationMS)
        success = try container.decode(Bool.self, forKey: .success)
        errorCode = try container.decodeIfPresent(String.self, forKey: .errorCode)
        errorCategory = try container.decodeIfPresent(String.self, forKey: .errorCategory)
        stages = try container.decodeIfPresent([RuntimeAnalyticsStage].self, forKey: .stages) ?? []
    }
}

struct RuntimeToolStats: Decodable, Identifiable {
    let tool: String
    let count: Int
    let errorCount: Int
    let errorRate: Double
    let p50DurationMS: Double
    let p95DurationMS: Double
    let p99DurationMS: Double
    /// insertions/deletions 是该工具在窗口内造成的代码行数变动。
    let insertions: Int
    let deletions: Int

    var id: String { tool }

    private enum CodingKeys: String, CodingKey {
        case tool, count
        case errorCount = "error_count"
        case errorRate = "error_rate"
        case p50DurationMS = "p50_duration_ms"
        case p95DurationMS = "p95_duration_ms"
        case p99DurationMS = "p99_duration_ms"
        case insertions, deletions
    }
}

/// RuntimeAlert 是后端阈值告警的一条活跃告警。
/// 告警种类是固定集合（error_rate / trace_disk / task_blocked），
/// 展示层把种类映射成本地化标题，message/detail 保留后端原文。
struct RuntimeAlert: Decodable, Identifiable {
    let id: String
    let severity: String
    let kind: String
    let message: String
    let detail: String
    let firstSeen: String
    let lastSeen: String
    let active: Bool

    private enum CodingKeys: String, CodingKey {
        case id, severity, kind, message, detail, active
        case firstSeen = "first_seen"
        case lastSeen = "last_seen"
    }
}

struct RuntimeAlertsPayload: Decodable {
    let alerts: [RuntimeAlert]
    let resolvedCount: Int

    private enum CodingKeys: String, CodingKey {
        case alerts
        case resolvedCount = "resolved_count"
    }
}

struct RuntimeProcessSnapshot: Decodable {
    let goroutines: Int
    let heapAllocBytes: UInt64
    let heapInuseBytes: UInt64
    let heapSysBytes: UInt64
    let gcCycles: UInt32
    let uptimeMS: Int64

    private enum CodingKeys: String, CodingKey {
        case goroutines
        case heapAllocBytes = "heap_alloc_bytes"
        case heapInuseBytes = "heap_inuse_bytes"
        case heapSysBytes = "heap_sys_bytes"
        case gcCycles = "gc_cycles"
        case uptimeMS = "uptime_ms"
    }
}

struct RuntimeAnalyticsPayload: Decodable {
    let startedAt: String
    let recentCapacity: Int
    let windowCalls: Int
    let totalCalls: UInt64
    let totalErrors: UInt64
    let activeCalls: Int
    /// totalInsertions/totalDeletions 是进程启动以来的代码行数变动累计值。
    let totalInsertions: UInt64
    let totalDeletions: UInt64
    let toolStats: [RuntimeToolStats]
    let recentCalls: [RuntimeAnalyticsCall]
    let process: RuntimeProcessSnapshot

    private enum CodingKeys: String, CodingKey {
        case process
        case startedAt = "started_at"
        case recentCapacity = "recent_capacity"
        case windowCalls = "window_calls"
        case totalCalls = "total_calls"
        case totalErrors = "total_errors"
        case activeCalls = "active_calls"
        case totalInsertions = "total_insertions"
        case totalDeletions = "total_deletions"
        case toolStats = "tool_stats"
        case recentCalls = "recent_calls"
    }
}

struct RuntimeDashboardSnapshot {
    let countsAvailable: Bool
    let diagnosticsAvailable: Bool
    let skillCount: Int
    let mcpCount: Int
    let pluginCount: Int
    let recentCalls: [RuntimeDiagnosticCall]

    static let empty = RuntimeDashboardSnapshot(
        countsAvailable: false,
        diagnosticsAvailable: false,
        skillCount: 0,
        mcpCount: 0,
        pluginCount: 0,
        recentCalls: []
    )
}

final class ServiceController: @unchecked Sendable {
    static let coreLabel = "com.uvwt.agentdock.core"
    static let tunnelLabel = "com.uvwt.agentdock.tunnel"
    static let corePlistName = "com.uvwt.agentdock.core.plist"
    static let tunnelPlistName = "com.uvwt.agentdock.tunnel.plist"
    private static let quickTunnelReadyTimeout: TimeInterval = 85

    let paths: AppPaths
    let lifecycleCoordinator = BackgroundServiceLifecycleCoordinator()

    final class LifecycleTransaction: @unchecked Sendable {
        private let service: ServiceController

        fileprivate init(service: ServiceController) {
            self.service = service
        }

        func start() async throws -> BackgroundServiceLifecycleResult {
            try await service.startWithinLifecycle(forceCoreRestart: false)
        }

        func stop() throws {
            try service.stopWithinLifecycle()
        }

        func restart() async throws -> BackgroundServiceLifecycleResult {
            try await service.startWithinLifecycle(forceCoreRestart: true)
        }

        func setTunnelEnabled(_ enabled: Bool) throws {
            try service.setTunnelEnabledWithinLifecycle(enabled)
        }
    }

    init(paths: AppPaths = AppPaths()) {
        self.paths = paths
    }

    func withLifecycleTransaction<T: Sendable>(
        _ operation: @escaping @Sendable (LifecycleTransaction) async throws -> T
    ) async throws -> T {
        try await lifecycleCoordinator.run {
            try await operation(LifecycleTransaction(service: self))
        }
    }

    func status() async -> ServiceStatus {
        let fileManager = FileManager.default
        let migrationRequired = LegacyDesktopRuntimeMigration.isPresent(paths: paths)
        let installed = fileManager.isExecutableFile(atPath: paths.binary.path)
            && fileManager.fileExists(atPath: paths.coreSkillBundle.appendingPathComponent("manifest.json").path)
            && fileManager.fileExists(atPath: paths.environment.path)
        guard installed else { return .missing }

        let configuration = ServiceConfiguration.load(from: paths.environment)
        let nexusDevice = nexusDeviceStatus()
        let registration = coreService.status
        let requiresApproval = registration == .requiresApproval
        let enabled = registration == .enabled
        let registered = enabled || requiresApproval
        let loaded = enabled && isLoaded(label: Self.coreLabel)
        let nexusConnected = loaded && nexusDevice.paired ? await fetchNexusConnected() : false

        guard loaded, let healthURL = configuration?.healthURL else {
            return ServiceStatus(
                installed: true,
                loaded: loaded,
                healthy: false,
                version: nil,
                configuration: configuration,
                autostartEnabled: registered,
                requiresApproval: requiresApproval,
                migrationRequired: migrationRequired,
                nexusConnection: .resolve(device: nexusDevice, connected: nexusConnected)
            )
        }

        let health = await fetchHealth(url: healthURL)
        return ServiceStatus(
            installed: true,
            loaded: true,
            healthy: health?.ok == true,
            version: health?.version,
            configuration: configuration,
            autostartEnabled: registered,
            requiresApproval: requiresApproval,
            migrationRequired: migrationRequired,
            nexusConnection: .resolve(device: nexusDevice, connected: nexusConnected)
        )
    }

    func dashboard(configuration: ServiceConfiguration?) async -> RuntimeDashboardSnapshot {
        guard let configuration else { return .empty }

        async let overviewTask: RuntimeOverviewPayload? = fetchRuntimePayload(
            RuntimeOverviewPayload.self,
            configuration: configuration,
            path: "/internal/runtime/overview"
        )
        async let diagnosticsTask: RuntimeDiagnosticsPayload? = fetchRuntimePayload(
            RuntimeDiagnosticsPayload.self,
            configuration: configuration,
            path: "/internal/runtime/diagnostics"
        )
        let overviewPayload = await overviewTask

        let diagnosticsPayload = await diagnosticsTask
        return RuntimeDashboardSnapshot(
            countsAvailable: overviewPayload != nil,
            diagnosticsAvailable: diagnosticsPayload != nil,
            skillCount: overviewPayload?.skills.count ?? 0,
            mcpCount: overviewPayload?.mcp.count ?? 0,
            pluginCount: overviewPayload?.plugins.count ?? 0,
            recentCalls: diagnosticsPayload?.recentCalls ?? []
        )
    }

    func runtimeAnalytics(configuration: ServiceConfiguration?) async -> RuntimeAnalyticsPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeAnalyticsPayload.self,
            configuration: configuration,
            path: "/internal/runtime/analytics"
        )
    }

    /// runtimeAlerts 拉取当前活跃的阈值告警。告警由后端惰性评估并去重，
    /// 客户端只读不写：关掉告警不会影响采集。
    func runtimeAlerts(configuration: ServiceConfiguration?) async -> RuntimeAlertsPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeAlertsPayload.self,
            configuration: configuration,
            path: "/internal/runtime/alerts",
            timeout: 5
        )
    }

    func runtimeCatalog(configuration: ServiceConfiguration?) async -> RuntimeCatalogPayload? {
        guard let configuration else { return nil }
        // 全量工具 Schema 比状态查询重得多，给独立更宽的超时。
        return await fetchRuntimePayload(
            RuntimeCatalogPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.catalog,
            timeout: 6
        )
    }

    func runtimeSkills(configuration: ServiceConfiguration?) async -> RuntimeSkillsPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeSkillsPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.skills,
            timeout: 5
        )
    }

    func runtimePlugins(configuration: ServiceConfiguration?) async -> RuntimePluginsPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimePluginsPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.plugins,
            timeout: 5
        )
    }

    func runtimePlugin(name: String, configuration: ServiceConfiguration?) async -> RuntimePluginDetail? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimePluginDetailPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.plugins + "/" + name,
            timeout: 5
        )?.plugin
    }

    func runtimeTasks(configuration: ServiceConfiguration?) async -> RuntimeTasksPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeTasksPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.tasks,
            queryItems: [URLQueryItem(name: "limit", value: "200")],
            timeout: 5
        )
    }

    func runtimeTask(id: String, configuration: ServiceConfiguration?) async -> RuntimeTaskDetail? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeTaskDetailPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.tasks + "/" + id,
            timeout: 5
        )?.task
    }

    func runtimeArtifacts(configuration: ServiceConfiguration?) async -> RuntimeArtifactsPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeArtifactsPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.artifacts,
            timeout: 5
        )
    }

    func runtimeFileChanges(query: String = "", limit: Int = 500, configuration: ServiceConfiguration?) async -> RuntimeFileChangesPayload? {
        guard let configuration else { return nil }
        var items: [URLQueryItem] = [URLQueryItem(name: "limit", value: String(limit))]
        if !query.isEmpty { items.append(URLQueryItem(name: "q", value: query)) }
        return await fetchRuntimePayload(
            RuntimeFileChangesPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.fileChanges,
            queryItems: items,
            timeout: 5
        )
    }

    func runtimeTraces(
        query: String = "",
        tool: String = "",
        source: String = "",
        status: String = "",
        view: String = "full",
        limit: Int = 0,
        configuration: ServiceConfiguration?
    ) async -> RuntimeTracesPayload? {
        guard let configuration else { return nil }
        var items: [URLQueryItem] = []
        if !query.isEmpty { items.append(URLQueryItem(name: "q", value: query)) }
        if !tool.isEmpty { items.append(URLQueryItem(name: "tool", value: tool)) }
        if !source.isEmpty { items.append(URLQueryItem(name: "source", value: source)) }
        if !status.isEmpty { items.append(URLQueryItem(name: "status", value: status)) }
        // view=summary 让列表只回摘要；正文由 runtimeTraceDetail 按需拉取。
        if !view.isEmpty { items.append(URLQueryItem(name: "view", value: view)) }
        if limit > 0 { items.append(URLQueryItem(name: "limit", value: String(limit))) }
        return await fetchRuntimePayload(
            RuntimeTracesPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.traces,
            queryItems: items,
            timeout: 5
        )
    }

    func runtimeTraceDetail(id: UInt64, full: Bool = false, configuration: ServiceConfiguration?) async -> RuntimeTraceDetailPayload? {
        guard let configuration else { return nil }
        var items = [URLQueryItem(name: "id", value: String(id))]
        // full=1 时后端附带旁路全文副本；预览足够时不必拉全文。
        if full { items.append(URLQueryItem(name: "full", value: "1")) }
        return await fetchRuntimePayload(
            RuntimeTraceDetailPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.traceDetail,
            queryItems: items,
            timeout: 5
        )
    }

    /// 列表默认 summary：每条记录单侧正文可达 32 KiB，首屏只拉摘要，
    /// 正文由 runtimeNetworkDetail 按需取。
    func runtimeNetwork(
        query: String = "",
        channel: String = "",
        status: String = "",
        limit: Int = 0,
        configuration: ServiceConfiguration?
    ) async -> RuntimeNetworkPayload? {
        guard let configuration else { return nil }
        var items = [URLQueryItem(name: "view", value: "summary")]
        if !query.isEmpty { items.append(URLQueryItem(name: "q", value: query)) }
        if !channel.isEmpty { items.append(URLQueryItem(name: "channel", value: channel)) }
        if !status.isEmpty { items.append(URLQueryItem(name: "status", value: status)) }
        if limit > 0 { items.append(URLQueryItem(name: "limit", value: String(limit))) }
        return await fetchRuntimePayload(
            RuntimeNetworkPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.networkCapture,
            queryItems: items,
            timeout: 5
        )
    }

    func runtimeNetworkDetail(id: UInt64, configuration: ServiceConfiguration?) async -> RuntimeNetworkDetailPayload? {
        guard let configuration else { return nil }
        return await fetchRuntimePayload(
            RuntimeNetworkDetailPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.networkCaptureDetail,
            queryItems: [URLQueryItem(name: "id", value: String(id))],
            timeout: 5
        )
    }

    func runtimeNetworkClear(configuration: ServiceConfiguration?) async -> Bool {
        guard let configuration else { return false }
        guard let payload = await fetchRuntimePayload(
            RuntimeNetworkClearPayload.self,
            configuration: configuration,
            path: RuntimeAPIPath.networkCapture,
            method: "DELETE",
            timeout: 5
        ) else { return false }
        return payload.enabled
    }

    func start() async throws -> BackgroundServiceLifecycleResult {
        try await withLifecycleTransaction { lifecycle in
            try await lifecycle.start()
        }
    }

    func stop() async throws {
        try await withLifecycleTransaction { lifecycle in
            try lifecycle.stop()
        }
    }

    func unregisterManagedBackgroundServicesForUninstall() throws {
        // 仅供 main.swift 的独立 --unregister-background-services helper 进程调用。
        // 该进程不会启动 AppDelegate，也不存在并发生命周期 mutation；正常 App 路径
        // 必须通过 lifecycleCoordinator，不能复用这个同步逃生口。
        var failures: [String] = []
        do {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        } catch {
            failures.append("AgentDock Tunnel: \(error.localizedDescription)")
        }
        do {
            try unregister(service: coreService, label: Self.coreLabel)
        } catch {
            failures.append("AgentDock Core: \(error.localizedDescription)")
        }
        if !failures.isEmpty {
            throw ValidationError(failures.joined(separator: "\n"))
        }
    }

    func restart() async throws -> BackgroundServiceLifecycleResult {
        try await withLifecycleTransaction { lifecycle in
            try await lifecycle.restart()
        }
    }

    func nexusDeviceStatus() -> NexusDeviceStatus {
        NexusDeviceStatus.load(from: paths.nexusDeviceIdentity)
    }

    func runtimeExtensionOverview(configuration: ServiceConfiguration?) async -> RuntimeExtensionOverview {
        guard let configuration,
              let healthURL = configuration.healthURL,
              var components = URLComponents(url: healthURL, resolvingAgainstBaseURL: false) else {
            return .unavailable
        }
        components.path = "/internal/runtime/overview"
        components.query = nil
        components.fragment = nil
        guard let url = components.url else { return .unavailable }

        do {
            var request = URLRequest(url: url)
            request.timeoutInterval = 2.5
            if !configuration.authToken.isEmpty {
                request.setValue("Bearer \(configuration.authToken)", forHTTPHeaderField: "Authorization")
            }
            let (data, response) = try await URLSession.shared.data(for: request)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { return .unavailable }
            let payload = try JSONDecoder().decode(RuntimeOverviewPayload.self, from: data)
            return RuntimeExtensionOverview(
                available: true,
                skillCount: max(0, payload.skills.count),
                pluginCount: max(0, payload.plugins.count),
                pluginsAvailable: payload.plugins.available,
                mcpCount: max(0, payload.mcp.count)
            )
        } catch {
            return .unavailable
        }
    }

    func pairNexus(endpoint: String, pairingCode: String) async throws {
        let endpoint = endpoint.trimmingCharacters(in: .whitespacesAndNewlines)
        let pairingCode = pairingCode.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !endpoint.isEmpty, !pairingCode.isEmpty else {
            throw ValidationError(L10n.text("NexusDock address and one-time pairing code cannot be empty."))
        }
        try await withLifecycleTransaction { lifecycle in
            let result = try await self.runInBackground {
                try runProcess(
                    executable: self.paths.binary.path,
                    arguments: ["nexus", "pair", "--endpoint", endpoint, "--code", pairingCode]
                )
            }
            guard result.status == 0 else {
                throw ValidationError(self.commandError(result.output, action: L10n.text("NexusDock pairing")))
            }
            _ = try await lifecycle.restart()
        }
    }

    func setAutostart(enabled: Bool) async throws {
        if enabled {
            _ = try await start()
        } else {
            try await stop()
        }
    }

    func tunnelEnabled() -> Bool {
        let status = tunnelService.status
        return status == .enabled || status == .requiresApproval
    }

    func cloudflaredComponentStatus() async -> CloudflaredComponentStatus {
        do {
            let result = try await runInBackground {
                try runProcess(
                    executable: self.paths.binary.path,
                    arguments: [
                        "component", "status", "cloudflared",
                        "--runtime-root", self.paths.appSupport.path,
                        "--json",
                    ]
                )
            }
            guard result.status == 0,
                  let data = result.output.data(using: .utf8),
                  let status = try? JSONDecoder().decode(CloudflaredComponentStatus.self, from: data) else {
                return .unavailable
            }
            return status
        } catch {
            return .unavailable
        }
    }

    func bootstrapBundledCoreSkillsForUpdate() async throws {
        let result = try await runInBackground {
            try runProcess(
                executable: self.paths.binary.path,
                arguments: ["skill", "bootstrap", "--bundle", self.paths.coreSkillBundle.path]
            )
        }
        guard result.status == 0 else {
            throw ValidationError(
                result.output.isEmpty
                    ? L10n.text("Official core Skill initialization failed.")
                    : result.output
            )
        }
    }

    func migrateLegacyCloudflaredIfNeeded(source: URL?, required: Bool) async throws {
        try await lifecycleCoordinator.run {
            try await self.migrateLegacyCloudflaredWithinLifecycle(source: source, required: required)
        }
    }

    private func migrateLegacyCloudflaredWithinLifecycle(source: URL?, required: Bool) async throws {
        let current = await cloudflaredComponentStatus()
        guard !current.ready, required else { return }
        guard let source else {
            throw ValidationError(L10n.text("Cloudflare Tunnel is configured, but its optional component is missing. Repair the component before updating AgentDock."))
        }
        let values = try source.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true else {
            throw ValidationError(L10n.text("Cloudflare Tunnel is configured, but the legacy component cannot be migrated safely."))
        }
        let result = try await runInBackground {
            try runProcess(
                executable: self.paths.binary.path,
                arguments: [
                    "component", "__import-legacy", "cloudflared",
                    "--runtime-root", self.paths.appSupport.path,
                    "--source", source.path,
                    "--json",
                ]
            )
        }
        guard result.status == 0 else {
            throw ValidationError(L10n.text("Cloudflare Tunnel component migration failed. The AgentDock update was not committed."))
        }
        let migrated = await cloudflaredComponentStatus()
        guard migrated.ready else {
            throw ValidationError(L10n.text("Cloudflare Tunnel component migration did not produce a ready component."))
        }
    }

    func installCloudflaredComponent(
        onProgress: ((CloudflaredComponentProgress) -> Void)? = nil
    ) async throws -> CloudflaredComponentStatus {
        try await lifecycleCoordinator.run {
            try await self.runCloudflaredComponentAction("install", onProgress: onProgress)
        }
    }

    func updateCloudflaredComponent(
        onProgress: ((CloudflaredComponentProgress) -> Void)? = nil
    ) async throws -> CloudflaredComponentStatus {
        try await lifecycleCoordinator.run {
            try await self.runCloudflaredComponentAction("update", onProgress: onProgress)
        }
    }

    func uninstallCloudflaredComponent() async throws -> CloudflaredComponentStatus {
        try await lifecycleCoordinator.run {
            let mode = try self.configuredTunnelMode()
            if mode != .local {
                try self.setTunnelEnabledWithinLifecycle(false)
                try await self.configureTunnelWithinLifecycle(mode: .local, serverURL: "", tunnelToken: "")
            }
            return try await self.runCloudflaredComponentAction("uninstall")
        }
    }

    func configureTunnel(mode: TunnelMode, serverURL: String, tunnelToken: String) async throws {
        try await lifecycleCoordinator.run {
            try await self.configureTunnelWithinLifecycle(
                mode: mode,
                serverURL: serverURL,
                tunnelToken: tunnelToken
            )
        }
    }

    private func configureTunnelWithinLifecycle(
        mode: TunnelMode,
        serverURL: String,
        tunnelToken: String
    ) async throws {
        if mode != .local {
            let component = await cloudflaredComponentStatus()
            guard component.ready else {
                throw ValidationError(L10n.text("Install the Cloudflare Tunnel component first."))
            }
        }

        let wasEnabled = tunnelEnabled()
        if wasEnabled {
            try setTunnelEnabledWithinLifecycle(false)
        }

        let tokenFile = try writeTemporaryTunnelToken(tunnelToken)
        defer {
            if let tokenFile { try? FileManager.default.removeItem(at: tokenFile) }
        }

        var arguments = [
            "tunnel", "configure",
            "--runtime-root", paths.appSupport.path,
            "--mode", mode.rawValue,
            "--server-url", serverURL,
        ]
        if let tokenFile {
            arguments += ["--token-file", tokenFile.path]
        }

        do {
            let result = try await runInBackground {
                try runProcess(executable: self.paths.binary.path, arguments: arguments)
            }
            guard result.status == 0 else {
                throw ValidationError(commandError(result.output, action: L10n.text("Tunnel configuration")))
            }
            if mode != .local {
                try setTunnelEnabledWithinLifecycle(true)
            }
            if mode == .quick {
                try await waitForQuickTunnelReady()
            }
        } catch {
            if wasEnabled {
                try? setTunnelEnabledWithinLifecycle(true)
            }
            throw error
        }
    }

    private func waitForQuickTunnelReady() async throws {
        let deadline = Date().addingTimeInterval(Self.quickTunnelReadyTimeout)
        var sawReadyAddress = false

        while Date() < deadline {
            try Task.checkCancellation()

            let statusResult = try await runInBackground {
                try runProcess(
                    executable: self.paths.binary.path,
                    arguments: [
                        "tunnel", "status",
                        "--runtime-root", self.paths.appSupport.path,
                    ]
                )
            }
            if statusResult.status == 0,
               let data = statusResult.output.data(using: .utf8),
               let tunnelStatus = try? JSONDecoder().decode(TunnelStatusPayload.self, from: data),
               tunnelStatus.mode == TunnelMode.quick.rawValue,
               tunnelStatus.running,
               tunnelStatus.ready,
               let publicURL = tunnelStatus.publicURL?.trimmingCharacters(in: .whitespacesAndNewlines),
               !publicURL.isEmpty {
                sawReadyAddress = true

                if let configuration = ServiceConfiguration.load(from: paths.environment),
                   configuration.publicURL == publicURL,
                   await coreReadyForCurrentApp(configuration: configuration, timeout: 5) {
                    return
                }
            }

            try await Task.sleep(nanoseconds: 250_000_000)
        }

        if sawReadyAddress {
            throw ValidationError(L10n.text(
                "A temporary public address was generated, but AgentDock Core did not recover to a healthy state."
            ))
        }
        throw ValidationError(L10n.text(
            "cloudflared did not generate a temporary public address before the timeout."
        ))
    }

    func configuredNamedTunnelOrigin() -> String {
        let path = paths.appSupport.appendingPathComponent("named-server-url.txt")
        guard let data = try? Data(contentsOf: path),
              let value = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) else {
            return ""
        }
        return value
    }

    private func runCloudflaredComponentAction(
        _ action: String,
        onProgress: ((CloudflaredComponentProgress) -> Void)? = nil
    ) async throws -> CloudflaredComponentStatus {
        let result = try await runInBackground {
            try self.runCloudflaredComponentProcess(action: action, onProgress: onProgress)
        }
        guard result.exitStatus == 0 else {
            throw ValidationError(L10n.text("Cloudflare Tunnel component operation failed. Check diagnostics and try again."))
        }
        guard let status = result.status else {
            throw ValidationError(L10n.text("Unable to read Cloudflare Tunnel component status."))
        }
        return status
    }

    private func runCloudflaredComponentProcess(
        action: String,
        onProgress: ((CloudflaredComponentProgress) -> Void)?
    ) throws -> CloudflaredComponentProcessResult {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: paths.binary.path)
        process.arguments = [
            "component", action, "cloudflared",
            "--runtime-root", paths.appSupport.path,
            action == "install" || action == "update" ? "--progress-json" : "--json",
        ]

        // component 的 stdout 是逐行 JSON 协议。安装/更新实时消费进度事件，
        // 最后一行仍是组件状态；stderr 合流后仅忽略非协议诊断行，失败统一由退出码处理。
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        try process.run()

        let handle = pipe.fileHandleForReading
        var buffered = Data()
        var finalStatus: CloudflaredComponentStatus?

        func consumeLine(_ data: Data) {
            guard !data.isEmpty else { return }
            if let progress = try? JSONDecoder().decode(CloudflaredComponentProgress.self, from: data) {
                onProgress?(progress)
                return
            }
            if let status = try? JSONDecoder().decode(CloudflaredComponentStatus.self, from: data) {
                finalStatus = status
            }
        }

        while true {
            let chunk = handle.availableData
            if chunk.isEmpty { break }
            buffered.append(chunk)
            while let newline = buffered.firstIndex(of: 0x0A) {
                consumeLine(Data(buffered[..<newline]))
                buffered.removeSubrange(buffered.startIndex...newline)
            }
        }
        consumeLine(buffered)
        process.waitUntilExit()
        return CloudflaredComponentProcessResult(
            exitStatus: process.terminationStatus,
            status: finalStatus
        )
    }

    private func writeTemporaryTunnelToken(_ token: String) throws -> URL? {
        let token = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !token.isEmpty else { return nil }
        guard !token.contains("\n"), !token.contains("\r") else {
            throw ValidationError(L10n.text("Tunnel Token must be a single line of text."))
        }
        try FileManager.default.createDirectory(at: paths.appSupport, withIntermediateDirectories: true)
        let url = paths.appSupport.appendingPathComponent(".tunnel-token.\(UUID().uuidString)")
        try Data((token + "\n").utf8).write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        return url
    }

    func configuredTunnelMode() throws -> TunnelMode {
        guard FileManager.default.fileExists(atPath: paths.tunnelEnvironment.path) else {
            return .local
        }
        let environment = try ManagedEnvironment.load(from: paths.tunnelEnvironment)
        let rawMode = environment.values["AGENTDOCK_TUNNEL_MODE"]?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased() ?? ""
        return TunnelMode(rawValue: rawMode) ?? .local
    }

    func setTunnelEnabledWithinLifecycle(_ enabled: Bool) throws {
        if enabled {
            try register(
                service: tunnelService,
                plistName: Self.tunnelPlistName,
                displayName: "AgentDock Tunnel"
            )
        } else {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        }
    }

    private func restartTunnelWithinLifecycle() throws {
        try reregister(service: tunnelService, label: Self.tunnelLabel, displayName: "AgentDock Tunnel")
    }

    private func restoreBackgroundServiceRegistrationsWithinLifecycle(
        coreEnabled: Bool,
        tunnelEnabled: Bool
    ) throws {
        if coreEnabled {
            try restoreRegistration(service: coreService, label: Self.coreLabel, displayName: "AgentDock Core")
        } else {
            try unregister(service: coreService, label: Self.coreLabel)
        }
        if tunnelEnabled {
            try restoreRegistration(service: tunnelService, label: Self.tunnelLabel, displayName: "AgentDock Tunnel")
        } else {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        }
    }

    func restoreBackgroundServiceRegistrationsForUpdate(
        coreEnabled: Bool,
        tunnelEnabled: Bool
    ) async throws -> DesktopUpdateRegistrationState {
        try await lifecycleCoordinator.run {
            try self.restoreBackgroundServiceRegistrationsForUpdateWithinLifecycle(
                coreEnabled: coreEnabled,
                tunnelEnabled: tunnelEnabled
            )
        }
    }

    private func restoreBackgroundServiceRegistrationsForUpdateWithinLifecycle(
        coreEnabled: Bool,
        tunnelEnabled: Bool
    ) throws -> DesktopUpdateRegistrationState {
        let coreState = try restoreRegistrationForUpdate(
            service: coreService,
            label: Self.coreLabel,
            displayName: "AgentDock Core",
            expectedEnabled: coreEnabled
        )
        let tunnelState: String
        do {
            tunnelState = try restoreRegistrationForUpdate(
                service: tunnelService,
                label: Self.tunnelLabel,
                displayName: "AgentDock Tunnel",
                expectedEnabled: tunnelEnabled
            )
        } catch {
            // Tunnel availability depends on ServiceManagement policy plus external/network state.
            // Record the handoff state for diagnostics, but do not turn it into an update gate or
            // completion warning; the control panel owns eventual Tunnel/public readiness.
            NSLog("AgentDock Tunnel registration could not be restored during update handoff: %@", error.localizedDescription)
            tunnelState = "unavailable"
        }
        return DesktopUpdateRegistrationState(core: coreState, tunnel: tunnelState)
    }

    func recoverBackgroundServicesAfterUpdate(coreEnabled: Bool, tunnelEnabled: Bool) async -> [String] {
        do {
            return try await lifecycleCoordinator.run {
                await self.recoverBackgroundServicesAfterUpdateWithinLifecycle(
                    coreEnabled: coreEnabled,
                    tunnelEnabled: tunnelEnabled
                )
            }
        } catch {
            return [error.localizedDescription]
        }
    }

    private func recoverBackgroundServicesAfterUpdateWithinLifecycle(
        coreEnabled: Bool,
        tunnelEnabled: Bool
    ) async -> [String] {
        // App Bundle 替换后，SMAppService 可能已经返回 enabled，但 launchd 尚未真正启动 Core。
        // 先给系统一个正常传播窗口；仍不健康时只做一次完整 unregister/register 自愈。
        // 最终更新是否提交仍由外部 Arbiter 的 Core health/version gate 决定。
        var warnings: [String] = []
        if tunnelEnabled,
           tunnelService.status == .enabled,
           !(await waitForTunnelProcess()) {
            NSLog("AgentDock Tunnel 注册显示 enabled 但进程未稳定，开始自动重新注册。")
            do {
                try restartTunnelWithinLifecycle()
                if !(await waitForTunnelProcess()) {
                    NSLog("AgentDock Tunnel 重新注册后进程仍未稳定。")
                }
            } catch {
                // Tunnel/public readiness is intentionally outside the update commit boundary.
                NSLog("AgentDock Tunnel 自动重新注册失败：%@", error.localizedDescription)
            }
        }
        if coreEnabled,
           coreService.status == .enabled,
           !(await coreReadyForCurrentApp(timeout: 10)) {
            NSLog("AgentDock Core 注册显示 enabled 但健康检查未通过，开始自动重新注册。")
            do {
                _ = try await startWithinLifecycle(forceCoreRestart: true)
            } catch {
                warnings.append(error.localizedDescription)
            }
        }
        return warnings
    }

    func reregisterBackgroundServices(coreEnabled: Bool, tunnelEnabled: Bool) async throws -> [String] {
        try await lifecycleCoordinator.run {
            try self.restoreBackgroundServiceRegistrationsWithinLifecycle(
                coreEnabled: coreEnabled,
                tunnelEnabled: tunnelEnabled
            )
            return await self.recoverBackgroundServicesAfterUpdateWithinLifecycle(
                coreEnabled: coreEnabled,
                tunnelEnabled: tunnelEnabled
            )
        }
    }

    func openBackgroundItemsSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }

    func waitForTunnelProcess(timeout: TimeInterval = 10) async -> Bool {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                continuation.resume(returning: self.waitForStableLaunchdProcess(
                    label: Self.tunnelLabel,
                    timeout: timeout
                ))
            }
        }
    }

    func isLoaded() -> Bool {
        isLoaded(label: Self.coreLabel)
    }

    func waitForHealth(configuration: ServiceConfiguration, timeout: TimeInterval = 30) async -> Bool {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                continuation.resume(returning: self.waitForHealthSynchronously(configuration: configuration, timeout: timeout))
            }
        }
    }

    func checkForUpdates() async throws -> DesktopUpdateCheck {
        try await runInBackground {
            let result = try runProcess(
                executable: self.paths.binary.path,
                arguments: ["update", "--check"],
                environment: ["AGENTDOCK_DESKTOP_APP_PATH": self.paths.appBundle.path]
            )
            guard result.status == 0 else {
                throw ValidationError(self.commandError(result.output, action: L10n.text("Check for updates")))
            }
            return try DesktopUpdateCheck.decode(result.output)
        }
    }

    func applyUpdate(
        onProgress: @escaping @Sendable (UpdateProgressEvent) -> Void
    ) async throws -> String {
        try await lifecycleCoordinator.run {
            try await self.applyUpdateWithinLifecycle(onProgress: onProgress)
        }
    }

    private func applyUpdateWithinLifecycle(
        onProgress: @escaping @Sendable (UpdateProgressEvent) -> Void
    ) async throws -> String {
        // 用户确认之后才检查后台服务写入能力并进入停服/替换阶段。
        // 纯版本检查不应该产生任何服务状态或更新事务副作用。
        try validateServiceManagementReadiness()

        let currentStatus = await status()
        let serviceState = DesktopUpdateServiceState(
            coreEnabled: currentStatus.autostartEnabled,
            tunnelEnabled: tunnelEnabled()
        )
        let configuredMode = (try? configuredTunnelMode()) ?? .local
        try await migrateLegacyCloudflaredWithinLifecycle(
            source: paths.legacyBundledCloudflared,
            required: configuredMode != .local || serviceState.tunnelEnabled
        )
        try serviceState.write(to: paths.updateServiceState)

        let output: String
        do {
            try setTunnelEnabledWithinLifecycle(false)
            try stopWithinLifecycle()
            output = try await runInBackground {
                let result = try runUpdateProcess(
                    executable: self.paths.binary.path,
                    arguments: ["update", "--progress-json"],
                    environment: ["AGENTDOCK_DESKTOP_APP_PATH": self.paths.appBundle.path],
                    outputURL: self.paths.updateLog,
                    onProgress: onProgress
                )
                guard result.status == 0 else {
                    throw ValidationError(self.commandError(result.output, action: L10n.text("Update")))
                }
                return result.output.trimmingCharacters(in: .whitespacesAndNewlines)
            }
        } catch {
            let updateError = error
            do {
                try restoreBackgroundServiceRegistrationsWithinLifecycle(
                    coreEnabled: serviceState.coreEnabled,
                    tunnelEnabled: serviceState.tunnelEnabled
                )
                let recoveryWarnings = await recoverBackgroundServicesAfterUpdateWithinLifecycle(
                    coreEnabled: serviceState.coreEnabled,
                    tunnelEnabled: serviceState.tunnelEnabled
                )
                if !recoveryWarnings.isEmpty {
                    NSLog("AgentDock 更新失败后后台服务仍在启动：%@", recoveryWarnings.joined(separator: "；"))
                }
                DesktopUpdateServiceState.remove(at: paths.updateServiceState)
            } catch {
                throw ValidationError(L10n.format(
                    "The update was not applied, and restoring background services also failed: %@; %@",
                    updateError.localizedDescription,
                    error.localizedDescription
                ))
            }
            throw updateError
        }

        // 真正的 App 替换会终止旧 GUI，并由新版 App 根据 update-result.json 恢复服务。
        // 能执行到这里说明更新进程正常返回但没有完成 GUI handoff，因此旧 GUI 必须自己收尾。
        do {
            try restoreBackgroundServiceRegistrationsWithinLifecycle(
                coreEnabled: serviceState.coreEnabled,
                tunnelEnabled: serviceState.tunnelEnabled
            )
            let recoveryWarnings = await recoverBackgroundServicesAfterUpdateWithinLifecycle(
                coreEnabled: serviceState.coreEnabled,
                tunnelEnabled: serviceState.tunnelEnabled
            )
            if !recoveryWarnings.isEmpty {
                NSLog("AgentDock 更新返回后后台服务仍在启动：%@", recoveryWarnings.joined(separator: "；"))
            }
            DesktopUpdateServiceState.remove(at: paths.updateServiceState)
        } catch {
            throw ValidationError(L10n.format(
                "The update process returned, but restoring background services failed: %@",
                error.localizedDescription
            ))
        }
        return output
    }

    func openLogs() {
        try? FileManager.default.createDirectory(at: paths.logs, withIntermediateDirectories: true)
        NSWorkspace.shared.open(paths.logs)
    }

    func openConfiguration() {
        try? FileManager.default.createDirectory(at: paths.appSupport, withIntermediateDirectories: true)
        NSWorkspace.shared.open(paths.appSupport)
    }

    private func isLoopbackHost(_ host: String?) -> Bool {
        guard let normalized = host?.lowercased() else { return false }
        return normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1"
    }

    // 下面这些 module-internal 原语只供 BackgroundServiceLifecycle 的状态机编排；
    // UI、Installer 和更新流程不得绕过 lifecycleCoordinator 直接调用。
    var coreService: SMAppService {
        SMAppService.agent(plistName: Self.corePlistName)
    }

    var tunnelService: SMAppService {
        SMAppService.agent(plistName: Self.tunnelPlistName)
    }

    func registerCoreIfNeeded() throws {
        try register(
            service: coreService,
            plistName: Self.corePlistName,
            displayName: "AgentDock Core"
        )
    }

    private func register(service: SMAppService, plistName: String, displayName: String) throws {
        try validateServiceManagementReadiness()
        try validateBundledServiceDefinition(plistName: plistName, displayName: displayName)
        switch service.status {
        case .enabled:
            return
        case .requiresApproval:
            throw ValidationError(L10n.format(
                "%@ is registered, but you need to allow it to run in the background in System Settings → General → Login Items & Extensions.",
                displayName
            ))
        case .notRegistered, .notFound:
            // SMAppService 在服务首次 register 前可能返回 .notFound，即使 Bundle 内 plist
            // 已经存在。定义是否完整由上面的 Bundle 文件校验负责，不用 status 猜测。
            try service.register()
        @unknown default:
            throw ValidationError(L10n.format("Unable to determine background service status for %@.", displayName))
        }
        if service.status == .requiresApproval {
            throw ValidationError(L10n.format(
                "%@ requires permission to run in the background in System Settings → General → Login Items & Extensions.",
                displayName
            ))
        }
        guard service.status == .enabled else {
            throw ValidationError(L10n.format(
                "%@ registration completed, but the system did not mark it as runnable.",
                displayName
            ))
        }
    }

    func unregister(service: SMAppService, label: String) throws {
        switch service.status {
        case .notRegistered, .notFound:
            return
        case .enabled, .requiresApproval:
            try service.unregister()
            guard waitUntilUnregistered(service: service, label: label, timeout: 5) else {
                throw ValidationError(L10n.format(
                    "Background service %@ did not converge to the unregistered state.",
                    label
                ))
            }
        @unknown default:
            return
        }
    }

    func reregister(service: SMAppService, label: String, displayName: String) throws {
        try unregister(service: service, label: label)
        let plistName = label == Self.coreLabel ? Self.corePlistName : Self.tunnelPlistName
        try register(service: service, plistName: plistName, displayName: displayName)
    }

    private func restoreRegistration(service: SMAppService, label: String, displayName: String) throws {
        try validateServiceManagementReadiness()
        let plistName = label == Self.coreLabel ? Self.corePlistName : Self.tunnelPlistName
        try validateBundledServiceDefinition(plistName: plistName, displayName: displayName)
        try unregister(service: service, label: label)
        try register(service: service, plistName: plistName, displayName: displayName)
    }

    private func restoreRegistrationForUpdate(
        service: SMAppService,
        label: String,
        displayName: String,
        expectedEnabled: Bool
    ) throws -> String {
        guard expectedEnabled else {
            try unregister(service: service, label: label)
            return "disabled"
        }
        do {
            try restoreRegistration(service: service, label: label, displayName: displayName)
        } catch {
            // requiresApproval reflects user/system policy. It is a commit warning, not evidence
            // that the newly installed App Bundle is invalid.
            guard service.status == .requiresApproval else { throw error }
        }
        switch service.status {
        case .enabled:
            return "enabled"
        case .requiresApproval:
            return "requires_approval"
        case .notRegistered, .notFound:
            throw ValidationError(L10n.format(
                "%@ background registration did not become available after the update.",
                displayName
            ))
        @unknown default:
            throw ValidationError(L10n.format(
                "%@ background registration returned an unknown state after the update.",
                displayName
            ))
        }
    }

    private var serviceDomain: String { "gui/\(getuid())" }

    func validatePersistentAppLocation() throws {
        let path = paths.appBundle.resolvingSymlinksInPath().path
        if path == "/Volumes" || path.hasPrefix("/Volumes/") {
            throw ValidationError(L10n.text("Move AgentDock to the Applications folder before enabling the background service."))
        }
    }

    func validateServiceManagementReadiness() throws {
        try validatePersistentAppLocation()
        if LegacyDesktopRuntimeMigration.isPresent(paths: paths) {
            throw ValidationError(L10n.text("A legacy AgentDock background layout was detected. Apply the current settings in the main panel to complete migration first."))
        }
    }

    func validateBundledServiceDefinition(plistName: String, displayName: String) throws {
        let plist = paths.appBundle
            .appendingPathComponent("Contents/Library/LaunchAgents", isDirectory: true)
            .appendingPathComponent(plistName)
        guard let values = try? plist.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey]),
              values.isRegularFile == true,
              values.isSymbolicLink != true else {
            throw ValidationError(L10n.format(
                "AgentDock.app is missing the background service definition for %@. Reinstall the application.",
                displayName
            ))
        }
    }

    private func launchdJobPresent(label: String) -> Bool {
        (try? runProcess(
            executable: "/bin/launchctl",
            arguments: ["print", "\(serviceDomain)/\(label)"]
        ).status) == 0
    }

    private func isLoaded(label: String) -> Bool {
        launchdProcessID(label: label) != nil
    }

    func launchdProcessID(label: String) -> Int? {
        guard let result = try? runProcess(
            executable: "/bin/launchctl",
            arguments: ["print", "\(serviceDomain)/\(label)"]
        ), result.status == 0 else { return nil }
        for rawLine in result.output.split(whereSeparator: \.isNewline) {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            guard line.hasPrefix("pid = "),
                  let pid = Int(line.dropFirst("pid = ".count)),
                  pid > 0 else { continue }
            return pid
        }
        return nil
    }

    func kickstartRegisteredService(
        label: String,
        displayName: String,
        killExisting: Bool
    ) throws {
        var arguments = ["kickstart"]
        if killExisting {
            arguments.append("-k")
        }
        arguments.append("\(serviceDomain)/\(label)")
        let result = try runProcess(executable: "/bin/launchctl", arguments: arguments)
        guard result.status == 0 else {
            throw ValidationError(commandError(result.output, action: displayName))
        }
    }

    func waitForLaunchdPID(label: String, timeout: TimeInterval) async -> Bool {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let deadline = Date().addingTimeInterval(timeout)
                while Date() < deadline {
                    if self.launchdProcessID(label: label) != nil {
                        continuation.resume(returning: true)
                        return
                    }
                    Thread.sleep(forTimeInterval: 0.1)
                }
                continuation.resume(returning: self.launchdProcessID(label: label) != nil)
            }
        }
    }

    private func waitForStableLaunchdProcess(label: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        var previousPID: Int?
        var stableChecks = 0
        while Date() < deadline {
            if let pid = launchdProcessID(label: label) {
                if pid == previousPID {
                    stableChecks += 1
                } else {
                    previousPID = pid
                    stableChecks = 1
                }
                if stableChecks >= 4 { return true }
            } else {
                previousPID = nil
                stableChecks = 0
            }
            Thread.sleep(forTimeInterval: 0.25)
        }
        return false
    }

    private func waitUntilUnregistered(service: SMAppService, label: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !launchdJobPresent(label: label), Self.isUnregistered(service.status) { return true }
            Thread.sleep(forTimeInterval: 0.1)
        }
        return !launchdJobPresent(label: label) && Self.isUnregistered(service.status)
    }

    static func isUnregistered(_ status: SMAppService.Status) -> Bool {
        status == .notRegistered || status == .notFound
    }

    private func waitForHealthSynchronously(configuration: ServiceConfiguration, timeout: TimeInterval) -> Bool {
        guard let url = configuration.healthURL else { return false }
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            let semaphore = DispatchSemaphore(value: 0)
            var healthy = false
            var request = URLRequest(url: url)
            request.timeoutInterval = 1.5
            URLSession.shared.dataTask(with: request) { data, response, _ in
                defer { semaphore.signal() }
                guard (response as? HTTPURLResponse)?.statusCode == 200,
                      let data,
                      let payload = try? JSONDecoder().decode(HealthPayload.self, from: data) else { return }
                healthy = payload.ok
            }.resume()
            _ = semaphore.wait(timeout: .now() + 2)
            if healthy { return true }
            Thread.sleep(forTimeInterval: 0.5)
        }
        return false
    }

    private func fetchNexusConnected() async -> Bool {
        do {
            let result = try await runInBackground {
                try runProcess(
                    executable: self.paths.binary.path,
                    arguments: ["service", "status", "--runtime-root", self.paths.appSupport.path]
                )
            }
            guard result.status == 0,
                  let data = result.output.data(using: .utf8),
                  let payload = try? JSONDecoder().decode(DesktopServiceStatusPayload.self, from: data) else {
                return false
            }
            return payload.nexusConnected
        } catch {
            return false
        }
    }

    func fetchHealth(url: URL) async -> HealthPayload? {
        do {
            var request = URLRequest(url: url)
            request.timeoutInterval = 2.5
            let (data, response) = try await URLSession.shared.data(for: request)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
            return try JSONDecoder().decode(HealthPayload.self, from: data)
        } catch {
            return nil
        }
    }

    private func fetchRuntimePayload<T: Decodable>(
        _ type: T.Type,
        configuration: ServiceConfiguration,
        path: String,
        queryItems: [URLQueryItem] = [],
        method: String = "GET",
        timeout: TimeInterval = 2.5
    ) async -> T? {
        guard let localMCPURL = configuration.localMCPURL,
              var components = URLComponents(url: localMCPURL, resolvingAgainstBaseURL: false),
              components.scheme == "http",
              isLoopbackHost(components.host) else {
            return nil
        }
        components.path = path
        components.queryItems = queryItems.isEmpty ? nil : queryItems
        components.fragment = nil
        guard let url = components.url else { return nil }

        do {
            var request = URLRequest(url: url)
            request.timeoutInterval = timeout
            request.httpMethod = method
            if !configuration.authToken.isEmpty {
                request.setValue("Bearer \(configuration.authToken)", forHTTPHeaderField: "Authorization")
            }
            let (data, response) = try await URLSession.shared.data(for: request)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
            return try JSONDecoder().decode(type, from: data)
        } catch {
            return nil
        }
    }

    private func commandError(_ output: String, action: String) -> String {
        let message = output.trimmingCharacters(in: .whitespacesAndNewlines)
        return message.isEmpty ? L10n.format("AgentDock %@ failed.", action) : message
    }

    func runInBackground<T>(_ operation: @escaping () throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                do { continuation.resume(returning: try operation()) }
                catch { continuation.resume(throwing: error) }
            }
        }
    }
}
