import SwiftUI

// MARK: - Typed result models

/// workspace_context 结果的结构化投影（internal/app/workspace_context.go）。
struct RuntimeWorkspaceContext: Decodable {
    let workdir: String?
    let workspaceRoot: String?
    let instructions: [RuntimeInstructionFile]?
    let workspaceSkills: [RuntimeWorkspaceSkill]?
    let warnings: [RuntimeCapabilityWarning]?

    private enum CodingKeys: String, CodingKey {
        case workdir, instructions, warnings
        case workspaceRoot = "workspace_root"
        case workspaceSkills = "workspace_skills"
    }
}

struct RuntimeInstructionFile: Decodable {
    let scope: String?
    let path: String?
    let status: String?
}

struct RuntimeWorkspaceSkill: Decodable {
    let name: String?
    let description: String?
}

struct RuntimeCapabilityWarning: Decodable {
    let source: String?
    let message: String?
}

/// agentdock_context 结果的结构化投影（internal/app/agentdock_context.go）。
/// 只取控制面板需要展示的索引字段，运行环境细节仍由通用 JSON 卡片承载。
struct RuntimeCapabilityContext: Decodable {
    let skills: [RuntimeCapabilitySkill]?
    let plugins: [RuntimeCapabilityPlugin]?
    let dynamicMCP: [RuntimeCapabilityMCP]?
    let rules: [String]?
    let warnings: [RuntimeCapabilityWarning]?

    private enum CodingKeys: String, CodingKey {
        case skills, plugins, rules, warnings
        case dynamicMCP = "dynamic_mcp"
    }
}

struct RuntimeCapabilitySkill: Decodable {
    let name: String?
    let description: String?
    let sourceType: String?
    let pluginName: String?

    private enum CodingKeys: String, CodingKey {
        case name, description
        case sourceType = "source_type"
        case pluginName = "plugin_name"
    }
}

struct RuntimeCapabilityPlugin: Decodable {
    let name: String?
    let version: String?
    let enabled: Bool?
    let description: String?
    let skillsCount: Int?
    let mcpCount: Int?

    private enum CodingKeys: String, CodingKey {
        case name, version, enabled, description
        case skillsCount = "skills_count"
        case mcpCount = "mcp_count"
    }
}

struct RuntimeCapabilityMCP: Decodable {
    let name: String?
    let description: String?
    let status: String?
    let toolCount: Int?
    let lastErrorCode: String?

    private enum CodingKeys: String, CodingKey {
        case name, description, status
        case toolCount = "tool_count"
        case lastErrorCode = "last_error_code"
    }
}

/// task_create / task_manage 的返回契约（internal/tool/task/task_actions.go）。
/// 两个工具都走 Service.Manage，返回 action/task_id/task_summary/state_dir，
/// task_summary 是 compactTaskSummary 的投影，字段与完整 Task 不同。
struct RuntimeTaskToolResult: Decodable {
    let action: String?
    let taskId: String?
    let stateDir: String?
    let taskSummary: RuntimeTaskCompactSummary?

    private enum CodingKeys: String, CodingKey {
        case action
        case taskId = "task_id"
        case stateDir = "state_dir"
        case taskSummary = "task_summary"
    }
}

struct RuntimeTaskCompactSummary: Decodable {
    let id: String?
    let title: String?
    let status: String?
    let phase: String?
    let completedStepCount: Int?
    let stepCount: Int?
    let steps: [RuntimeTaskCompactStep]?
    let conditionCount: Int?
    let conditionRefs: [RuntimeTaskCompactCondition]?
    let reviewStatus: String?
    let currentStep: RuntimeTaskCompactStep?
    let summary: String?
    let blocker: String?
    let finalReview: RuntimeTaskCompactReview?
    let updatedAt: String?
    let completedAt: String?

    private enum CodingKeys: String, CodingKey {
        case id, title, status, phase, steps, summary, blocker
        case completedStepCount = "completed_step_count"
        case stepCount = "step_count"
        case conditionCount = "condition_count"
        case conditionRefs = "condition_refs"
        case reviewStatus = "review_status"
        case currentStep = "current_step"
        case finalReview = "final_review"
        case updatedAt = "updated_at"
        case completedAt = "completed_at"
    }
}

struct RuntimeTaskCompactStep: Decodable {
    let id: String?
    let title: String?
    let status: String?
}

struct RuntimeTaskCompactCondition: Decodable {
    let id: String?
    let text: String?
}

/// compactTaskSummary 的 final_review 只有计数，不含事实列表。
struct RuntimeTaskCompactReview: Decodable {
    let status: String?
    let summary: String?
    let verifiedCount: Int?
    let riskCount: Int?
    let reviewedAt: String?

    private enum CodingKeys: String, CodingKey {
        case status, summary
        case verifiedCount = "verified_count"
        case riskCount = "risk_count"
        case reviewedAt = "reviewed_at"
    }
}

/// read_file 结果（internal/tool/file/runtime_files.go）。
struct RuntimeReadFileResult: Decodable {
    let path: String?
    let content: String?
    let startLine: Int?
    let endLine: Int?
    let totalLines: Int?
    let sizeBytes: Int?
    let truncated: Bool?

    private enum CodingKeys: String, CodingKey {
        case path, content, truncated
        case startLine = "start_line"
        case endLine = "end_line"
        case totalLines = "total_lines"
        case sizeBytes = "size_bytes"
    }
}

/// search_text 结果（internal/tool/file/search.go）。
struct RuntimeSearchTextResult: Decodable {
    let query: String?
    let matches: [RuntimeSearchMatch]?
    let totalMatches: Int?
    let truncated: Bool?

    private enum CodingKeys: String, CodingKey {
        case query, matches, truncated
        case totalMatches = "total_matches"
    }
}

struct RuntimeSearchMatch: Decodable {
    let path: String?
    let line: Int?
    let preview: String?
}

/// list_dir 结果（internal/tool/file/runtime_files.go）。
struct RuntimeListDirResult: Decodable {
    let path: String?
    let entries: [RuntimeDirEntry]?
    let truncated: Bool?

    private enum CodingKeys: String, CodingKey {
        case path, entries, truncated
    }
}

struct RuntimeDirEntry: Decodable {
    let name: String?
    let path: String?
    let type: String?
    let sizeBytes: Int?

    private enum CodingKeys: String, CodingKey {
        case name, path, type
        case sizeBytes = "size_bytes"
    }
}

// MARK: - Dispatch

/// 已知工具的 Result 专用卡片分派。解析在 TraceDetailView.init 完成，
/// body 只做渲染；返回 nil 表示该工具没有专用卡片，调用方回退通用 JSON 渲染。
enum TraceTypedResult {
    case workspaceContext(RuntimeWorkspaceContext)
    case capabilityContext(RuntimeCapabilityContext)
    case task(RuntimeTaskToolResult)
    case readFile(RuntimeReadFileResult)
    case searchText(RuntimeSearchTextResult)
    case listDir(RuntimeListDirResult)
    case viewImage(RuntimeViewImageResult)

    static func decode(tool: String, text: String?) -> TraceTypedResult? {
        guard let text, !text.isEmpty else { return nil }
        let data = Data(text.utf8)
        switch tool {
        case "workspace_context":
            return (try? JSONDecoder().decode(RuntimeWorkspaceContext.self, from: data)).map { .workspaceContext($0) }
        case "agentdock_context":
            return (try? JSONDecoder().decode(RuntimeCapabilityContext.self, from: data)).map { .capabilityContext($0) }
        case "task_create", "task_manage":
            // 两个工具共用 Service.Manage 的返回：{action, task_id, task_summary, state_dir}。
            return (try? JSONDecoder().decode(RuntimeTaskToolResult.self, from: data)).map { .task($0) }
        case "read_file":
            return (try? JSONDecoder().decode(RuntimeReadFileResult.self, from: data)).map { .readFile($0) }
        case "search_text":
            return (try? JSONDecoder().decode(RuntimeSearchTextResult.self, from: data)).map { .searchText($0) }
        case "list_dir":
            return (try? JSONDecoder().decode(RuntimeListDirResult.self, from: data)).map { .listDir($0) }
        case "view_image":
            return (try? JSONDecoder().decode(RuntimeViewImageResult.self, from: data)).map { .viewImage($0) }
        default:
            return nil
        }
    }
}

struct TraceTypedResultCard: View {
    let result: TraceTypedResult
    let rawText: String

    var body: some View {
        switch result {
        case let .workspaceContext(context):
            TraceWorkspaceContextCard(context: context, rawText: rawText)
        case let .capabilityContext(context):
            TraceCapabilityContextCard(context: context, rawText: rawText)
        case let .task(result):
            TraceTaskCard(result: result, rawText: rawText)
        case let .readFile(file):
            TraceReadFileCard(file: file, rawText: rawText)
        case let .searchText(search):
            TraceSearchTextCard(search: search, rawText: rawText)
        case let .listDir(listing):
            TraceListDirCard(listing: listing, rawText: rawText)
        case let .viewImage(image):
            TraceViewImageCard(image: image, rawText: rawText)
        }
    }
}

// MARK: - Shared helpers

/// 卡片内的分组标题。专用卡共用同一层级视觉。
private struct TraceCardSectionTitle: View {
    let title: String
    var trailing: String? = nil

    var body: some View {
        HStack(spacing: 8) {
            Text(title)
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
            if let trailing {
                Text(trailing)
                    .font(.system(size: 10.5))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.top, 6)
    }
}

/// 变更/匹配行左侧的增删计数，正负号与颜色同时编码，不依赖单一通道。
private struct TraceDeltaLabel: View {
    let insertions: Int
    let deletions: Int

    var body: some View {
        HStack(spacing: 6) {
            if insertions > 0 {
                Text("+\(insertions)")
                    .foregroundStyle(.green)
            }
            if deletions > 0 {
                Text("−\(deletions)")
                    .foregroundStyle(.red)
            }
        }
        .font(.system(size: 10.5, weight: .medium, design: .monospaced))
    }
}

// MARK: - file_edit

/// file_edit 的文件级变更卡：一次调用可能影响多个文件（patch/move），
/// 每个文件一行，diff 按需展开。数据来自 TraceRecord.fileChanges，
/// 不走 Result 文本，因此摘要列表行同样能渲染。
struct TraceFileChangesCard: View {
    let changes: [RuntimeFileChange]
    let rawText: String

    var body: some View {
        TraceSurfaceCard(title: L10n.format("Files (%d)", changes.count), trailing: CopyButton(text: rawText)) {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(changes) { change in
                    TraceFileChangeRow(change: change)
                }
            }
        }
    }
}

private struct TraceFileChangeRow: View {
    let change: RuntimeFileChange

    @State private var showDiff = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                TraceBadge(text: change.action, color: actionColor)
                Text(change.path)
                    .font(.system(size: 11.5, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let moveTo = change.moveTo, !moveTo.isEmpty {
                    Image(systemName: "arrow.right")
                        .font(.system(size: 9))
                        .foregroundStyle(.secondary)
                    Text(moveTo)
                        .font(.system(size: 11.5, design: .monospaced))
                        .textSelection(.enabled)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer(minLength: 8)
                TraceDeltaLabel(insertions: change.insertions ?? 0, deletions: change.deletions ?? 0)
            }

            if let diff = change.diff, !diff.isEmpty {
                HStack(spacing: 8) {
                    Button(showDiff ? L10n.text("Hide diff") : L10n.text("Show diff")) {
                        showDiff.toggle()
                    }
                    .buttonStyle(.link)
                    .font(.system(size: 10.5))
                    if change.truncated == true {
                        TraceBadge(text: L10n.text("truncated"))
                    }
                }
                if showDiff {
                    TraceCodeBlock(text: diff, language: .shell)
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var actionColor: Color {
        switch change.action {
        case "add", "create": return .green
        case "delete", "remove": return .red
        case "move": return .blue
        default: return .secondary
        }
    }
}

// MARK: - workspace_context

/// workspace_context 详情卡：规则文件与本地 Skill 索引按列表渲染，
/// 比通用键值卡片更贴合该工具的结构。
struct TraceWorkspaceContextCard: View {
    let context: RuntimeWorkspaceContext
    let rawText: String

    var body: some View {
        TraceSurfaceCard(title: L10n.text("Workspace context"), trailing: CopyButton(text: rawText)) {
            VStack(alignment: .leading, spacing: 4) {
                if let workdir = context.workdir, !workdir.isEmpty {
                    TraceMetadataRow(label: L10n.text("Working directory"), value: workdir)
                }
                if let root = context.workspaceRoot, !root.isEmpty {
                    TraceMetadataRow(label: L10n.text("Workspace root"), value: root)
                }
                TraceWarningList(warnings: context.warnings)
                if let files = context.instructions, !files.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Rules (%d)", files.count))
                    ForEach(files.indices, id: \.self) { index in
                        let file = files[index]
                        VStack(alignment: .leading, spacing: 2) {
                            Text(file.path ?? "")
                                .font(.system(size: 11.5, design: .monospaced))
                                .textSelection(.enabled)
                            HStack(spacing: 6) {
                                if let scope = file.scope, !scope.isEmpty {
                                    TraceBadge(text: scope)
                                }
                                if let status = file.status, !status.isEmpty {
                                    TraceBadge(text: status)
                                }
                            }
                        }
                    }
                }
                if let skills = context.workspaceSkills, !skills.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Skills (%d)", skills.count))
                    ForEach(skills.indices, id: \.self) { index in
                        let skill = skills[index]
                        VStack(alignment: .leading, spacing: 1) {
                            Text(skill.name ?? "")
                                .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                                .textSelection(.enabled)
                            if let description = skill.description, !description.isEmpty {
                                Text(description)
                                    .font(.system(size: 11))
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
    }
}

private struct TraceWarningList: View {
    let warnings: [RuntimeCapabilityWarning]?

    var body: some View {
        if let warnings, !warnings.isEmpty {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(warnings.indices, id: \.self) { index in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text("⚠").font(.system(size: 10))
                        Text(warnings[index].message ?? "")
                            .font(.system(size: 11.5))
                            .foregroundStyle(.orange)
                    }
                }
            }
            .padding(.top, 2)
        }
    }
}

// MARK: - agentdock_context

/// agentdock_context 详情卡：能力索引（Skill / 插件 / 动态 MCP / 规则）。
/// 与 workspace_context 同族，但覆盖全局能力而非工作区规则。
struct TraceCapabilityContextCard: View {
    let context: RuntimeCapabilityContext
    let rawText: String

    var body: some View {
        TraceSurfaceCard(title: L10n.text("Capability context"), trailing: CopyButton(text: rawText)) {
            VStack(alignment: .leading, spacing: 4) {
                TraceWarningList(warnings: context.warnings)
                if let rules = context.rules, !rules.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Rules (%d)", rules.count))
                    ForEach(rules.indices, id: \.self) { index in
                        Text(rules[index])
                            .font(.system(size: 11.5))
                            .textSelection(.enabled)
                    }
                }
                if let skills = context.skills, !skills.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Skills (%d)", skills.count))
                    ForEach(skills.indices, id: \.self) { index in
                        let skill = skills[index]
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Text(skill.name ?? "")
                                .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                                .textSelection(.enabled)
                            if let source = skill.sourceType, !source.isEmpty {
                                TraceBadge(text: source)
                            }
                            if let plugin = skill.pluginName, !plugin.isEmpty {
                                TraceBadge(text: plugin, color: .purple)
                            }
                            Spacer(minLength: 8)
                            if let description = skill.description, !description.isEmpty {
                                Text(description)
                                    .font(.system(size: 11))
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                    .truncationMode(.tail)
                            }
                        }
                    }
                }
                if let plugins = context.plugins, !plugins.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Plugins (%d)", plugins.count))
                    ForEach(plugins.indices, id: \.self) { index in
                        let plugin = plugins[index]
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 8) {
                                Text(plugin.name ?? "")
                                    .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                                    .textSelection(.enabled)
                                if let version = plugin.version, !version.isEmpty {
                                    TraceBadge(text: version)
                                }
                                TraceBadge(
                                    text: plugin.enabled == true ? L10n.text("Enabled") : L10n.text("Disabled"),
                                    color: plugin.enabled == true ? .green : .secondary
                                )
                                Spacer(minLength: 8)
                                if let skills = plugin.skillsCount, skills > 0 {
                                    Text(L10n.format("Skills (%d)", skills))
                                        .font(.system(size: 10.5))
                                        .foregroundStyle(.tertiary)
                                }
                                if let mcp = plugin.mcpCount, mcp > 0 {
                                    Text(L10n.format("MCP servers (%d)", mcp))
                                        .font(.system(size: 10.5))
                                        .foregroundStyle(.tertiary)
                                }
                            }
                            if let description = plugin.description, !description.isEmpty {
                                Text(description)
                                    .font(.system(size: 11))
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                if let servers = context.dynamicMCP, !servers.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("MCP servers (%d)", servers.count))
                    ForEach(servers.indices, id: \.self) { index in
                        let server = servers[index]
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Text(server.name ?? "")
                                .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                                .textSelection(.enabled)
                            if let status = server.status, !status.isEmpty {
                                TraceBadge(text: status, color: status == "ready" ? .green : .orange)
                            }
                            if let tools = server.toolCount, tools > 0 {
                                Text(L10n.format("%d tools", tools))
                                    .font(.system(size: 10.5))
                                    .foregroundStyle(.tertiary)
                            }
                            if let code = server.lastErrorCode, !code.isEmpty {
                                TraceBadge(text: code, color: .red)
                            }
                            Spacer(minLength: 8)
                        }
                    }
                }
            }
        }
    }
}

// MARK: - task

/// task_create / task_manage 的任务状态卡。两个工具共用 Service.Manage 的
/// 返回结构（action + task_summary + state_dir），task_summary 是紧凑投影：
/// 步骤只有 id/title/status，终审只有计数没有事实列表。
struct TraceTaskCard: View {
    let result: RuntimeTaskToolResult
    let rawText: String

    private var summary: RuntimeTaskCompactSummary? { result.taskSummary }

    var body: some View {
        TraceSurfaceCard(title: L10n.text("Task"), trailing: CopyButton(text: rawText)) {
            VStack(alignment: .leading, spacing: 4) {
                if let action = result.action, !action.isEmpty {
                    TraceBadge(text: action, color: .blue)
                }
                if let title = summary?.title, !title.isEmpty {
                    Text(title)
                        .font(.system(size: 12.5, weight: .semibold))
                        .textSelection(.enabled)
                }
                if let id = summary?.id ?? result.taskId, !id.isEmpty {
                    TraceMetadataRow(label: L10n.text("Task ID"), value: id)
                }
                HStack(spacing: 6) {
                    if let status = summary?.status, !status.isEmpty {
                        TraceBadge(text: status, color: statusColor(status))
                    }
                    if let phase = summary?.phase, !phase.isEmpty {
                        TraceBadge(text: phase, color: .blue)
                    }
                    if let review = summary?.reviewStatus, !review.isEmpty {
                        TraceBadge(text: review, color: statusColor(review))
                    }
                    if let completed = summary?.completedStepCount, let total = summary?.stepCount, total > 0 {
                        Text(L10n.format("%d of %d steps", completed, total))
                            .font(.system(size: 10.5))
                            .foregroundStyle(.tertiary)
                    }
                }
                if let current = summary?.currentStep, let title = current.title, !title.isEmpty {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(L10n.text("Current step"))
                            .font(.system(size: 10.5, weight: .medium))
                            .foregroundStyle(.secondary)
                        Text(title)
                            .font(.system(size: 11.5))
                            .textSelection(.enabled)
                    }
                    .padding(.top, 2)
                }
                if let blocker = summary?.blocker, !blocker.isEmpty {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text("\u{26a0}").font(.system(size: 10))
                        Text(blocker)
                            .font(.system(size: 11.5))
                            .foregroundStyle(.orange)
                            .textSelection(.enabled)
                    }
                }
                if let steps = summary?.steps, !steps.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Steps (%d)", steps.count))
                    ForEach(steps.indices, id: \.self) { index in
                        let step = steps[index]
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Text(step.id ?? "")
                                .font(.system(size: 10.5, design: .monospaced))
                                .foregroundStyle(.tertiary)
                                .frame(width: 42, alignment: .leading)
                            Text(step.title ?? "")
                                .font(.system(size: 11.5))
                                .textSelection(.enabled)
                            Spacer(minLength: 8)
                            if let status = step.status, !status.isEmpty {
                                TraceBadge(text: status, color: statusColor(status))
                            }
                        }
                    }
                }
                if let refs = summary?.conditionRefs, !refs.isEmpty {
                    TraceCardSectionTitle(title: L10n.format("Conditions (%d)", refs.count))
                    ForEach(refs.indices, id: \.self) { index in
                        Text(refs[index].text ?? "")
                            .font(.system(size: 11.5))
                            .textSelection(.enabled)
                    }
                }
                if let review = summary?.finalReview {
                    TraceCardSectionTitle(title: L10n.text("Final review"))
                    HStack(spacing: 6) {
                        if let status = review.status, !status.isEmpty {
                            TraceBadge(text: status, color: statusColor(status))
                        }
                        if let verified = review.verifiedCount, verified > 0 {
                            TraceBadge(text: L10n.format("%d verified facts", verified), color: .green)
                        }
                        if let risks = review.riskCount, risks > 0 {
                            TraceBadge(text: L10n.format("%d open risks", risks), color: .orange)
                        }
                    }
                    if let text = review.summary, !text.isEmpty {
                        Text(text)
                            .font(.system(size: 11.5))
                            .textSelection(.enabled)
                    }
                }
                if let text = summary?.summary, !text.isEmpty {
                    TraceCardSectionTitle(title: L10n.text("Summary"))
                    Text(text)
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                if let stateDir = result.stateDir, !stateDir.isEmpty {
                    TraceMetadataRow(label: L10n.text("State directory"), value: stateDir)
                }
            }
        }
    }

    private func statusColor(_ status: String) -> Color {
        switch status {
        case "completed", "passed", "done": return .green
        case "blocked", "failed": return .red
        case "active", "running", "in_progress": return .blue
        default: return .secondary
        }
    }
}

/// view_image 结果（internal/tool/media/view_image.go）。
/// trace 会剥离内联 base64（见 app.traceImageResultText），只留元数据。
struct RuntimeViewImageResult: Decodable {
    let source: String?
    let image: RuntimeImageMetadata?
    let original: RuntimeImageMetadata?
    let resized: Bool?
    let warnings: [String]?
    let imageBytesOmitted: Int?
    let mcpImageMimeType: String?

    private enum CodingKeys: String, CodingKey {
        case source, image, original, resized, warnings
        case imageBytesOmitted = "image_bytes_omitted"
        case mcpImageMimeType = "_mcp_image_mime_type"
    }
}

struct RuntimeImageMetadata: Decodable {
    let filename: String?
    let mimeType: String?
    let sizeBytes: Int?
    let width: Int?
    let height: Int?

    private enum CodingKeys: String, CodingKey {
        case filename, width, height
        case mimeType = "mime_type"
        case sizeBytes = "size_bytes"
    }
}


// MARK: - view_image

/// view_image 详情卡：来源 + 处理后/原始图像元数据。图片本体不进调用追踪
/// （内联 base64 会占满预览），所以这里只说明省略了多少字节。
struct TraceViewImageCard: View {
    let image: RuntimeViewImageResult
    let rawText: String

    var body: some View {
        TraceSurfaceCard(title: L10n.text("Image"), trailing: CopyButton(text: rawText)) {
            VStack(alignment: .leading, spacing: 4) {
                if let source = image.source, !source.isEmpty {
                    TraceMetadataRow(label: L10n.text("Source"), value: source)
                }
                if let metadata = image.image {
                    HStack(spacing: 6) {
                        if let mime = metadata.mimeType, !mime.isEmpty {
                            TraceBadge(text: mime, color: .blue)
                        }
                        if let width = metadata.width, let height = metadata.height, width > 0, height > 0 {
                            TraceBadge(text: "\(width) × \(height)")
                        }
                        if let size = metadata.sizeBytes, size > 0 {
                            TraceBadge(text: TraceFormat.bytes(size))
                        }
                        if image.resized == true {
                            TraceBadge(text: L10n.text("resized"), color: .orange)
                        }
                    }
                    .padding(.vertical, 2)
                    if let filename = metadata.filename, !filename.isEmpty {
                        TraceMetadataRow(label: L10n.text("File name"), value: filename)
                    }
                }
                // 原始尺寸只在处理改动了图像时才有对比价值。
                if image.resized == true, let original = image.original {
                    TraceMetadataRow(
                        label: L10n.text("Original"),
                        value: originalSummary(original)
                    )
                }
                if let omitted = image.imageBytesOmitted, omitted > 0 {
                    Text(L10n.format("Image data (%@) is not recorded in call traces.", TraceFormat.bytes(omitted)))
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                }
                if let warnings = image.warnings, !warnings.isEmpty {
                    ForEach(warnings.indices, id: \.self) { index in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Text("\u{26a0}").font(.system(size: 10))
                            Text(warnings[index])
                                .font(.system(size: 11))
                                .foregroundStyle(.orange)
                        }
                    }
                }
            }
        }
    }

    private func originalSummary(_ original: RuntimeImageMetadata) -> String {
        var parts: [String] = []
        if let width = original.width, let height = original.height, width > 0, height > 0 {
            parts.append("\(width) × \(height)")
        }
        if let size = original.sizeBytes, size > 0 {
            parts.append(TraceFormat.bytes(size))
        }
        if let mime = original.mimeType, !mime.isEmpty {
            parts.append(mime)
        }
        return parts.joined(separator: " · ")
    }
}

// MARK: - read_file

/// read_file 详情卡：文件范围徽章 + 带行号的正文。
/// 通用 JSON 卡片只能显示一坨 content，看不出"这是第几行到第几行"。
struct TraceReadFileCard: View {
    let file: RuntimeReadFileResult
    let rawText: String

    var body: some View {
        TraceSurfaceCard(title: L10n.text("File content"), trailing: CopyButton(text: file.content ?? rawText)) {
            VStack(alignment: .leading, spacing: 4) {
                if let path = file.path, !path.isEmpty {
                    TraceMetadataRow(label: L10n.text("Path"), value: path)
                }
                HStack(spacing: 6) {
                    if let start = file.startLine, let end = file.endLine {
                        TraceBadge(text: L10n.format("lines %d–%d", start, end))
                    }
                    if let total = file.totalLines, total > 0 {
                        TraceBadge(text: L10n.format("%d lines", total))
                    }
                    if let size = file.sizeBytes, size > 0 {
                        TraceBadge(text: TraceFormat.bytes(size))
                    }
                    if file.truncated == true {
                        TraceBadge(text: L10n.text("truncated"), color: .orange)
                    }
                }
                .padding(.vertical, 2)

                if let content = file.content, !content.isEmpty {
                    TraceNumberedCodeBlock(
                        text: content,
                        language: .plain,
                        firstLine: file.startLine ?? 1
                    )
                } else {
                    Text("—").foregroundStyle(.secondary)
                }
            }
        }
    }
}

/// 带行号的等宽代码块。行号列固定宽度右对齐，正文与行号同字体，
/// 分页展开逻辑与 TraceCodeBlock 一致。
struct TraceNumberedCodeBlock: View {
    private static let pageSize = 400

    let language: TraceCodeLanguage
    private let lines: [String]
    private let firstLine: Int
    @State private var visibleLines = TraceNumberedCodeBlock.pageSize

    init(text: String, language: TraceCodeLanguage, firstLine: Int) {
        self.language = language
        self.lines = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        self.firstLine = max(1, firstLine)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            LazyVStack(alignment: .leading, spacing: 1) {
                ForEach(0..<min(lines.count, visibleLines), id: \.self) { index in
                    HStack(alignment: .firstTextBaseline, spacing: 10) {
                        Text("\(firstLine + index)")
                            .font(.system(size: 10.5, design: .monospaced))
                            .foregroundStyle(.tertiary)
                            .frame(width: 46, alignment: .trailing)
                        TraceCodeHighlighter.styled(lines[index], language: language)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
            .textSelection(.enabled)

            if lines.count > visibleLines {
                Button(L10n.format("Load %d more lines", min(TraceNumberedCodeBlock.pageSize, lines.count - visibleLines))) {
                    visibleLines += TraceNumberedCodeBlock.pageSize
                }
                .buttonStyle(.link)
                .font(.system(size: 11))
            }
        }
    }
}

// MARK: - search_text

/// search_text 详情卡：匹配按文件分组，路径只显示一次，行号与预览并列。
struct TraceSearchTextCard: View {
    let search: RuntimeSearchTextResult
    let rawText: String

    private struct Group {
        let path: String
        let matches: [RuntimeSearchMatch]
    }

    private var groups: [Group] {
        guard let matches = search.matches else { return [] }
        var order: [String] = []
        var buckets: [String: [RuntimeSearchMatch]] = [:]
        for match in matches {
            let path = match.path ?? ""
            if buckets[path] == nil {
                order.append(path)
                buckets[path] = []
            }
            buckets[path]?.append(match)
        }
        return order.map { Group(path: $0, matches: buckets[$0] ?? []) }
    }

    var body: some View {
        TraceSurfaceCard(
            title: L10n.text("Search matches"),
            trailing: CopyButton(text: rawText)
        ) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    if let query = search.query, !query.isEmpty {
                        TraceBadge(text: query, color: .blue)
                    }
                    if let total = search.totalMatches {
                        TraceBadge(text: L10n.format("%d matches", total))
                    }
                    if search.truncated == true {
                        TraceBadge(text: L10n.text("truncated"), color: .orange)
                    }
                }
                ForEach(groups, id: \.path) { group in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(group.path)
                            .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                            .textSelection(.enabled)
                        ForEach(group.matches.indices, id: \.self) { index in
                            let match = group.matches[index]
                            HStack(alignment: .firstTextBaseline, spacing: 8) {
                                Text("\(match.line ?? 0)")
                                    .font(.system(size: 10.5, design: .monospaced))
                                    .foregroundStyle(.tertiary)
                                    .frame(width: 46, alignment: .trailing)
                                Text((match.preview ?? "").trimmingCharacters(in: .whitespaces))
                                    .font(.system(size: 11, design: .monospaced))
                                    .lineLimit(1)
                                    .truncationMode(.tail)
                                    .textSelection(.enabled)
                            }
                        }
                    }
                }
            }
        }
    }
}

// MARK: - list_dir

/// list_dir 详情卡：条目按类型着色，目录后缀斜杠，文件显示大小。
struct TraceListDirCard: View {
    let listing: RuntimeListDirResult
    let rawText: String

    var body: some View {
        TraceSurfaceCard(title: L10n.text("Directory entries"), trailing: CopyButton(text: rawText)) {
            VStack(alignment: .leading, spacing: 3) {
                if let path = listing.path, !path.isEmpty {
                    TraceMetadataRow(label: L10n.text("Path"), value: path)
                }
                HStack(spacing: 6) {
                    if let entries = listing.entries {
                        TraceBadge(text: L10n.format("%d entries", entries.count))
                    }
                    if listing.truncated == true {
                        TraceBadge(text: L10n.text("truncated"), color: .orange)
                    }
                }
                .padding(.vertical, 2)
                if let entries = listing.entries {
                    ForEach(entries.indices, id: \.self) { index in
                        let entry = entries[index]
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Image(systemName: entry.type == "dir" ? "folder" : "doc")
                                .font(.system(size: 9))
                                .foregroundStyle(entry.type == "dir" ? Color.blue : Color.secondary)
                            Text(entry.name ?? "")
                                .font(.system(size: 11.5, design: .monospaced))
                                .textSelection(.enabled)
                            Spacer(minLength: 8)
                            if let size = entry.sizeBytes, entry.type != "dir", size > 0 {
                                Text(TraceFormat.bytes(size))
                                    .font(.system(size: 10.5))
                                    .foregroundStyle(.tertiary)
                            }
                        }
                    }
                }
            }
        }
    }
}
