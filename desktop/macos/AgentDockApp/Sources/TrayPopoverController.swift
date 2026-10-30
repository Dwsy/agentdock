import AppKit
import SwiftUI

/// 托盘面板可触发的服务动作。
/// 用枚举而不是字符串，是为了让 AppDelegate 的接线在编译期覆盖全部分支，
/// 不会再出现 "restart" 之类的字面量拼写错误直接落到 else 分支。
enum TrayServiceAction {
    case start
    case stop
}

@MainActor
final class TrayPopoverController: NSObject, NSPopoverDelegate {
    private let model: TrayPopoverModel
    private let popover = NSPopover()
    private let onRequestStatusRefresh: () -> Void

    init(
        service: ServiceController,
        onRequestStatusRefresh: @escaping () -> Void,
        onOpenAgentDock: @escaping () -> Void,
        onOpenSettings: @escaping () -> Void,
        onRunServiceAction: @escaping (TrayServiceAction) -> Void,
        onShowUpdateProgress: @escaping () -> Void,
        onOpenAlertDetail: @escaping (ControlPanelTarget) -> Void
    ) {
        model = TrayPopoverModel(service: service)
        self.onRequestStatusRefresh = onRequestStatusRefresh
        super.init()
        popover.behavior = .transient
        popover.animates = true
        popover.delegate = self
        let hostingController = NSHostingController(
            rootView: TrayPopoverView(
                model: model,
                onOpenAgentDock: onOpenAgentDock,
                onOpenSettings: onOpenSettings,
                onRunServiceAction: onRunServiceAction,
                onShowUpdateProgress: onShowUpdateProgress,
                onOpenAlertDetail: onOpenAlertDetail
            )
        )
        // 面板高度随「最近调用」条数变化。写死 contentSize 会在数据不足时留出空白，
        // 因此把尺寸决定权交给 SwiftUI 的 ideal size（宽度由视图自身固定）。
        hostingController.sizingOptions = [.preferredContentSize]
        popover.contentViewController = hostingController
    }

    /// AppDelegate 每 15 秒回灌一次服务状态与告警；面板打开时才顺带刷新运行态数据（内部节流）。
    func update(status: ServiceStatus, isUpdating: Bool, isCheckingForUpdate: Bool, alerts: [RuntimeAlert]) {
        model.apply(
            status: status,
            isUpdating: isUpdating,
            isCheckingForUpdate: isCheckingForUpdate,
            alerts: alerts
        )
    }

    func toggle(relativeTo button: NSStatusBarButton) {
        if popover.isShown {
            popover.performClose(nil)
            return
        }
        show(relativeTo: button)
    }

    func show(relativeTo button: NSStatusBarButton) {
        guard !popover.isShown else { return }
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        // 面板可能显示 AppDelegate 上一轮轮询（最长 15 秒前）的状态，先让它重新取一次，
        // 避免刚打开的窗口显示过期的「已停止/未安装」。
        onRequestStatusRefresh()
        // popoverDidShow 也会触发；这里的显式调用保证首次展示立刻拉取，不依赖动画回调时序。
        model.startMonitoring()
    }

    func close() {
        popover.performClose(nil)
    }

    func popoverDidShow(_ notification: Notification) {
        model.startMonitoring()
    }

    func popoverDidClose(_ notification: Notification) {
        // 关闭即停：取消刷新循环，面板不再对 Runtime API 做任何轮询。
        model.stopMonitoring()
    }
}

// MARK: - Runtime snapshot

/// 面板单次拉取到的运行态快照。只保留面板要展示的字段，
/// 避免把 analytics 全量载荷带进视图层。
private struct TrayRuntimeSnapshot {
    let windowCalls: Int
    let windowErrors: Int
    let activeCalls: Int
    let activeTaskCount: Int?
    let blockedTaskCount: Int?
    /// 进行中任务的最新 5 条（updatedAt 倒序）。托盘只展示这个切片，
    /// 完整列表在控制面板 Tasks 页。
    let activeTasks: [TrayActiveTask]
    let recentCalls: [TrayRecentCall]
    /// codeInsertions/codeDeletions 是窗口内各工具造成的代码行数变动。
    let codeInsertions: Int
    let codeDeletions: Int

    /// windowCalls 与 recentCalls 来自同一个内存环，错误率直接由环内失败数计算，
    /// 不需要为了面板给 Runtime API 增加新字段。
    var errorRate: Double {
        windowCalls > 0 ? Double(windowErrors) / Double(windowCalls) : 0
    }
}

private struct TrayRecentCall: Identifiable {
    let id: UInt64
    let tool: String
    let success: Bool
    let durationMS: Double
    let startedAt: String
}

/// 托盘展示的进行中任务切片。只留面板要显示的字段，
/// 完整任务状态（steps/conditions/events）只在控制面板渲染。
private struct TrayActiveTask: Identifiable {
    let id: String
    let title: String
    let project: String?
    let completedStepCount: Int
    let stepCount: Int
}

/// 拉取失败的统一原因。
/// 面板必须降级成「不可用 + 原因」而不是空白，所以失败原因要能区分
/// 「没装」「Core 没跑」「Core 没响应」「请求失败」这几种完全不同的处置方式。
private enum TrayRuntimeUnavailable {
    case notInstalled
    case coreStopped
    case coreUnhealthy
    case notConfigured
    case requestFailed
}

/// 拉取前的就绪检查结果：要么拿到请求配置，要么直接给出展示用原因。
/// 用专用枚举而不是 Result，是因为这里的失败不是 Error，而是面板要展示的文案来源。
private enum FetchReadiness {
    case ready(ServiceConfiguration)
    case blocked(TrayRuntimeUnavailable)
}

// MARK: - Model

@MainActor
private final class TrayPopoverModel: ObservableObject {
    /// 两次拉取的最小间隔。status 轮询（15 秒）与反复开关面板都会触发刷新，
    /// 节流把重复请求挡在这里，避免面板变成高频轮询器。
    static let minimumFetchInterval: TimeInterval = 3
    /// 面板打开期间的刷新节奏；面板关闭后整个循环被取消。
    static let refreshInterval: TimeInterval = 5
    /// 面板高度有限，最近调用只展示最新几条。
    static let recentCallLimit = 5
    /// 进行中任务同理：只给最新更新的几条，避免 popover 被长列表撑高。
    static let visibleTaskLimit = 5

    @Published private(set) var status: ServiceStatus = .missing
    @Published private(set) var isUpdating = false
    @Published private(set) var isCheckingForUpdate = false
    @Published private(set) var snapshot: TrayRuntimeSnapshot?
    @Published private(set) var unavailable: TrayRuntimeUnavailable?
    @Published private(set) var tunnelMode: TunnelMode?
    @Published private(set) var isRefreshing = false
    /// alerts 由 AppDelegate 的 15 秒轮询回灌：告警由后端惰性评估并去重，
    /// 面板不必自己再拉一次。
    @Published private(set) var alerts: [RuntimeAlert] = []

    private let service: ServiceController
    private var monitor: Task<Void, Never>?
    private var lastFetchAt = Date.distantPast

    init(service: ServiceController) {
        self.service = service
    }

    func apply(status: ServiceStatus, isUpdating: Bool, isCheckingForUpdate: Bool, alerts: [RuntimeAlert]) {
        self.status = status
        self.isUpdating = isUpdating
        self.isCheckingForUpdate = isCheckingForUpdate
        self.alerts = alerts.filter(\.active)
        // Core 刚启动/刚停止时，面板上的结论会瞬间过期；面板没打开就不额外请求。
        guard monitor != nil else { return }
        // 结论变了必须立刻重拉，不能被 3 秒节流挡住，否则面板会先显示上一轮的「未安装/已停止」。
        if blockedReason != unavailable { lastFetchAt = .distantPast }
        Task { [weak self] in
            await self?.refresh()
        }
    }

    /// 只在面板打开期间运行：先立刻拉一次，之后按 refreshInterval 低频刷新。
    func startMonitoring() {
        guard monitor == nil else { return }
        monitor = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(nanoseconds: UInt64(Self.refreshInterval * 1_000_000_000))
            }
        }
    }

    func stopMonitoring() {
        monitor?.cancel()
        monitor = nil
    }

    /// 判断当前是否可能拿到运行态数据；拿不到时给出可以展示的原因，而不是发空请求。
    private func fetchReadiness() -> FetchReadiness {
        guard status.installed else { return .blocked(.notInstalled) }
        guard status.loaded else { return .blocked(.coreStopped) }
        guard status.healthy else { return .blocked(.coreUnhealthy) }
        guard let configuration = status.configuration else { return .blocked(.notConfigured) }
        return .ready(configuration)
    }

    /// 只取阻塞原因，用于判断面板当前展示的结论是否已经过期。
    private var blockedReason: TrayRuntimeUnavailable? {
        switch fetchReadiness() {
        case .blocked(let reason): return reason
        case .ready: return nil
        }
    }

    private func refresh() async {
        guard !isRefreshing else { return }
        let now = Date()
        guard now.timeIntervalSince(lastFetchAt) >= Self.minimumFetchInterval else { return }
        lastFetchAt = now
        isRefreshing = true
        defer { isRefreshing = false }

        let service = self.service
        // 隧道模式只是读一个本地 env 文件。即使 Core 没跑也要显示配置，所以先单独取，
        // 并放到后台队列，避免打开面板时在主线程做文件 IO。
        tunnelMode = try? await service.runInBackground { try service.configuredTunnelMode() }

        switch fetchReadiness() {
        case .blocked(let reason):
            markUnavailable(reason)
        case .ready(let configuration):
            await load(configuration: configuration, service: service)
        }
    }

    private func load(configuration: ServiceConfiguration, service: ServiceController) async {
        // 单次拉取固定两个只读请求（analytics + tasks），低于 3 个并发请求的上限。
        // 最近调用直接取 analytics 的内存环，不再额外请求 traces：
        // trace 采集可能被关闭，而 analytics 环始终可用。
        async let analyticsRequest = service.runtimeAnalytics(configuration: configuration)
        async let tasksRequest = service.runtimeTasks(configuration: configuration)
        let analytics = await analyticsRequest
        let tasks = await tasksRequest

        // 面板已关闭导致任务取消时不能回写半套数据，否则下次打开会闪一下旧值。
        guard !Task.isCancelled else { return }
        guard let analytics else {
            markUnavailable(.requestFailed)
            return
        }

        snapshot = TrayRuntimeSnapshot(
            windowCalls: max(analytics.windowCalls, analytics.recentCalls.count),
            windowErrors: analytics.recentCalls.reduce(into: 0) { count, call in
                if !call.success { count += 1 }
            },
            activeCalls: analytics.activeCalls,
            activeTaskCount: tasks.map { payload in
                payload.tasks.filter { $0.status == "active" }.count
            },
            blockedTaskCount: tasks.map { payload in
                payload.tasks.filter { $0.status == "blocked" }.count
            },
            activeTasks: tasks.map { payload in
                payload.tasks
                    .filter { $0.status == "active" }
                    .sorted { $0.updatedAt > $1.updatedAt }
                    .prefix(Self.visibleTaskLimit)
                    .map { task in
                        TrayActiveTask(
                            id: task.id,
                            title: task.title,
                            project: task.project,
                            completedStepCount: task.completedStepCount,
                            stepCount: task.stepCount
                        )
                    }
            } ?? [],
            recentCalls: analytics.recentCalls.prefix(Self.recentCallLimit).map { call in
                TrayRecentCall(
                    id: call.id,
                    tool: call.tool,
                    success: call.success,
                    durationMS: call.durationMS,
                    startedAt: call.startedAt
                )
            },
            codeInsertions: analytics.toolStats.reduce(into: 0) { total, stat in
                total += stat.insertions
            },
            codeDeletions: analytics.toolStats.reduce(into: 0) { total, stat in
                total += stat.deletions
            }
        )
        unavailable = nil
    }

    private func markUnavailable(_ reason: TrayRuntimeUnavailable) {
        snapshot = nil
        unavailable = reason
    }
}

// MARK: - View

/// 状态语义色：绿=正常、橙=需注意、红=失败、灰=停止。
private enum StatusTone {
    case normal
    case attention
    case failure
    case stopped

    var color: Color {
        switch self {
        case .normal: return .green
        case .attention: return .orange
        case .failure: return .red
        case .stopped: return Color(nsColor: .tertiaryLabelColor)
        }
    }
}

private struct TrayPopoverView: View {
    @ObservedObject var model: TrayPopoverModel
    let onOpenAgentDock: () -> Void
    let onOpenSettings: () -> Void
    let onRunServiceAction: (TrayServiceAction) -> Void
    let onShowUpdateProgress: () -> Void
    let onOpenAlertDetail: (ControlPanelTarget) -> Void

    private static let width: CGFloat = 350
    private static let cardRadius: CGFloat = 12

    var body: some View {
        VStack(alignment: .leading, spacing: 11) {
            header
            alertsCard
            activeTasksCard
            statusCard
            recentCallsCard
            actionButtons
        }
        .padding(16)
        .frame(width: Self.width)
    }

    // MARK: Header

    private var statusTitle: String {
        if model.isCheckingForUpdate { return L10n.text("Checking for updates…") }
        if model.isUpdating { return L10n.text("Updating…") }
        if !model.status.installed { return L10n.text("Not installed") }
        if model.status.healthy { return L10n.text("Running normally") }
        if model.status.requiresApproval { return L10n.text("Needs permission") }
        if model.status.loaded { return L10n.text("Service error") }
        return L10n.text("Stopped")
    }

    private var statusTone: StatusTone {
        if model.isUpdating || model.isCheckingForUpdate { return .attention }
        if model.status.healthy { return .normal }
        if model.status.requiresApproval { return .attention }
        if model.status.loaded { return .failure }
        return .stopped
    }

    /// 版本与端口。Core 未运行时 healthz 取不到版本，退回 App 自身版本，
    /// 避免标题区随服务状态忽隐忽现。
    private var identityLine: String? {
        var parts: [String] = []
        if let version = model.status.version, !version.isEmpty {
            parts.append(AppVersion.display(version))
        } else if model.status.installed {
            parts.append(AppVersion.display(AppVersion.current))
        }
        if let port = model.status.configuration?.port {
            parts.append(L10n.format("Port %d", port))
        }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    private var header: some View {
        HStack(spacing: 10) {
            AgentDockLogoView(size: 34)
            VStack(alignment: .leading, spacing: 2) {
                Text("AgentDock")
                    .font(.system(size: 16, weight: .semibold))
                if let identityLine {
                    Text(identityLine)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .help(identityLine)
                }
            }
            Spacer(minLength: 8)
            HStack(spacing: 5) {
                Circle()
                    .fill(statusTone.color)
                    .frame(width: 6, height: 6)
                Text(statusTitle)
                    .font(.system(size: 10.5, weight: .semibold))
                    .lineLimit(1)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .background(statusTone.color.opacity(0.12), in: Capsule())
            .help(statusTitle)
        }
    }

    // MARK: Alerts

    /// 托盘不展示 task_blocked 告警：阻塞任务按任务逐条生成，几十个阻塞任务
    /// 会把 popover 撑出屏幕。阻塞状态已在状态卡「Active tasks」行体现
    /// （活跃数 · 阻塞数，tone 同时转为 attention），完整告警列表在控制面板。
    private var trayAlerts: [RuntimeAlert] {
        model.alerts.filter { $0.kind != "task_blocked" }
    }

    /// 告警卡只在有活跃告警时出现：没告警时不该占面板高度。
    /// 标题按种类本地化，message 保留后端原文（detail 放 tooltip）。
    @ViewBuilder
    private var alertsCard: some View {
        if !trayAlerts.isEmpty {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 6) {
                    Text(L10n.text("Alerts"))
                        .font(.system(size: 11.5, weight: .semibold))
                    Spacer(minLength: 6)
                    Text(L10n.format("%d active", trayAlerts.count))
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
                .padding(.horizontal, 12)
                .padding(.top, 8)
                .padding(.bottom, 3)

                ForEach(Array(trayAlerts.enumerated()), id: \.element.id) { index, alert in
                    if index > 0 {
                        Divider().padding(.leading, 24)
                    }
                    alertRow(alert)
                }
            }
            .padding(.bottom, 6)
            .background(cardFill)
            .overlay(cardStroke)
        }
    }

    private func alertRow(_ alert: RuntimeAlert) -> some View {
        Button {
            onOpenAlertDetail(alertTarget(alert.kind))
        } label: {
            HStack(spacing: 7) {
                Circle()
                    .fill(alert.severity == "danger" ? StatusTone.failure.color : StatusTone.attention.color)
                    .frame(width: 6, height: 6)
                VStack(alignment: .leading, spacing: 1) {
                    Text(alertTitle(alert.kind))
                        .font(.system(size: 11.5, weight: .medium))
                        .lineLimit(1)
                    Text(alert.message)
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                Spacer(minLength: 8)
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 5)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(alert.detail.isEmpty ? alert.message : alert.detail)
    }

    /// 告警种类是后端固定集合；未知种类回退到通用标题，不丢内容。
    private func alertTitle(_ kind: String) -> String {
        switch kind {
        case "error_rate": return L10n.text("Error rate alert")
        case "trace_disk": return L10n.text("Trace disk alert")
        case "task_blocked": return L10n.text("Blocked task alert")
        default: return L10n.text("Alerts")
        }
    }

    /// 告警种类对应控制面板页面：点告警直接落到能处理它的页面。
    private func alertTarget(_ kind: String) -> ControlPanelTarget {
        switch kind {
        case "error_rate": return .analytics
        case "trace_disk": return .traces
        case "task_blocked": return .tasks
        default: return .analytics
        }
    }

    // MARK: Status card

    private var statusCard: some View {
        VStack(spacing: 0) {
            metricRow(
                symbol: "cpu",
                title: L10n.text("Core service"),
                tone: coreTone,
                value: coreText
            )
            rowDivider
            metricRow(
                symbol: "point.3.connected.trianglepath.dotted",
                title: L10n.text("Remote access"),
                tone: remoteTone,
                value: remoteText,
                help: remoteText
            )
            rowDivider
            metricRow(
                symbol: "waveform.path.ecg",
                title: L10n.text("Window calls"),
                tone: callsTone,
                value: callsText,
                help: callsHelp
            )
            rowDivider
            metricRow(
                symbol: "arrow.up.arrow.down",
                title: L10n.text("Code changes"),
                tone: codeTone,
                value: codeText,
                help: codeHelp
            )
        }
        .background(cardFill)
        .overlay(cardStroke)
    }

    private var coreText: String {
        if !model.status.installed { return L10n.text("Not installed") }
        if model.status.healthy { return L10n.text("Running") }
        if model.status.loaded { return L10n.text("Unavailable") }
        return L10n.text("Stopped")
    }

    private var coreTone: StatusTone {
        if !model.status.installed { return .stopped }
        if model.status.healthy { return .normal }
        if model.status.requiresApproval { return .attention }
        if model.status.loaded { return .failure }
        return .stopped
    }

    /// 远程连接：模式来自隧道配置，在线状态来自 Core 的 Nexus 上报。
    /// Core 没跑时探测不到在线状态，只报配置好的模式，不编造连接结果。
    private var remoteText: String {
        let mode = model.tunnelMode.map(tunnelModeText)
        guard model.status.installed else { return L10n.text("Not installed") }
        guard model.status.healthy else {
            return [mode, L10n.text("Unavailable")].compactMap { $0 }.joined(separator: " · ")
        }
        let connection = nexusText
        return [mode, connection].compactMap { $0 }.joined(separator: " · ")
    }

    private var remoteTone: StatusTone {
        guard model.status.installed, model.status.healthy else { return .stopped }
        switch model.status.nexusConnection {
        case .connected: return .normal
        case .disconnected: return .attention
        case .configurationError: return .failure
        case .unconfigured: return .stopped
        }
    }

    private var nexusText: String {
        switch model.status.nexusConnection {
        case .connected: return L10n.text("Connected")
        case .disconnected: return L10n.text("Not connected")
        case .configurationError: return L10n.text("Unavailable")
        case .unconfigured: return L10n.text("Not configured")
        }
    }

    private func tunnelModeText(_ mode: TunnelMode) -> String {
        switch mode {
        case .local: return L10n.text("Local only")
        case .quick: return L10n.text("Public tunnel")
        case .named: return L10n.text("Custom domain")
        }
    }

    // MARK: Active tasks

    /// 进行中任务卡：只列 status=active 且按 updatedAt 倒序的前 5 条。
    /// blocked 任务不进托盘（它们只会在告警里变成噪音），完整列表在控制面板。
    /// 没有进行中任务时整张卡不出现，不给面板加空高度。
    @ViewBuilder
    private var activeTasksCard: some View {
        let tasks = model.snapshot?.activeTasks ?? []
        if !tasks.isEmpty {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 6) {
                    Text(L10n.text("Active tasks"))
                        .font(.system(size: 11.5, weight: .semibold))
                    Spacer(minLength: 6)
                    Text("\(tasks.count)")
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
                .padding(.horizontal, 12)
                .padding(.top, 8)
                .padding(.bottom, 3)

                ForEach(Array(tasks.enumerated()), id: \.element.id) { index, task in
                    if index > 0 {
                        Divider().padding(.leading, 12)
                    }
                    activeTaskRow(task)
                }
            }
            .padding(.bottom, 6)
            .background(cardFill)
            .overlay(cardStroke)
        }
    }

    private func activeTaskRow(_ task: TrayActiveTask) -> some View {
        Button {
            onOpenAlertDetail(.tasks)
        } label: {
            HStack(alignment: .firstTextBaseline, spacing: 7) {
                Circle()
                    .fill(StatusTone.normal.color)
                    .frame(width: 6, height: 6)
                    .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 3 }
                VStack(alignment: .leading, spacing: 1) {
                    Text(task.title)
                        .font(.system(size: 11.5, weight: .medium))
                        .lineLimit(1)
                        .truncationMode(.tail)
                    if let project = task.project, !project.isEmpty {
                        Text(project)
                            .font(.system(size: 10.5))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                }
                Spacer(minLength: 8)
                if task.stepCount > 0 {
                    Text("\(task.completedStepCount)/\(task.stepCount)")
                        .font(.system(size: 10.5, weight: .medium, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 5)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(task.title)
    }

    private var callsText: String {
        guard let snapshot = model.snapshot else { return L10n.text("Unavailable") }
        guard snapshot.windowCalls > 0 else { return "0" }
        return "\(snapshot.windowCalls) · \(percent(snapshot.errorRate))"
    }

    private var callsTone: StatusTone {
        guard let snapshot = model.snapshot, snapshot.windowCalls > 0 else { return .stopped }
        if snapshot.errorRate >= 0.05 { return .failure }
        return snapshot.errorRate > 0 ? .attention : .normal
    }

    private var callsHelp: String {
        guard let snapshot = model.snapshot else { return L10n.text("Unavailable") }
        var text = L10n.format("%d calls · %d errors", snapshot.windowCalls, snapshot.windowErrors)
        if snapshot.activeCalls > 0 {
            text += " · " + L10n.format("%d running", snapshot.activeCalls)
        }
        return text
    }

    /// 代码变动：窗口内各工具造成的增删行数。
    private var codeText: String {
        guard let snapshot = model.snapshot else { return L10n.text("Unavailable") }
        if snapshot.codeInsertions == 0 && snapshot.codeDeletions == 0 { return "—" }
        return "+\(snapshot.codeInsertions) · -\(snapshot.codeDeletions)"
    }

    private var codeTone: StatusTone {
        guard let snapshot = model.snapshot else { return .stopped }
        if snapshot.codeInsertions == 0 && snapshot.codeDeletions == 0 { return .stopped }
        return .normal
    }

    private var codeHelp: String {
        guard let snapshot = model.snapshot else { return L10n.text("Unavailable") }
        return L10n.format("+%d added · -%d deleted", snapshot.codeInsertions, snapshot.codeDeletions)
    }

    private func percent(_ ratio: Double) -> String {
        String(format: "%.1f%%", ratio * 100)
    }

    // MARK: Recent calls

    private var recentCallsCard: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Text(L10n.text("Recent calls"))
                    .font(.system(size: 11.5, weight: .semibold))
                Spacer(minLength: 6)
                if model.isRefreshing {
                    ProgressView()
                        .controlSize(.small)
                        .scaleEffect(0.7)
                }
            }
            .padding(.horizontal, 12)
            .padding(.top, 8)
            .padding(.bottom, 3)

            if let snapshot = model.snapshot, !snapshot.recentCalls.isEmpty {
                ForEach(Array(snapshot.recentCalls.enumerated()), id: \.element.id) { index, call in
                    if index > 0 {
                        Divider().padding(.leading, 24)
                    }
                    recentCallRow(call)
                }
            } else {
                emptyStateLine
            }
        }
        .padding(.bottom, 6)
        .background(cardFill)
        .overlay(cardStroke)
    }

    @ViewBuilder
    private var emptyStateLine: some View {
        if let reason = model.unavailable {
            // 拉取失败时不留空白：明确写出不可用与原因。
            Text("\(L10n.text("Unavailable")) · \(unavailableReason(reason))")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .lineLimit(2)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
        } else if model.snapshot != nil {
            Text(L10n.text("No tool calls recorded yet."))
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
        } else {
            Text(L10n.text("Loading…"))
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
        }
    }

    private func unavailableReason(_ reason: TrayRuntimeUnavailable) -> String {
        switch reason {
        case .notInstalled: return L10n.text("Not installed")
        case .coreStopped: return L10n.text("Core is not running")
        case .coreUnhealthy: return L10n.text("Core is not responding")
        case .notConfigured: return L10n.text("Not configured")
        case .requestFailed: return L10n.text("Could not load runtime data")
        }
    }

    private func recentCallRow(_ call: TrayRecentCall) -> some View {
        HStack(spacing: 7) {
            Circle()
                .fill(call.success ? StatusTone.normal.color : StatusTone.failure.color)
                .frame(width: 6, height: 6)
            Text(call.tool)
                .font(.system(size: 11.5, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
                .help(call.tool)
            Spacer(minLength: 8)
            HStack(spacing: 4) {
                Text(RuntimeActivityFormat.duration(call.durationMS))
                    .font(.system(size: 10.5, design: .monospaced))
                    .monospacedDigit()
                Text("·")
                    .foregroundStyle(.tertiary)
                // 相对时间让「几秒前发生」在托盘里可读，不只靠刷新倒推。
                Text(RuntimeActivityFormat.relativeTime(call.startedAt))
                    .font(.system(size: 10.5))
            }
            .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }

    // MARK: Actions

    private var serviceAction: TrayServiceAction {
        model.status.loaded ? .stop : .start
    }

    private var serviceActionTitle: String {
        model.status.loaded ? L10n.text("Stop AgentDock") : L10n.text("Start AgentDock")
    }

    private var actionButtons: some View {
        VStack(spacing: 8) {
            Button {
                if model.isUpdating {
                    onShowUpdateProgress()
                } else {
                    onOpenAgentDock()
                }
            } label: {
                Text(model.isUpdating
                     ? L10n.text("Show update progress")
                     : model.status.installed ? L10n.text("Open AgentDock") : L10n.text("Set up AgentDock…"))
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)

            HStack(spacing: 8) {
                Button(serviceActionTitle) {
                    onRunServiceAction(serviceAction)
                }
                .frame(maxWidth: .infinity)
                .disabled(
                    model.isUpdating ||
                    !model.status.installed ||
                    model.status.requiresApproval
                )

                Button(L10n.text("Settings")) {
                    onOpenSettings()
                }
                .frame(maxWidth: .infinity)
                .disabled(model.isUpdating)
            }
            .controlSize(.regular)
        }
    }

    // MARK: Building blocks

    private func metricRow(
        symbol: String,
        title: String,
        tone: StatusTone,
        value: String,
        help: String? = nil
    ) -> some View {
        HStack(spacing: 9) {
            Image(systemName: symbol)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .frame(width: 14)
            Text(title)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Spacer(minLength: 8)
            HStack(spacing: 5) {
                Circle()
                    .fill(tone.color)
                    .frame(width: 5, height: 5)
                Text(value)
                    .font(.system(size: 11.5, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .monospacedDigit()
            }
            .help(help ?? value)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private var rowDivider: some View {
        Divider().padding(.leading, 35)
    }

    private var cardFill: some View {
        RoundedRectangle(cornerRadius: Self.cardRadius, style: .continuous)
            .fill(Color(nsColor: .controlBackgroundColor))
    }

    private var cardStroke: some View {
        RoundedRectangle(cornerRadius: Self.cardRadius, style: .continuous)
            .stroke(Color(nsColor: .separatorColor).opacity(0.6), lineWidth: 0.5)
    }
}
