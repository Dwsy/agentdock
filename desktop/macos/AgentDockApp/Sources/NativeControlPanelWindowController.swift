import AppKit
import SwiftUI

@MainActor
final class NativeControlPanelWindowController: NSWindowController, NSWindowDelegate {
    private let model: ControlPanelModel
    private lazy var permissionsWindow = DesktopPermissionsWindowController()

    var hasActiveServiceOperation: Bool { model.isBusy }

    init(
        service: ServiceController,
        menuLoginAgent: MenuLoginAgentController,
        onChanged: @escaping () -> Void,
        onUpdateRequested: @escaping () -> Void
    ) {
        model = ControlPanelModel(
            service: service,
            menuLoginAgent: menuLoginAgent,
            onChanged: onChanged,
            onUpdateRequested: onUpdateRequested
        )
        // 侧边栏固定 240pt + 内容区，1060 在长页面里显得拥挤，启动宽度提到 1272（+20%）。
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1272, height: 720),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = "AgentDock"
        // 内容延伸至透明标题栏时，原生窗口标题会盖住滚动到顶部的页面标题。
        // 仅隐藏标题文字，保留窗口按钮和系统窗口名称。
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.setFrame(NSRect(x: 0, y: 0, width: 1272, height: 720), display: false)
        window.minSize = NSSize(width: 900, height: 620)
        window.center()
        super.init(window: window)
        window.delegate = self
        window.contentViewController = {
            let hosting = NSHostingController(rootView: ControlPanelRootView(model: model))
            // 默认的 .preferredContentSize 会让页面内容反向驱动窗口尺寸，某些页面
            // （无固有高度的列表/图表）会把窗口高度拉到屏幕高度。窗口尺寸由我们
            // 自己的 frame 与 minSize 控制。
            hosting.sizingOptions = []
            return hosting
        }()
        NotificationCenter.default.addObserver(
            forName: .agentDockOpenPermissions,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in self?.presentPermissions() }
        }
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func present(status: ServiceStatus) {
        model.update(status)
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func update(status: ServiceStatus) { model.update(status) }
    func refreshServiceStatus(_ status: ServiceStatus) { model.update(status) }

    func presentActivity(status: ServiceStatus) {
        model.page = .activity
        present(status: status)
    }

    func presentSettings(status: ServiceStatus) {
        model.page = .settings
        present(status: status)
    }

    func setUpdateActivity(_ activity: DesktopUpdateActivity) {
        model.updateActivity = activity
    }

    func invalidatePublicEndpointCheck() {
        model.invalidatePublicEndpointCheck()
    }

    func presentPermissions() { permissionsWindow.present() }
}

@MainActor
private final class ControlPanelModel: ObservableObject {
    enum Page: String, CaseIterable, Identifiable {
        case home, connections, capabilities, tasks, files, published, traces, analytics, activity, settings
        var id: String { rawValue }
        var title: String {
            switch self {
            case .home: return L10n.text("Home")
            case .connections: return L10n.text("Connections")
            case .capabilities: return L10n.text("Capabilities")
            case .tasks: return L10n.text("Tasks")
            case .files: return L10n.text("Files")
            case .published: return L10n.text("Published")
            case .traces: return L10n.text("Traces")
            case .analytics: return L10n.text("Analytics")
            case .activity: return L10n.text("Activity")
            case .settings: return L10n.text("Settings")
            }
        }
        var symbol: String {
            switch self {
            case .home: return "house"
            case .connections: return "link"
            case .capabilities: return "square.grid.2x2"
            case .tasks: return "checklist"
            case .files: return "folder"
            case .published: return "link"
            case .traces: return "waveform.path.ecg"
            case .analytics: return "chart.bar.xaxis"
            case .activity: return "clock.arrow.circlepath"
            case .settings: return "gearshape"
            }
        }
    }

    /// 能力目录分类。侧栏只放页面导航，目录条目进二级可搜索列表，避免把侧栏铺满。
    enum CatalogSection: String, CaseIterable, Identifiable {
        case tools, skills, commonSkills, rules, plugins

        var id: String { rawValue }

        var title: String {
            switch self {
            case .tools: return L10n.text("Tools")
            case .skills: return L10n.text("Skills")
            case .commonSkills: return L10n.text("Common skills")
            case .rules: return L10n.text("Rules")
            case .plugins: return L10n.text("Plugins")
            }
        }

        var symbol: String {
            switch self {
            case .tools: return "wrench.and.screwdriver"
            case .skills: return "book.closed"
            case .commonSkills: return "books.vertical"
            case .rules: return "doc.text"
            case .plugins: return "puzzlepiece.extension"
            }
        }
    }

    struct CatalogEntry: Identifiable {
        let id: String
        let title: String
        let subtitle: String?
        let selection: SidebarSelection
    }

    /// Sidebar 只承载页面导航，能力目录条目通过 selection 进入二级列表。
    enum SidebarSelection: Hashable {
        case page(Page)
        case catalog(CatalogSection)
        case tool(String)
        case skill(String)
        case commonSkill(String)
        case rule(String)
        case plugin(String)
    }

    enum SettingsPage: String, CaseIterable, Identifiable {
        case appearance, permissions, startup, logs, traces, advancedConnection, about

        var id: String { rawValue }
        var title: String {
            switch self {
            case .permissions: return L10n.text("Permissions")
            case .startup: return L10n.text("Startup")
            case .logs: return L10n.text("Logs")
            case .traces: return L10n.text("Call tracing")

            case .advancedConnection: return L10n.text("Advanced connection")
            case .appearance: return L10n.text("Appearance")
            case .about: return L10n.text("About")

            }
        }
    }

    struct ComponentOperationState {
        let text: String
        let progress: Double?
    }

    /// page 与 selection 是同一份导航状态的两个视图：
    /// 点击侧栏条目走 selection，代码内跳转（通知/按钮）走 page，两者互相收敛。
    @Published var page: Page = .home {
        didSet {
            if selection != .page(page) {
                selection = .page(page)
            }
        }
    }
    @Published var selection: SidebarSelection = .page(.home) {
        didSet {
            if case .page(let value) = selection, value != page {
                page = value
            }
        }
    }
    @Published var settingsPage: SettingsPage = .appearance
    @Published var status: ServiceStatus = .missing
    @Published var dashboard = RuntimeDashboardSnapshot.empty
    @Published var catalog: RuntimeCatalogPayload?
    @Published var runtimeSkills: RuntimeSkillsPayload?
    @Published var runtimePlugins: RuntimePluginsPayload?
    @Published var runtimeTasks: RuntimeTasksPayload?
    @Published var cloudflaredComponent = CloudflaredComponentStatus.unavailable
    @Published var statusUpdatedAt = Date()
    @Published private(set) var languageRevision = 0
    @Published var isBusy = false
    @Published private(set) var quickTunnelGenerating = false
    @Published var updateActivity: DesktopUpdateActivity = .idle
    @Published var message: String?
    @Published var notice: String?
    @Published private(set) var cloudflaredComponentOperation: ComponentOperationState?
    @Published private(set) var publicEndpointCheckRevision = 0

    let service: ServiceController
    let menuLoginAgent: MenuLoginAgentController
    let onChanged: () -> Void
    let onUpdateRequested: () -> Void
    private lazy var installer = InstallerRunner(service: service)
    private lazy var configurationController = ServiceConfigurationController(service: service)
    private var cloudflaredComponentOperationRevision = 0
    // 目录数据较重，status 轮询会反复触发；20 秒内复用上一次结果。
    private var lastRuntimeDataRefresh = Date.distantPast

    init(service: ServiceController, menuLoginAgent: MenuLoginAgentController, onChanged: @escaping () -> Void, onUpdateRequested: @escaping () -> Void) {
        self.service = service
        self.menuLoginAgent = menuLoginAgent
        self.onChanged = onChanged
        self.onUpdateRequested = onUpdateRequested
    }

    var nexusDevice: NexusDeviceStatus { service.nexusDeviceStatus() }

    func update(_ status: ServiceStatus) {
        self.status = status
        if !status.loaded || !status.healthy {
            dashboard = .empty
            catalog = nil
            runtimeSkills = nil
            runtimePlugins = nil
            runtimeTasks = nil
        }
        statusUpdatedAt = Date()
    }

    func openLogs() { service.openLogs() }
    func openConfiguration() { service.openConfiguration() }

    func refresh() async {
        update(await service.status())
        cloudflaredComponent = await service.cloudflaredComponentStatus()
    }

    func refreshCloudflaredComponent() async {
        cloudflaredComponent = await service.cloudflaredComponentStatus()
    }

    func refreshDashboard() async {
        guard status.loaded, status.healthy else {
            dashboard = .empty
            return
        }
        dashboard = await service.dashboard(configuration: status.configuration)
    }

    /// 能力目录与任务列表都来自只读 Runtime API，一次并发拉取，避免逐个串行请求。
    func refreshRuntimeData(force: Bool = false) async {
        let now = Date()
        if !force, now.timeIntervalSince(lastRuntimeDataRefresh) < 20 {
            return
        }
        lastRuntimeDataRefresh = now
        guard status.loaded, status.healthy, let configuration = status.configuration else {
            catalog = nil
            runtimeSkills = nil
            runtimePlugins = nil
            runtimeTasks = nil
            return
        }
        async let catalogTask = service.runtimeCatalog(configuration: configuration)
        async let skillsTask = service.runtimeSkills(configuration: configuration)
        async let pluginsTask = service.runtimePlugins(configuration: configuration)
        async let tasksTask = service.runtimeTasks(configuration: configuration)
        catalog = await catalogTask
        runtimeSkills = await skillsTask
        runtimePlugins = await pluginsTask
        runtimeTasks = await tasksTask
    }

    func loadTaskDetail(id: String) async -> RuntimeTaskDetail? {
        await service.runtimeTask(id: id, configuration: status.configuration)
    }

    func loadTraces(query: String, tool: String, source: String, status filter: String) async -> RuntimeTracesPayload? {
        // 后端单次查询上限 500；UI 分批渲染，不必为了首屏少拉而丢历史。
        let summary = await service.runtimeTraces(
            query: query,
            tool: tool,
            source: source,
            status: filter,
            view: "summary",
            limit: 500,
            configuration: status.configuration
        )
        if summary != nil { return summary }
        // 与未支持 view=summary 的旧 Core 混用时回退到完整列表，仍能展示记录。
        return await service.runtimeTraces(
            query: query,
            tool: tool,
            source: source,
            status: filter,
            view: "full",
            limit: 500,
            configuration: status.configuration
        )
    }

    /// 列表只带摘要，正文（命令/stdout/stderr）在选中记录后单独回读。
    /// full=true 时附带旁路全文副本；副本缺失时 payload.full_available=false。
    func loadTraceDetail(id: UInt64, full: Bool = false) async -> RuntimeTraceDetailPayload? {
        await service.runtimeTraceDetail(id: id, full: full, configuration: status.configuration)
    }

    func loadArtifacts() async -> RuntimeArtifactsPayload? {
        await service.runtimeArtifacts(configuration: status.configuration)
    }

    func loadPluginDetail(name: String) async -> RuntimePluginDetail? {
        await service.runtimePlugin(name: name, configuration: status.configuration)
    }

    func loadFileChanges(query: String) async -> RuntimeFileChangesPayload? {
        await service.runtimeFileChanges(query: query, configuration: status.configuration)
    }

    func catalogTool(named name: String) -> RuntimeCatalogTool? {
        catalog?.tools.first { $0.name == name }
    }

    func catalogSkill(reference: String) -> RuntimeSkillItem? {
        runtimeSkills?.skills.first { $0.skillRef == reference }
    }

    func catalogCommonSkill(reference: String) -> RuntimeCommonSkillItem? {
        catalog?.commonSkills?.items.first { $0.skillRef == reference }
    }

    func catalogRule(path: String) -> RuntimeCatalogRule? {
        catalog?.rules.first { $0.path == path }
    }

    func catalogPlugin(named name: String) -> RuntimePluginItem? {
        runtimePlugins?.plugins.first { $0.name == name }
    }

    func catalogEntryCount(for section: CatalogSection) -> Int {
        catalogEntries(for: section).count
    }

    func catalogEntries(for section: CatalogSection) -> [CatalogEntry] {
        switch section {
        case .tools:
            return (catalog?.tools ?? []).map {
                CatalogEntry(id: "tool:\($0.name)", title: $0.name, subtitle: $0.title, selection: .tool($0.name))
            }
        case .skills:
            return (runtimeSkills?.skills ?? []).map {
                CatalogEntry(id: "skill:\($0.skillRef)", title: $0.name, subtitle: $0.sourceType, selection: .skill($0.skillRef))
            }
        case .commonSkills:
            return (catalog?.commonSkills?.items ?? []).map {
                CatalogEntry(id: "common:\($0.skillRef)", title: $0.name, subtitle: $0.file, selection: .commonSkill($0.skillRef))
            }
        case .rules:
            return (catalog?.rules ?? []).map {
                CatalogEntry(
                    id: "rule:\($0.path)",
                    title: ($0.path as NSString).lastPathComponent,
                    subtitle: $0.scope.isEmpty ? $0.path : "\($0.scope) · \($0.path)",
                    selection: .rule($0.path)
                )
            }
        case .plugins:
            return (runtimePlugins?.plugins ?? []).map {
                CatalogEntry(id: "plugin:\($0.name)", title: $0.name, subtitle: $0.format, selection: .plugin($0.name))
            }
        }
    }

    func invalidatePublicEndpointCheck() {
        publicEndpointCheckRevision &+= 1
    }

    @discardableResult
    func applyTunnel(mode: TunnelMode, serverURL: String, tunnelToken: String) async -> Bool {
        let generatingQuickTunnel = mode == .quick
        if generatingQuickTunnel {
            quickTunnelGenerating = true
            invalidatePublicEndpointCheck()
        }

        let succeeded = await perform(recheckPublicEndpointOnSuccess: true) {
            try await self.service.configureTunnel(mode: mode, serverURL: serverURL, tunnelToken: tunnelToken)
        }

        if generatingQuickTunnel {
            // 失败路径也必须重新读取真实配置，不能让旧临时地址重新出现在界面上。
            update(await service.status())
            quickTunnelGenerating = false
        }
        cloudflaredComponent = await service.cloudflaredComponentStatus()
        return succeeded
    }

    func runCloudflaredComponentAction(_ action: String) async {
        cloudflaredComponentOperationRevision &+= 1
        let operationRevision = cloudflaredComponentOperationRevision
        await perform(recheckPublicEndpointOnSuccess: true) {
            self.cloudflaredComponentOperation = self.initialCloudflaredComponentOperation(action)
            let onProgress: (CloudflaredComponentProgress) -> Void = { [weak self] progress in
                Task { @MainActor in
                    guard let self,
                          self.cloudflaredComponentOperationRevision == operationRevision else { return }
                    self.cloudflaredComponentOperation = self.cloudflaredComponentOperationState(progress)
                }
            }
            switch action {
            case "update":
                self.cloudflaredComponent = try await self.service.updateCloudflaredComponent(onProgress: onProgress)
            case "uninstall":
                self.cloudflaredComponent = try await self.service.uninstallCloudflaredComponent()
            default:
                self.cloudflaredComponent = try await self.service.installCloudflaredComponent(onProgress: onProgress)
            }
        }
        cloudflaredComponentOperationRevision &+= 1
        cloudflaredComponentOperation = nil
        cloudflaredComponent = await service.cloudflaredComponentStatus()
    }

    private func initialCloudflaredComponentOperation(_ action: String) -> ComponentOperationState {
        switch action {
        case "update":
            return ComponentOperationState(text: L10n.text("Updating component…"), progress: nil)
        case "uninstall":
            return ComponentOperationState(text: L10n.text("Uninstalling component…"), progress: nil)
        default:
            return ComponentOperationState(
                text: cloudflaredComponent.state == "broken"
                    ? L10n.text("Repairing component…")
                    : L10n.text("Installing component…"),
                progress: nil
            )
        }
    }

    private func cloudflaredComponentOperationState(
        _ event: CloudflaredComponentProgress
    ) -> ComponentOperationState {
        switch event.stage {
        case "catalog":
            return ComponentOperationState(text: L10n.text("Checking component metadata…"), progress: nil)
        case "downloading":
            guard let bytes = event.bytes, let total = event.total, total > 0 else {
                return ComponentOperationState(text: L10n.text("Downloading component…"), progress: nil)
            }
            let completed = min(max(bytes, 0), total)
            let percentage = Int(completed * 100 / total)
            return ComponentOperationState(
                text: L10n.format("Downloading component… %d%%", percentage),
                progress: Double(completed) / Double(total)
            )
        case "verifying":
            return ComponentOperationState(text: L10n.text("Verifying component…"), progress: nil)
        case "importing":
            return ComponentOperationState(text: L10n.text("Importing existing component…"), progress: nil)
        case "ready":
            return ComponentOperationState(text: L10n.text("Ready"), progress: 1)
        default:
            return ComponentOperationState(text: L10n.text("Processing component…"), progress: nil)
        }
    }

    @discardableResult
    func pairNexus(endpoint: String, code: String) async -> Bool {
        await perform(recheckPublicEndpointOnSuccess: true) {
            try await self.service.pairNexus(endpoint: endpoint, pairingCode: code)
        }
    }

    @discardableResult
    func applySettings(_ settings: EditableServiceSettings) async -> Bool {
        let applied = await perform(recheckPublicEndpointOnSuccess: true) {
            try await self.configurationController.apply(settings)
        }
        if applied {
            notice = L10n.text("Saved. Restart AgentDock Core to apply the new configuration.")
        }
        return applied
    }

    func setCoreAutostart(_ enabled: Bool) async {
        await perform(recheckPublicEndpointOnSuccess: true) {
            try await self.service.setAutostart(enabled: enabled)
        }
    }

    func setMenuAutostart(_ enabled: Bool) async {
        await perform { try self.menuLoginAgent.setEnabled(enabled) }
    }

    func requestUpdate() { onUpdateRequested() }

    func setLanguagePreference(_ preference: UILanguagePreference) {
        guard preference != L10n.languagePreference() else { return }
        L10n.setLanguagePreference(preference)
        languageRevision += 1
    }

    func setThemePreference(_ preference: UIThemePreference) {
        guard preference != AppAppearance.preference() else { return }
        AppAppearance.setPreference(preference)
    }

    @discardableResult
    private func perform(
        recheckPublicEndpointOnSuccess: Bool = false,
        _ operation: @escaping () async throws -> Void
    ) async -> Bool {
        guard !isBusy, !updateActivity.locksApplication else { return false }
        isBusy = true
        defer { isBusy = false }
        message = nil
        notice = nil
        do {
            try await operation()
            update(await service.status())
            if recheckPublicEndpointOnSuccess {
                invalidatePublicEndpointCheck()
            }
            onChanged()
            return true
        } catch {
            message = error.localizedDescription
            return false
        }
    }

    func toggleRuntime() {
        guard !isBusy, !updateActivity.locksApplication else { return }
        isBusy = true
        message = nil
        Task {
            do {
                if !status.installed {
                    _ = try await installer.run(request: InstallRequest(mode: .local, serverURL: "", tunnelToken: ""))
                } else if status.loaded {
                    try await service.stop()
                } else {
                    _ = try await service.start()
                }
                status = await service.status()
                cloudflaredComponent = await service.cloudflaredComponentStatus()
                invalidatePublicEndpointCheck()
                onChanged()
            } catch {
                message = error.localizedDescription
            }
            isBusy = false
        }
    }
}

private struct ControlPanelRootView: View {
    @ObservedObject var model: ControlPanelModel

    var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                HStack(spacing: 9) {
                    AgentDockLogoView(size: 20)
                    Text("AgentDock").font(.system(size: 15, weight: .semibold))
                    Spacer()
                }
                .padding(.horizontal, 14)
                // fullSizeContentView 已经给出标题栏安全区，再叠 18pt 会把侧栏标题推得偏高。
                .padding(.top, 6)
                .padding(.bottom, 10)

                List(selection: $model.selection) {
                    Section {
                        ForEach(ControlPanelModel.Page.allCases) { page in
                            Label(page.title, systemImage: page.symbol)
                                .tag(ControlPanelModel.SidebarSelection.page(page))
                        }
                    }
                }
                .id(model.languageRevision)
                .listStyle(.sidebar)
            }
            .navigationSplitViewColumnWidth(min: 220, ideal: 240, max: 300)
        } detail: {
            pageContent
                // 原生透明标题栏没有可见标题，右侧内容不应再为系统安全区重复留白。
                // 统一保留 44pt 的可拖动顶边，页面自身保留 16pt 的顶部间距，总体约 60pt。
                .padding(.top, 44)
                .ignoresSafeArea(.container, edges: .top)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                .background(Color(nsColor: .windowBackgroundColor))
        }
        .navigationSplitViewStyle(.balanced)
        .task(id: model.statusUpdatedAt) {
            await model.refreshRuntimeData()
        }
    }

    @ViewBuilder private var pageContent: some View {
        switch model.selection {
        case .catalog(let section): CatalogSectionListView(model: model, section: section)
        case .tool(let name): ToolCatalogDetailView(model: model, toolName: name)
        case .skill(let reference): SkillCatalogDetailView(model: model, skillRef: reference)
        case .commonSkill(let reference): CommonSkillCatalogDetailView(model: model, skillRef: reference)
        case .rule(let path): RuleCatalogDetailView(model: model, rulePath: path)
        case .plugin(let name): PluginCatalogDetailView(model: model, pluginName: name)
        default: pageView
        }
    }

    @ViewBuilder private var pageView: some View {
        switch model.page {
        case .home: HomeView(model: model)
        case .settings: SettingsView(model: model)
        case .connections: ConnectionsView(model: model)
        case .capabilities: CapabilitiesView(model: model)
        case .tasks: TasksView(model: model)
        case .files: FilesView(model: model)
        case .published: PublishedView(model: model)
        case .traces: TracesView(model: model)
        case .analytics: AnalyticsView(model: model)
        case .activity: ActivityView(model: model)
        }
    }
}

private struct PageHeader: View {
    let title: String
    var detail: String? = nil
    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(title).font(.system(size: 20, weight: .semibold))
            if let detail { Text(detail).font(.system(size: 12.5)).foregroundStyle(.secondary) }
        }
    }
}

private struct StatusPill: View {
    let text: String
    let active: Bool
    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(active ? Color.green : Color.secondary).frame(width: 7, height: 7)
            Text(text).font(.system(size: 12, weight: .medium))
        }
        .foregroundStyle(active ? .primary : .secondary)
    }
}

private struct SettingsSection<Content: View>: View {
    let title: String
    @ViewBuilder let content: Content
    init(_ title: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.content = content()
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.system(size: 12, weight: .semibold)).foregroundStyle(.secondary)
            SettingsCard { content }
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct SettingsCard<Content: View>: View {
    @ViewBuilder let content: Content
    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }
    var body: some View {
        VStack(spacing: 0) { content }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(nsColor: .controlBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(Color.primary.opacity(0.08), lineWidth: 1))
    }
}

private struct SettingsRow<Trailing: View>: View {
    let title: String
    var detail: String? = nil
    @ViewBuilder let trailing: Trailing
    init(_ title: String, detail: String? = nil, @ViewBuilder trailing: () -> Trailing) {
        self.title = title; self.detail = detail; self.trailing = trailing()
    }
    var body: some View {
        GeometryReader { proxy in
            HStack(spacing: 16) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.system(size: 13))
                    if let detail {
                        Text(detail)
                            .font(.system(size: 11.5))
                            .foregroundStyle(.secondary)
                    }
                }

                Spacer(minLength: 20)

                trailing
                    .fixedSize(horizontal: true, vertical: false)
            }
            .frame(width: max(0, proxy.size.width - 26), height: proxy.size.height)
            .padding(.horizontal, 13)
        }
        .frame(height: detail == nil ? 44 : 52)
    }
}

private struct RowDivider: View {
    var body: some View { Divider().padding(.leading, 13) }
}

private enum HomeCapabilityState {
    case enabled
    case disabled
    case attention

    var symbol: String {
        switch self {
        case .enabled: return "circle.fill"
        case .disabled: return "circle"
        case .attention: return "exclamationmark.circle.fill"
        }
    }

    var color: Color {
        switch self {
        case .enabled: return .green
        case .disabled: return .secondary
        case .attention: return .orange
        }
    }

    var text: String {
        switch self {
        case .enabled: return L10n.text("Enabled")
        case .disabled: return L10n.text("Disabled")
        case .attention: return L10n.text("Needs attention")
        }
    }
}

private struct HomeCapabilityItem {
    let title: String
    let detail: String
    let state: HomeCapabilityState
}

private struct HomeMetric: View {
    let title: String
    let value: String
    let detail: String

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.system(size: 11.5))
                .foregroundStyle(.secondary)
            Text(value)
                .font(.system(size: 20, weight: .semibold))
            Text(detail)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct HomeCapabilityRow: View {
    let item: HomeCapabilityItem

    var body: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.title).font(.system(size: 13))
                Text(item.detail).font(.system(size: 11.5)).foregroundStyle(.secondary)
            }
            Spacer(minLength: 20)
            HStack(spacing: 9) {
                HStack(spacing: 6) {
                    Image(systemName: item.state.symbol)
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundStyle(item.state.color)
                    Text(item.state.text)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(item.state == .disabled ? .secondary : .primary)
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 13)
        .frame(minHeight: 56)
        .contentShape(Rectangle())
    }
}

private struct HomeRecentActivityRow: View {
    let call: RuntimeDiagnosticCall
    let source: String
    let time: String

    var body: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 2) {
                Text(call.tool)
                    .font(.system(size: 13, weight: .medium))
                Text("\(source) · \(time)")
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 20)
            HStack(spacing: 6) {
                Image(systemName: call.success ? "circle.fill" : "exclamationmark.circle.fill")
                    .font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(call.success ? Color.green : Color.orange)
                Text(call.success ? L10n.text("Succeeded") : L10n.text("Failed"))
                    .font(.system(size: 12, weight: .medium))
            }
        }
        .padding(.horizontal, 13)
        .frame(minHeight: 54)
    }
}

private struct HomeView: View {
    @ObservedObject var model: ControlPanelModel

    private static let fractionalDateFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let basicDateFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    private var serviceLoaded: Bool { model.status.loaded }
    private var serviceHealthy: Bool { model.status.loaded && model.status.healthy }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                PageHeader(title: L10n.text("Home"))

                HStack(alignment: .center, spacing: 16) {
                    AgentDockLogoView(size: 48)

                    VStack(alignment: .leading, spacing: 5) {
                        HStack(spacing: 8) {
                            Text("AgentDock")
                                .font(.system(size: 18, weight: .semibold))
                            if let version = model.status.version, !version.isEmpty {
                                Text(version.hasPrefix("v") ? version : "v\(version)")
                                    .font(.system(size: 11.5))
                                    .foregroundStyle(.secondary)
                            }
                        }
                        Text(agentDockDetailText)
                            .font(.system(size: 13))
                            .foregroundStyle(.secondary)
                        if let message = model.message {
                            Text(message).font(.system(size: 12)).foregroundStyle(.red)
                        } else if let notice = model.notice {
                            Text(notice).font(.system(size: 12)).foregroundStyle(.secondary)
                        }
                    }

                    Spacer()

                    HStack(spacing: 10) {
                        Button(serviceLoaded ? L10n.text("Stop") : L10n.text("Start")) { model.toggleRuntime() }
                            .controlSize(.small)
                            .disabled(model.isBusy || model.updateActivity.locksApplication)
                        StatusPill(text: agentDockStatusText, active: serviceHealthy)
                    }
                }
                .padding(.horizontal, 18)
                .padding(.vertical, 16)
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .stroke(Color.primary.opacity(0.08), lineWidth: 1)
                )

                HStack(spacing: 0) {
                    Button {
                        model.page = .connections
                    } label: {
                        HomeMetric(
                            title: L10n.text("Remote connection"),
                            value: nexusText,
                            detail: remoteConnectionDetail
                        )
                        .frame(minWidth: 180, maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)

                    metricDivider

                    HomeMetric(
                        title: L10n.text("Skills"),
                        value: dashboardCount(model.dashboard.skillCount),
                        detail: L10n.text("Installed")
                    )
                    .frame(width: 120)

                    metricDivider

                    HomeMetric(
                        title: "MCP",
                        value: dashboardCount(model.dashboard.mcpCount),
                        detail: L10n.text("Configured")
                    )
                    .frame(width: 120)

                    metricDivider

                    HomeMetric(
                        title: L10n.text("Plugins"),
                        value: dashboardCount(model.dashboard.pluginCount),
                        detail: L10n.text("Installed")
                    )
                    .frame(width: 120)
                }
                .padding(.horizontal, 18)
                .padding(.vertical, 12)
                .background(Color(nsColor: .controlBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.primary.opacity(0.08), lineWidth: 1)
                )

                SettingsSection(L10n.text("Core capabilities")) {
                    ForEach(Array(capabilityItems.enumerated()), id: \.offset) { index, item in
                        if index > 0 { RowDivider() }
                        Button {
                            model.page = .capabilities
                        } label: {
                            HomeCapabilityRow(item: item)
                        }
                        .buttonStyle(.plain)
                    }
                }

                SettingsSection(L10n.text("Recent activity")) {
                    if !model.dashboard.diagnosticsAvailable {
                        emptyActivity(L10n.text("Recent activity is unavailable."))
                    } else if model.dashboard.recentCalls.isEmpty {
                        emptyActivity(L10n.text("No recent activity yet."))
                    } else {
                        ForEach(Array(model.dashboard.recentCalls.prefix(3).enumerated()), id: \.element.id) { index, call in
                            if index > 0 { RowDivider() }
                            Button {
                                model.page = .activity
                            } label: {
                                HomeRecentActivityRow(
                                    call: call,
                                    source: activitySource(call.source),
                                    time: relativeTime(call.startedAt)
                                )
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
            .padding(.horizontal, 28)
            .padding(.top, 16)
            .padding(.bottom, 32)
            .frame(maxWidth: 840, alignment: .leading)
        }
        .task(id: model.statusUpdatedAt) {
            await model.refreshDashboard()
        }
    }

    @ViewBuilder
    private var metricDivider: some View {
        Divider()
            .frame(height: 56)
            .padding(.horizontal, 18)
    }

    @ViewBuilder
    private func emptyActivity(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 12.5))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 13)
            .frame(minHeight: 52, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func dashboardCount(_ value: Int) -> String {
        model.dashboard.countsAvailable ? String(value) : "—"
    }

    private var agentDockStatusText: String {
        if !serviceLoaded { return L10n.text("Stopped") }
        return serviceHealthy ? L10n.text("Running") : L10n.text("Needs attention")
    }

    private var agentDockDetailText: String {
        if !serviceLoaded { return L10n.text("Start AgentDock to accept AI client connections.") }
        return serviceHealthy
            ? L10n.text("This device is ready for AI.")
            : L10n.text("AgentDock is running, but the connection service is not ready.")
    }

    private var nexusText: String {
        switch model.status.nexusConnection {
        case .connected: return L10n.text("Connected")
        case .disconnected: return L10n.text("Offline")
        case .configurationError: return L10n.text("Unavailable")
        case .unconfigured: return L10n.text("Not configured")
        }
    }

    private var remoteConnectionDetail: String {
        let device = model.nexusDevice
        if device.error != nil { return L10n.text("Unavailable") }
        guard device.paired else { return L10n.text("Not configured") }
        let endpoint = device.endpoint.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        if endpoint.caseInsensitiveCompare("https://mcp.nexusdock.co") == .orderedSame {
            return "\(L10n.text("Official service")) · nexusdock.co"
        }
        return "\(L10n.text("Self-hosted service")) · \(device.endpoint)"
    }

    private var capabilityItems: [HomeCapabilityItem] {
        guard let configuration = model.status.configuration else {
            return [
                HomeCapabilityItem(title: L10n.text("Browser"), detail: L10n.text("Not enabled yet"), state: .disabled),
                HomeCapabilityItem(title: L10n.text("Coding Agent"), detail: L10n.text("Not enabled yet"), state: .disabled),
                HomeCapabilityItem(title: L10n.text("MCP Apps"), detail: L10n.text("Off"), state: .disabled),
            ]
        }

        let browserDetail: String
        if !configuration.browserEnabled {
            browserDetail = L10n.text("Not enabled yet")
        } else if !configuration.browserCDPURL.isEmpty {
            browserDetail = L10n.text("Use the configured CDP browser")
        } else if configuration.browserReuseExistingCDP {
            browserDetail = L10n.text("Prefer an existing local browser")
        } else {
            browserDetail = L10n.text("Use an isolated browser")
        }

        let enabledProfiles = configuration.acpProfiles.filter(\.enabled)
        let codingAgentState: HomeCapabilityState
        let codingAgentDetail: String
        if !configuration.acpEnabled {
            codingAgentState = .disabled
            codingAgentDetail = L10n.text("Not enabled yet")
        } else if let defaultProfile = enabledProfiles.first(where: { $0.id == configuration.acpDefaultProfile }) {
            codingAgentState = .enabled
            let name = defaultProfile.displayName?.trimmingCharacters(in: .whitespacesAndNewlines)
            codingAgentDetail = L10n.format("%@ · default", (name?.isEmpty == false ? name! : defaultProfile.id))
        } else {
            codingAgentState = .attention
            codingAgentDetail = L10n.text("Needs attention")
        }

        let mcpAppsState: HomeCapabilityState = configuration.mcpAppsMode == .off ? .disabled : .enabled
        let mcpAppsDetail: String
        switch configuration.mcpAppsMode {
        case .full: mcpAppsDetail = L10n.text("Full")
        case .compact: mcpAppsDetail = L10n.text("Compact")
        case .off: mcpAppsDetail = L10n.text("Off")
        }

        return [
            HomeCapabilityItem(
                title: L10n.text("Browser"),
                detail: browserDetail,
                state: configuration.browserEnabled ? .enabled : .disabled
            ),
            HomeCapabilityItem(
                title: L10n.text("Coding Agent"),
                detail: codingAgentDetail,
                state: codingAgentState
            ),
            HomeCapabilityItem(
                title: L10n.text("MCP Apps"),
                detail: mcpAppsDetail,
                state: mcpAppsState
            ),
        ]
    }

    private func activitySource(_ source: String) -> String {
        switch source.lowercased() {
        case "nexus": return L10n.text("Remote")
        case "internal": return L10n.text("Local")
        case "mcp": return "MCP"
        default: return source
        }
    }

    private func relativeTime(_ rawDate: String) -> String {
        guard let date = Self.fractionalDateFormatter.date(from: rawDate)
                ?? Self.basicDateFormatter.date(from: rawDate) else {
            return rawDate
        }
        let elapsed = max(0, Date().timeIntervalSince(date))
        if elapsed < 60 { return L10n.text("Just now") }
        if elapsed < 3600 { return L10n.format("%d min ago", max(1, Int(elapsed / 60))) }
        if elapsed < 86400 { return L10n.format("%d hr ago", max(1, Int(elapsed / 3600))) }
        return DateFormatter.localizedString(from: date, dateStyle: .short, timeStyle: .short)
    }
}

private struct ConnectionsView: View {
    private enum RemoteServiceChoice: String, CaseIterable {
        case official
        case selfHosted
    }

    private static let officialEndpoint = "https://mcp.nexusdock.co"
    private static let officialDevicesURL = URL(string: "https://mcp.nexusdock.co/workspace/devices")!

    @ObservedObject var model: ControlPanelModel
    @State private var pairingCode = ""
    @State private var rePairing = false
    @State private var remoteService: RemoteServiceChoice = .official
    @State private var customEndpoint = ""
    @State private var editingRemoteService = false
    @State private var connectionInitialized = false

    private var nexusDevice: NexusDeviceStatus { model.nexusDevice }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                PageHeader(
                    title: L10n.text("Connections"),
                    detail: L10n.text("Connect AI clients to this device remotely.")
                )

                SettingsSection(L10n.text("Remote connection")) {
                    SettingsRow(L10n.text("Status")) {
                        StatusPill(text: connectionText, active: connectionActive)
                    }
                    RowDivider()
                    SettingsRow(L10n.text("Remote service"), detail: remoteServiceDetail) {
                        Button(editingRemoteService ? L10n.text("Done") : L10n.text("Change")) {
                            editingRemoteService.toggle()
                        }
                        .controlSize(.small)
                    }

                    if editingRemoteService {
                        RowDivider()
                        VStack(alignment: .leading, spacing: 9) {
                            Picker(L10n.text("Remote service"), selection: $remoteService) {
                                Text(L10n.text("Official service")).tag(RemoteServiceChoice.official)
                                Text(L10n.text("Self-hosted service")).tag(RemoteServiceChoice.selfHosted)
                            }
                            .labelsHidden()
                            .pickerStyle(.segmented)

                            if remoteService == .selfHosted {
                                TextField(L10n.text("Service address"), text: $customEndpoint)
                                    .textFieldStyle(.roundedBorder)
                            } else {
                                Text(L10n.text("Official service uses nexusdock.co by default."))
                                    .font(.system(size: 12))
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .padding(13)
                    }

                    RowDivider()
                    VStack(alignment: .leading, spacing: 10) {
                        if nexusDevice.paired && !rePairing {
                            if remoteService == .official {
                                Link(
                                    L10n.text("Manage connected devices ↗"),
                                    destination: Self.officialDevicesURL
                                )
                                .font(.system(size: 12))
                            }
                            Button(L10n.text("Re-pair")) {
                                pairingCode = ""
                                rePairing = true
                            }
                            .frame(maxWidth: .infinity, alignment: .trailing)
                        } else {
                            Text(L10n.text("Enter the pairing code to connect this device."))
                                .font(.system(size: 12))
                                .foregroundStyle(.secondary)
                            SecureField(L10n.text("Pairing code"), text: $pairingCode)
                                .textFieldStyle(.roundedBorder)
                            if remoteService == .official {
                                Link(
                                    L10n.text("No pairing code? Get one from NexusDock ↗"),
                                    destination: Self.officialDevicesURL
                                )
                                .font(.system(size: 12))
                            }
                            Button(L10n.text("Connect")) {
                                Task {
                                    if await model.pairNexus(endpoint: selectedEndpoint, code: pairingCode) {
                                        pairingCode = ""
                                        rePairing = false
                                        loadConnection()
                                    }
                                }
                            }
                            .disabled(model.isBusy || selectedEndpoint.isEmpty || pairingCode.isEmpty)
                            .frame(maxWidth: .infinity, alignment: .trailing)
                        }
                    }
                    .padding(13)
                }

                SettingsSection(L10n.text("Advanced connection settings")) {
                    Button {
                        model.settingsPage = .advancedConnection
                        model.page = .settings
                    } label: {
                        HStack(spacing: 12) {
                            VStack(alignment: .leading, spacing: 3) {
                                Text(L10n.text("Advanced connection settings"))
                                    .font(.system(size: 13, weight: .medium))
                                Text(L10n.text("Local MCP, access credentials, and direct public access."))
                                    .font(.system(size: 12))
                                    .foregroundStyle(.secondary)
                            }
                            Spacer(minLength: 20)
                            Image(systemName: "chevron.right")
                                .foregroundStyle(.tertiary)
                        }
                        .padding(.horizontal, 13)
                        .frame(minHeight: 52)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }

                if let message = model.message {
                    Text(message)
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 28)
            .padding(.top, 16)
            .padding(.bottom, 32)
            .frame(maxWidth: 760, alignment: .leading)
        }
        .task(id: model.statusUpdatedAt) {
            guard !connectionInitialized else { return }
            loadConnection()
        }
    }

    private func loadConnection() {
        let endpoint = nexusDevice.paired ? nexusDevice.endpoint : Self.officialEndpoint
        if isOfficialEndpoint(endpoint) {
            remoteService = .official
            customEndpoint = ""
        } else {
            remoteService = .selfHosted
            customEndpoint = endpoint
        }
        connectionInitialized = true
    }

    private var selectedEndpoint: String {
        switch remoteService {
        case .official:
            return Self.officialEndpoint
        case .selfHosted:
            return customEndpoint.trimmingCharacters(in: .whitespacesAndNewlines)
        }
    }

    private var remoteServiceDetail: String {
        switch remoteService {
        case .official:
            return "\(L10n.text("Official service")) · nexusdock.co"
        case .selfHosted:
            let endpoint = customEndpoint.trimmingCharacters(in: .whitespacesAndNewlines)
            return endpoint.isEmpty ? L10n.text("Self-hosted service") : "\(L10n.text("Self-hosted service")) · \(endpoint)"
        }
    }

    private var connectionActive: Bool {
        if case .connected = model.status.nexusConnection { return true }
        return false
    }

    private var connectionText: String {
        switch model.status.nexusConnection {
        case .connected: return L10n.text("Connected")
        case .disconnected: return L10n.text("Disconnected")
        case .configurationError: return L10n.text("Unavailable")
        case .unconfigured: return L10n.text("Not configured")
        }
    }


    private func isOfficialEndpoint(_ endpoint: String) -> Bool {
        endpoint.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            .caseInsensitiveCompare(Self.officialEndpoint) == .orderedSame

    }
}

private struct CapabilitiesView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var browserEnabled = false
    @State private var browserMode = 0
    @State private var browserCDPURL = ""
    @State private var acpEnabled = false
    @State private var profiles: [ACPProfileConfiguration] = []
    @State private var defaultProfile = ""
    @State private var selectedMCPAppsMode: MCPAppsMode = .full
    @State private var customName = ""
    @State private var customCommand = ""
    @State private var customArguments = ""
    @State private var configurationInitialized = false

    private var configuration: ServiceConfiguration? { model.status.configuration }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                PageHeader(title: L10n.text("Capabilities"), detail: L10n.text("Configure the capabilities AgentDock provides to AI."))
                // 目录条目进二级可搜索列表：数量一览无余，但不会把侧栏铺满。
                SettingsSection(L10n.text("Capability catalog")) {
                    ForEach(ControlPanelModel.CatalogSection.allCases) { section in
                        Button {
                            model.selection = .catalog(section)
                        } label: {
                            HStack(spacing: 10) {
                                Image(systemName: section.symbol)
                                    .frame(width: 18, alignment: .leading)
                                    .foregroundStyle(.secondary)
                                Text(section.title)
                                Spacer(minLength: 12)
                                Text("\(model.catalogEntryCount(for: section))")
                                    .font(.system(size: 12))
                                    .foregroundStyle(.secondary)
                                Image(systemName: "chevron.right")
                                    .font(.system(size: 11, weight: .semibold))
                                    .foregroundStyle(.tertiary)
                            }
                            .padding(.horizontal, 13)
                            .padding(.vertical, 10)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        if section != .plugins {
                            RowDivider()
                        }
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    Text(L10n.text("Built-in capabilities"))
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(.secondary)

                    SettingsCard {
                        SettingsRow(L10n.text("Browser"), detail: L10n.text("Browser automation and web operations")) {
                            Toggle(L10n.text("Browser"), isOn: $browserEnabled)
                                .labelsHidden()
                                .toggleStyle(.switch)
                        }
                        VStack(alignment: .leading, spacing: 8) {
                            Picker(L10n.text("Browser connection"), selection: $browserMode) {
                                Text(L10n.text("Isolated browser")).tag(0)
                                Text(L10n.text("Reuse local browser")).tag(1)
                                Text(L10n.text("Specified CDP")).tag(2)
                            }
                            .pickerStyle(.segmented)

                            TextField("http://127.0.0.1:9222", text: $browserCDPURL)
                                .textFieldStyle(.roundedBorder)
                                .opacity(browserMode == 2 ? 1 : 0)
                                .disabled(browserMode != 2)
                                .allowsHitTesting(browserMode == 2)
                                .accessibilityHidden(browserMode != 2)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 13)
                        .padding(.bottom, 12)
                    }

                    SettingsCard {
                        SettingsRow(L10n.text("Coding Agent"), detail: codingAgentDetail) {
                            Toggle(L10n.text("Coding Agent"), isOn: $acpEnabled)
                                .labelsHidden()
                                .toggleStyle(.switch)
                        }
                        VStack(alignment: .leading, spacing: 8) {
                            if !enabledProfiles.isEmpty {
                                Picker(L10n.text("Default Coding Agent"), selection: $defaultProfile) {
                                    ForEach(enabledProfiles, id: \.id) { profile in
                                        Text(profileTitle(profile)).tag(profile.id)
                                    }
                                }
                                .frame(width: 260, alignment: .leading)
                                .frame(maxWidth: .infinity, alignment: .leading)
                            }

                            VStack(alignment: .leading, spacing: 8) {
                                ForEach(profiles.indices, id: \.self) { index in
                                    HStack(spacing: 0) {
                                        Toggle(profileTitle(profiles[index]), isOn: $profiles[index].enabled)
                                            .labelsHidden()
                                            .toggleStyle(.checkbox)
                                            .frame(width: 18, alignment: .leading)
                                        Text(profileTitle(profiles[index]))
                                            .padding(.leading, 22)
                                            .frame(maxWidth: .infinity, alignment: .leading)
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                }
                            }
                            .frame(width: 260, alignment: .leading)
                            .frame(maxWidth: .infinity, alignment: .leading)

                            DisclosureGroup(L10n.text("Add custom Coding Agent")) {
                                VStack(alignment: .leading, spacing: 8) {
                                    TextField(L10n.text("Name"), text: $customName).textFieldStyle(.roundedBorder)
                                    TextField(L10n.text("Command"), text: $customCommand).textFieldStyle(.roundedBorder)
                                    TextField(L10n.text("Args JSON"), text: $customArguments).textFieldStyle(.roundedBorder)
                                    Button(L10n.text("Add")) { addCustomProfile() }
                                        .disabled(customName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                                        .frame(maxWidth: .infinity, alignment: .trailing)
                                }.padding(.top, 8)
                            }
                            .frame(width: 260, alignment: .leading)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 13)
                        .padding(.bottom, 12)
                    }

                    SettingsCard {
                        SettingsRow(L10n.text("MCP Apps"), detail: L10n.text("Interactive MCP app presentation")) {
                            Picker(L10n.text("MCP Apps"), selection: $selectedMCPAppsMode) {
                                Text(L10n.text("Full")).tag(MCPAppsMode.full)
                                Text(L10n.text("Compact")).tag(MCPAppsMode.compact)
                                Text(L10n.text("Off")).tag(MCPAppsMode.off)
                            }.labelsHidden().frame(width: 120, alignment: .trailing)
                        }
                    }
                }

                Button(L10n.text("Save")) {
                    let settings = EditableServiceSettings(
                        port: configuration?.port ?? 8765,
                        logLevel: configuration?.logLevel ?? "info",
                        mcpAppsMode: selectedMCPAppsMode,
                        browserEnabled: browserEnabled,
                        browserCDPURL: browserMode == 2 ? browserCDPURL : "",
                        browserReuseExistingCDP: browserMode == 1,
                        acpEnabled: acpEnabled,
                        acpProfiles: profiles,
                        acpDefaultProfile: defaultProfile,
                        traceMode: configuration?.traceMode ?? .off,
                        traceRetentionDays: configuration?.traceRetentionDays ?? 3
                    )
                    Task {
                        if await model.applySettings(settings) { loadConfiguration() }
                    }
                }
                .disabled(model.isBusy)
                .frame(maxWidth: .infinity, alignment: .trailing)
                if let message = model.message { Text(message).font(.system(size: 12)).foregroundStyle(.secondary) }

            }
            .padding(.horizontal, 28).padding(.top, 16).padding(.bottom, 32)
            .frame(maxWidth: 760, alignment: .leading)
        }
        .task(id: model.statusUpdatedAt) {
            if !configurationInitialized { loadConfiguration() }
        }
        .onChange(of: profiles.map(\.enabled)) { _ in
            if !enabledProfiles.contains(where: { $0.id == defaultProfile }) {
                defaultProfile = enabledProfiles.first?.id ?? ""
            }
        }
    }

    private var enabledProfiles: [ACPProfileConfiguration] { profiles.filter(\.enabled) }
    private var codingAgentDetail: String {
        let enabledCount = profiles.filter(\.enabled).count
        guard enabledCount > 0 else { return L10n.text("No profiles configured") }
        let defaultTitle = profiles.first(where: { $0.id == defaultProfile }).map(profileTitle) ?? defaultProfile
        return L10n.format("%d enabled · default %@", enabledCount, defaultTitle)
    }
    private func profileTitle(_ profile: ACPProfileConfiguration) -> String {
        if let displayName = profile.displayName, !displayName.isEmpty {
            return displayName
        }
        if profile.kind != .custom {
            return profile.kind.title
        }
        return profile.id
    }
    private func loadConfiguration() {
        guard let configuration else { return }
        browserEnabled = configuration.browserEnabled
        browserCDPURL = configuration.browserCDPURL
        browserMode = !configuration.browserCDPURL.isEmpty ? 2 : (configuration.browserReuseExistingCDP ? 1 : 0)
        acpEnabled = configuration.acpEnabled
        profiles = configuration.acpProfiles
        for preset in [ACPAgentPreset.codex, .claude, .grok] where !profiles.contains(where: { $0.id == preset.rawValue }) {
            profiles.append(ACPProfileConfiguration(
                id: preset.rawValue,
                kind: preset,
                command: "",
                args: [],
                envFromEnv: nil,
                enabled: false
            ))
        }
        defaultProfile = configuration.acpDefaultProfile
        selectedMCPAppsMode = configuration.mcpAppsMode
        configurationInitialized = true
    }

    private func addCustomProfile() {
        let name = customName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return }
        let id = ACPDesktopConfiguration.customProfileID(name: name, existingIDs: profiles.map(\.id))
        let args = (try? ACPDesktopConfiguration.decodeArguments(customArguments)) ?? []
        profiles.append(ACPProfileConfiguration(
            id: id,
            displayName: name,
            kind: .custom,
            command: customCommand.trimmingCharacters(in: .whitespacesAndNewlines),
            args: args,
            envFromEnv: nil,
            enabled: true
        ))
        if defaultProfile.isEmpty { defaultProfile = id }
        customName = ""; customCommand = ""; customArguments = ""
    }
}

private struct RuntimeActivityCallRow: View {
    let call: RuntimeAnalyticsCall
    @State private var isExpanded = false

    private var hasDetails: Bool {
        !(call.errorCode?.isEmpty ?? true) || !call.stages.isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if hasDetails {
                Button {
                    withAnimation(.easeInOut(duration: 0.16)) {
                        isExpanded.toggle()
                    }
                } label: {
                    callLabel
                }
                .buttonStyle(.plain)
            } else {
                callLabel
            }

            if hasDetails && isExpanded {
                callDetails
                    .padding(.top, 8)
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 13)
        .padding(.vertical, 8)
    }

    private var callLabel: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(call.tool)
                        .font(.system(size: 13, weight: .medium))
                    if hasDetails {
                        Image(systemName: isExpanded ? "chevron.down" : "chevron.right")
                            .font(.system(size: 9.5, weight: .semibold))
                            .foregroundStyle(.secondary)
                    }
                }
                HStack(spacing: 6) {
                    Text("\(RuntimeActivityFormat.source(call.source)) · \(RuntimeActivityFormat.relativeTime(call.startedAt))")
                    if let userAgent = call.userAgent, !userAgent.isEmpty {
                        Text("· \(userAgent)")
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .help(userAgent)
                    }
                }
                .font(.system(size: 11.5))
                .foregroundStyle(.secondary)
            }
            Spacer(minLength: 16)
            Text(RuntimeActivityFormat.duration(call.durationMS))
                .font(.system(size: 11.5, design: .monospaced))
                .foregroundStyle(.secondary)
            HStack(spacing: 6) {
                Circle().fill(call.success ? Color.green : Color.orange).frame(width: 7, height: 7)
                Text(call.success ? L10n.text("Succeeded") : L10n.text("Failed"))
                    .font(.system(size: 12, weight: .medium))
            }
        }
        .contentShape(Rectangle())
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var callDetails: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let errorCode = call.errorCode, !errorCode.isEmpty {
                Text("\(L10n.text("Error code")): \(errorCode)")
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
            }
            ForEach(call.stages) { stage in
                HStack(spacing: 10) {
                    Text(RuntimeActivityFormat.stage(stage.name))
                    Spacer()
                    Text(RuntimeActivityFormat.duration(stage.durationMS))
                        .foregroundStyle(.secondary)
                    Image(systemName: stage.success ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                        .foregroundStyle(stage.success ? Color.green : Color.orange)
                }
                .font(.system(size: 11.5))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct ActivityView: View {
    private static let pageSize = 20

    @ObservedObject var model: ControlPanelModel
    @State private var analytics: RuntimeAnalyticsPayload?
    @State private var visibleCallCount = pageSize
    @State private var query = ""
    @State private var autoRefresh = true

    private var running: Bool { model.status.loaded && model.status.healthy }

    private var filteredCalls: [RuntimeAnalyticsCall] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return (analytics?.recentCalls ?? []).filter { call in
            needle.isEmpty || call.tool.lowercased().contains(needle) || call.source.lowercased().contains(needle)
        }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                HStack(alignment: .top, spacing: 10) {
                    PageHeader(
                        title: L10n.text("Activity"),
                        detail: L10n.text("Recent tool calls and execution details.")
                    )
                    Spacer(minLength: 12)
                    SearchField(placeholder: L10n.text("Search tool or source"), text: $query)
                    AutoRefreshToggle(enabled: $autoRefresh)
                }

                SettingsSection(L10n.text("Recent calls")) {
                    Text(
                        L10n.text(
                            "Only tool name, source, latency, stages, status, and error code are retained. Arguments, results, commands, and file contents are never stored."
                        )
                    )
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 13)
                    .padding(.vertical, 10)

                    if analytics != nil {
                        let calls = filteredCalls
                        let visibleCalls = Array(calls.prefix(visibleCallCount))
                        if !visibleCalls.isEmpty { RowDivider() }
                        ForEach(Array(visibleCalls.enumerated()), id: \.element.id) { index, call in
                            if index > 0 { RowDivider() }
                            RuntimeActivityCallRow(call: call)
                                .onAppear {
                                    // 滚动到末行自动加载下一批。
                                    if index == visibleCalls.count - 1 {
                                        visibleCallCount += Self.pageSize
                                    }
                                }
                        }
                        if calls.isEmpty {
                            emptyAnalytics(L10n.text("No call data yet."))
                        } else if visibleCallCount < calls.count {
                            RowDivider()
                            Button(L10n.text("Show more")) {
                                visibleCallCount += Self.pageSize
                            }
                            .buttonStyle(.plain)
                            .font(.system(size: 12.5, weight: .medium))
                            .foregroundStyle(.tint)
                            .padding(.horizontal, 13)
                            .padding(.vertical, 12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    } else {
                        RowDivider()
                        emptyAnalytics(
                            running
                                ? L10n.text("Runtime analytics are unavailable.")
                                : L10n.text("Start AgentDock to view runtime activity.")
                        )
                    }
                }
            }
            .padding(.horizontal, 28)
            .padding(.top, 16)
            .padding(.bottom, 32)
            .frame(maxWidth: 760, alignment: .leading)
        }
        .task { await refreshAnalytics() }
        .autoRefresh(enabled: autoRefresh) { await refreshAnalytics() }
        .onChange(of: query) { _ in visibleCallCount = Self.pageSize }
    }

    /// 只在列表头部变化时替换数据：自动刷新不能把用户正要点击的行换掉。
    private func refreshAnalytics() async {
        guard running else {
            if analytics != nil { analytics = nil }
            return
        }
        let nextAnalytics = await model.service.runtimeAnalytics(configuration: model.status.configuration)
        let currentLatestID = analytics?.recentCalls.first?.id
        let nextLatestID = nextAnalytics?.recentCalls.first?.id
        if analytics == nil || nextLatestID != currentLatestID {
            analytics = nextAnalytics
        }
    }

    private func emptyAnalytics(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 12.5))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 13)
            .padding(.vertical, 14)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// 追踪是运行期可观测性开关，与浏览器/ACP 这类能力开关分开：单独一页，只提交追踪字段。
private struct SettingsTracesView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var traceMode: TraceMode = .off
    @State private var traceRetentionDays = 3
    @State private var initialized = false

    private var configuration: ServiceConfiguration? { model.status.configuration }

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            PageHeader(
                title: L10n.text("Call tracing"),
                detail: L10n.text("Record tool arguments and results for debugging.")
            )

            SettingsSection(L10n.text("Call tracing")) {
                SettingsRow(L10n.text("Call tracing"), detail: L10n.text("Record tool arguments and results for debugging.")) {
                    Picker(L10n.text("Call tracing"), selection: $traceMode) {
                        ForEach(TraceMode.allCases) { mode in
                            Text(mode.title).tag(mode)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 160, alignment: .trailing)
                }
                if traceMode != .off {
                    RowDivider()
                    SettingsRow(L10n.text("Keep traces"), detail: L10n.text("Older trace files are deleted after this window.")) {
                        Stepper(value: $traceRetentionDays, in: 1...30) {
                            Text(L10n.format("%d days", traceRetentionDays))
                                .monospacedDigit()
                        }
                        .frame(width: 120, alignment: .trailing)
                    }
                }
            }

            Button(L10n.text("Save")) {
                Task {
                    if await model.applySettings(editableSettings()) {
                        loadConfiguration()
                    }
                }
            }
            .disabled(model.isBusy)
            .frame(maxWidth: .infinity, alignment: .trailing)

            if let message = model.message {
                Text(message).font(.system(size: 12)).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: 650, alignment: .leading)
        .task(id: model.statusUpdatedAt) {
            if !initialized { loadConfiguration() }
        }
    }

    private func loadConfiguration() {
        guard let configuration else { return }
        traceMode = configuration.traceMode
        traceRetentionDays = configuration.traceRetentionDays
        initialized = true
    }

    /// 只改追踪字段，其余配置从当前环境回填，避免覆盖用户在其他页面的修改。
    private func editableSettings() -> EditableServiceSettings {
        let current = configuration
        return EditableServiceSettings(
            port: current?.port ?? 8765,
            logLevel: current?.logLevel ?? "info",
            mcpAppsMode: current?.mcpAppsMode ?? .full,
            browserEnabled: current?.browserEnabled ?? false,
            browserCDPURL: current?.browserCDPURL ?? "",
            browserReuseExistingCDP: current?.browserReuseExistingCDP ?? false,
            acpEnabled: current?.acpEnabled ?? false,
            acpProfiles: current?.acpProfiles ?? [],
            acpDefaultProfile: current?.acpDefaultProfile ?? "",
            traceMode: traceMode,
            traceRetentionDays: traceRetentionDays
        )
    }
}

private struct SettingsView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var port = 8765
    @State private var logLevel = "info"
    @State private var analytics: RuntimeAnalyticsPayload?
    @State private var showAdvancedDiagnostics = false
    @State private var showCustomPort = false
    @State private var languagePreference: UILanguagePreference = .system
    @State private var themePreference: UIThemePreference = .system
    @State private var coreAutostart = false
    @State private var menuAutostart = false
    @State private var tunnelMode: TunnelMode = .local
    @State private var serverURL = ""
    @State private var tunnelToken = ""
    @State private var publicEndpointCheckResult: PublicEndpointCheckResult?
    @State private var testedPublicMCPURL: URL?
    @State private var isTestingPublicEndpoint = false
    @State private var connectionDraftInitialized = false

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            PageHeader(title: L10n.text("Settings"))
                .padding(.horizontal, 28).padding(.top, 16)

            HStack(alignment: .top, spacing: 0) {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(ControlPanelModel.SettingsPage.allCases) { item in
                        Button {
                            model.settingsPage = item
                        } label: {
                            Text(item.title)
                                .font(.system(size: 13, weight: model.settingsPage == item ? .medium : .regular))
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.leading, 18)
                                .padding(.trailing, 10)
                                .frame(height: 32)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(.primary)
                        .background(
                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                .fill(
                                    model.settingsPage == item
                                        ? Color.accentColor.opacity(0.14)
                                        : Color.clear
                                )
                        )
                    }
                    Spacer(minLength: 0)
                }
                .id(model.languageRevision)
                .padding(.horizontal, 10)
                .padding(.top, 4)
                .frame(width: 190, alignment: .topLeading)
                .frame(maxHeight: .infinity, alignment: .topLeading)

                Divider()

                ScrollView {
                    settingsContent
                        .padding(.horizontal, 28).padding(.vertical, 4).padding(.bottom, 32)
                        .frame(maxWidth: 650, alignment: .leading)
                }
            }
        }
        .task(id: model.statusUpdatedAt) {
            if let configuration = model.status.configuration {
                if !connectionDraftInitialized { port = configuration.port }
                logLevel = configuration.logLevel
            }
            tunnelMode = (try? model.service.configuredTunnelMode()) ?? .local
            let savedNamedOrigin = model.service.configuredNamedTunnelOrigin()
            if !connectionDraftInitialized, model.status.configuration != nil {
                if !savedNamedOrigin.isEmpty {
                    serverURL = savedNamedOrigin
                } else if tunnelMode == .named {
                    serverURL = model.status.configuration?.publicURL ?? ""
                }
                connectionDraftInitialized = true
            }
            languagePreference = L10n.languagePreference()
            themePreference = AppAppearance.preference()
            coreAutostart = model.status.autostartEnabled
            menuAutostart = model.menuLoginAgent.isEnabled
        }
        .task(id: model.settingsPage) {
            if model.settingsPage == .advancedConnection {
                await model.refreshCloudflaredComponent()
                return
            }
            guard model.settingsPage == .logs else { return }
            analytics = model.status.loaded && model.status.healthy
                ? await model.service.runtimeAnalytics(configuration: model.status.configuration)
                : nil
        }
        .onChange(of: model.publicEndpointCheckRevision) { _ in
            publicEndpointCheckResult = nil
            testedPublicMCPURL = nil
            isTestingPublicEndpoint = false
        }
    }

    @ViewBuilder private var settingsContent: some View {
        switch model.settingsPage {
        case .logs:
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(
                    title: L10n.text("Logs"),
                    detail: L10n.text("View logs and diagnostics for troubleshooting.")
                )
                SettingsSection(L10n.text("Logs")) {
                    SettingsRow(L10n.text("Log level")) {
                        Picker(L10n.text("Log level"), selection: $logLevel) {
                            ForEach(["debug", "info", "warn", "error"], id: \.self) { value in
                                Text(logLevelTitle(value)).tag(value)
                            }
                        }
                        .labelsHidden()
                        .frame(width: 120, alignment: .trailing)
                        .onChange(of: logLevel) { value in
                            guard value != model.status.configuration?.logLevel else { return }
                            applyLogLevel(value)
                        }
                    }
                    RowDivider()
                    SettingsRow(L10n.text("Logs directory")) {
                        Button(L10n.text("Open")) { model.openLogs() }.controlSize(.small)
                    }
                    RowDivider()
                    SettingsRow(L10n.text("Configuration directory")) {
                        Button(L10n.text("Open")) { model.openConfiguration() }.controlSize(.small)
                    }
                }

                SettingsSection(L10n.text("Diagnostics")) {
                    DisclosureGroup(isExpanded: $showAdvancedDiagnostics) {
                        VStack(spacing: 0) {
                            SettingsRow(L10n.text("Runtime")) {
                                Text(model.status.loaded && model.status.healthy ? L10n.text("Running") : L10n.text("Stopped"))
                                    .foregroundStyle(.secondary)
                            }
                            RowDivider()
                            SettingsRow(L10n.text("Version")) {
                                Text(model.status.version ?? "—").foregroundStyle(.secondary)
                            }
                            if let analytics {
                                RowDivider()
                                SettingsRow(L10n.text("Total calls")) {
                                    Text("\(analytics.totalCalls)").foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("Failed calls")) {
                                    Text("\(analytics.totalErrors)").foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("Recent P95 latency")) {
                                    Text(RuntimeActivityFormat.duration(recentP95(analytics)))
                                        .font(.system(size: 11.5, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("Go heap")) {
                                    Text(RuntimeActivityFormat.bytes(analytics.process.heapAllocBytes)).foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("Goroutines")) {
                                    Text("\(analytics.process.goroutines)").foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("GC cycles")) {
                                    Text("\(analytics.process.gcCycles)").foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("Uptime")) {
                                    Text(RuntimeActivityFormat.uptime(analytics.process.uptimeMS)).foregroundStyle(.secondary)
                                }
                                RowDivider()
                                SettingsRow(L10n.text("Diagnostic information")) {
                                    Button(L10n.text("Copy diagnostics")) { copyDiagnostics(analytics) }
                                        .controlSize(.small)
                                }
                            } else {
                                RowDivider()
                                Text(L10n.text("Runtime analytics are unavailable."))
                                    .font(.system(size: 12.5))
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, 13)
                                    .padding(.vertical, 12)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.top, 8)
                    } label: {
                        Text(L10n.text("Advanced diagnostics"))
                            .font(.system(size: 13, weight: .medium))
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 13)
                    .padding(.vertical, 12)
                }

                if let message = model.message {
                    Text(message).font(.system(size: 12)).foregroundStyle(.secondary)
                }
            }
        case .traces:
            SettingsTracesView(model: model)
        case .permissions:
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(title: L10n.text("Permissions"), detail: L10n.text("Review macOS permissions required by enabled capabilities."))
                SettingsSection(L10n.text("System permissions")) {
                    SettingsRow(L10n.text("AgentDock permissions"), detail: L10n.text("Screen, accessibility and file access are managed by macOS.")) {
                        Button(L10n.text("Check permissions")) {
                            NotificationCenter.default.post(name: .agentDockOpenPermissions, object: nil)
                        }.controlSize(.small)
                    }
                }
            }
        case .startup:
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(title: L10n.text("Startup"), detail: L10n.text("Background service and menu bar startup behavior."))
                SettingsSection(L10n.text("Startup")) {
                    SettingsRow(L10n.text("Core background service")) {
                        Toggle(L10n.text("Core background service"), isOn: $coreAutostart)
                            .labelsHidden()
                            .toggleStyle(.switch)
                            .onChange(of: coreAutostart) { value in Task { await model.setCoreAutostart(value) } }
                    }
                    RowDivider()
                    SettingsRow(L10n.text("Menu bar app")) {
                        Toggle(L10n.text("Menu bar app"), isOn: $menuAutostart)
                            .labelsHidden()
                            .toggleStyle(.switch)
                            .onChange(of: menuAutostart) { value in Task { await model.setMenuAutostart(value) } }
                    }
                }
            }

        case .appearance:
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(
                    title: L10n.text("Appearance"),
                    detail: L10n.text("Customize the theme and interface language.")
                )
                SettingsSection(L10n.text("Appearance")) {
                    SettingsRow(L10n.text("Theme")) {
                        Picker(L10n.text("Theme"), selection: $themePreference) {
                            ForEach(UIThemePreference.allCases, id: \.rawValue) { preference in
                                Text(preference.title).tag(preference)
                            }
                        }
                        .labelsHidden()
                        .frame(width: 150, alignment: .trailing)
                        .onChange(of: themePreference) { preference in
                            model.setThemePreference(preference)
                        }
                    }
                    RowDivider()
                    SettingsRow(L10n.text("Interface language")) {
                        Picker(L10n.text("Interface language"), selection: $languagePreference) {
                            ForEach(UILanguagePreference.allCases, id: \.rawValue) { preference in
                                Text(preference.title).tag(preference)
                            }
                        }
                        .labelsHidden()
                        .frame(width: 150, alignment: .trailing)
                        .onChange(of: languagePreference) { preference in
                            model.setLanguagePreference(preference)
                        }
                    }
                }
            }
        case .about:
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(
                    title: L10n.text("About"),
                    detail: L10n.text("About AgentDock and useful project links.")
                )

                VStack(alignment: .leading, spacing: 8) {
                    AgentDockLogoView(size: 40)
                    Text("AgentDock")
                        .font(.system(size: 22, weight: .semibold))
                    Text(L10n.text("AgentDock lets AI clients securely use capabilities on this device."))
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.vertical, 4)

                SettingsSection(L10n.text("Application")) {
                    SettingsRow(L10n.text("Version")) {
                        Text(AppVersion.current)
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                    }
                    RowDivider()
                    SettingsRow(L10n.text("AgentDock update")) {
                        Button(
                            model.updateActivity == .checking
                                ? L10n.text("Checking for updates…")
                                : L10n.text("Check for updates")
                        ) {
                            model.requestUpdate()
                        }
                        .controlSize(.small)
                        .disabled(!model.updateActivity.canCheckForUpdates)
                    }
                }

                SettingsSection(L10n.text("Resources")) {
                    SettingsRow(L10n.text("Website")) {
                        Link(
                            L10n.text("Open"),
                            destination: URL(string: "https://nexusdock.co/")!
                        )
                        .controlSize(.small)
                    }
                    RowDivider()
                    SettingsRow(L10n.text("Documentation")) {
                        Link(
                            L10n.text("Open"),
                            destination: URL(string: "https://docs.nexusdock.co/agentdock/")!
                        )
                        .controlSize(.small)
                    }
                    RowDivider()
                    SettingsRow("GitHub") {
                        Link(
                            L10n.text("Open"),
                            destination: URL(string: "https://github.com/uvwt/agentdock")!
                        )
                        .controlSize(.small)
                    }
                }
            }

        case .advancedConnection:
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(
                    title: L10n.text("Advanced connection"),
                    detail: L10n.text("Local MCP, access credentials, and direct public access.")
                )

                SettingsSection("Local MCP") {
                    SettingsRow(
                        L10n.text("Local address"),
                        detail: model.status.configuration?.localMCPURL?.absoluteString ?? "—"
                    ) {
                        Button(L10n.text("Copy")) {
                            copy(model.status.configuration?.localMCPURL?.absoluteString)
                        }
                        .controlSize(.small)
                    }
                    RowDivider()
                    DisclosureGroup(isExpanded: $showCustomPort) {
                        VStack(spacing: 0) {
                            SettingsRow(L10n.text("Service port")) {
                                TextField(L10n.text("Service port"), value: $port, format: .number)
                                    .textFieldStyle(.roundedBorder)
                                    .frame(width: 100)
                            }
                            RowDivider()
                            HStack(spacing: 16) {
                                Text(L10n.text("Changing the service port will restart AgentDock."))
                                    .font(.system(size: 11.5))
                                    .foregroundStyle(.secondary)
                                Spacer()
                                Button(L10n.text("Apply changes")) {
                                    guard let configuration = model.status.configuration else { return }
                                    let settings = EditableServiceSettings(
                                        port: port,
                                        logLevel: configuration.logLevel,
                                        mcpAppsMode: configuration.mcpAppsMode,
                                        browserEnabled: configuration.browserEnabled,
                                        browserCDPURL: configuration.browserCDPURL,
                                        browserReuseExistingCDP: configuration.browserReuseExistingCDP,
                                        acpEnabled: configuration.acpEnabled,
                                        acpProfiles: configuration.acpProfiles,
                                        acpDefaultProfile: configuration.acpDefaultProfile,
                                        traceMode: configuration.traceMode,
                                        traceRetentionDays: configuration.traceRetentionDays
                                    )
                                    Task {
                                        if await model.applySettings(settings) {
                                            port = model.status.configuration?.port ?? port
                                        }
                                    }
                                }
                                .controlSize(.small)
                                .disabled(model.isBusy || port == model.status.configuration?.port)
                            }
                            .padding(.horizontal, 13)
                            .frame(minHeight: 48)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.top, 8)
                    } label: {
                        Text(L10n.text("Custom port"))
                            .font(.system(size: 13, weight: .medium))
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 13)
                    .padding(.vertical, 12)
                }

                SettingsSection(L10n.text("Access Credentials")) {
                    SettingsRow(
                        L10n.text("Authentication token"),
                        detail: displayedSecret(model.status.configuration?.authToken)
                    ) {
                        Button(L10n.text("Copy")) {
                            copy(model.status.configuration?.authToken)
                        }
                        .controlSize(.small)
                    }
                    RowDivider()
                    SettingsRow(
                        L10n.text("OAuth password"),
                        detail: displayedSecret(model.status.configuration?.oauthPassword)
                    ) {
                        Button(L10n.text("Copy")) {
                            copy(model.status.configuration?.oauthPassword)
                        }
                        .controlSize(.small)
                    }
                }

                SettingsSection(L10n.text("Optional component")) {
                    SettingsRow(
                        L10n.text("Cloudflare Tunnel"),
                        detail: model.cloudflaredComponentOperation?.text ?? cloudflaredComponentDetail
                    ) {
                        HStack(spacing: 8) {
                            if let operation = model.cloudflaredComponentOperation {
                                if let progress = operation.progress {
                                    ProgressView(value: progress)
                                        .frame(width: 72)
                                } else {
                                    ProgressView()
                                        .controlSize(.small)
                                }
                            } else if model.cloudflaredComponent.ready {
                                Button(L10n.text("Update")) {
                                    Task { await model.runCloudflaredComponentAction("update") }
                                }
                                .controlSize(.small)
                                Button(L10n.text("Uninstall")) {
                                    Task { await model.runCloudflaredComponentAction("uninstall") }
                                }
                                .controlSize(.small)
                            } else {
                                Button(
                                    model.cloudflaredComponent.state == "broken"
                                        ? L10n.text("Repair")
                                        : L10n.text("Install")
                                ) {
                                    Task { await model.runCloudflaredComponentAction("install") }
                                }
                                .controlSize(.small)
                            }
                        }
                        .disabled(model.isBusy)
                    }
                    RowDivider()
                    Text(L10n.text("Used for self-hosted public access. NexusDock remote connections do not require this component."))
                        .font(.system(size: 11.5))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 13)
                        .padding(.vertical, 11)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }

                if model.cloudflaredComponent.ready {
                SettingsSection(L10n.text("Public access")) {
                    SettingsRow(
                        L10n.text("Public address"),
                        detail: model.quickTunnelGenerating
                            ? L10n.text("Generating a new address…")
                            : (model.status.configuration?.publicMCPURL?.absoluteString ?? L10n.text("Disabled"))
                    ) {
                        HStack(spacing: 8) {
                            if !model.quickTunnelGenerating,
                               testedPublicMCPURL == model.status.configuration?.publicMCPURL,
                               let result = publicEndpointCheckResult {
                                Text(publicEndpointStatusText(result))
                                    .font(.system(size: 11.5, weight: .medium))
                                    .foregroundStyle(result.isReachable ? Color.green : Color.red)
                                    .help(result.message)
                            }

                            Button(isTestingPublicEndpoint ? L10n.text("Checking") : L10n.text("Test")) {
                                guard let publicMCPURL = model.status.configuration?.publicMCPURL else { return }
                                Task { await testPublicEndpoint(publicMCPURL) }
                            }
                            .controlSize(.small)
                            .disabled(
                                model.quickTunnelGenerating
                                    || model.status.configuration?.publicMCPURL == nil
                                    || isTestingPublicEndpoint
                            )

                            Button(L10n.text("Copy")) {
                                copy(model.status.configuration?.publicMCPURL?.absoluteString)
                            }
                            .controlSize(.small)
                            .disabled(model.quickTunnelGenerating || model.status.configuration?.publicMCPURL == nil)
                        }
                    }
                    RowDivider()
                    SettingsRow(
                        L10n.text("Temporary domain"),
                        detail: L10n.text("Generate a temporary public address without configuring a domain.")
                    ) {
                        Button(
                            tunnelMode == .quick
                                ? L10n.text("Regenerate temporary address")
                                : L10n.text("Generate temporary address")
                        ) {
                            Task {
                                await model.applyTunnel(mode: .quick, serverURL: "", tunnelToken: "")
                            }
                        }
                        .controlSize(.small)
                        .disabled(model.isBusy)
                    }
                    RowDivider()
                    VStack(alignment: .leading, spacing: 8) {
                        Text(L10n.text("Fixed domain"))
                            .font(.system(size: 13, weight: .medium))
                        Text(L10n.text("Use your own HTTPS domain with a Cloudflare Tunnel token."))
                            .font(.system(size: 12))
                            .foregroundStyle(.secondary)
                        TextField("https://mcp.example.com", text: $serverURL)
                            .textFieldStyle(.roundedBorder)
                        SecureField(L10n.text("Cloudflare Tunnel Token"), text: $tunnelToken)
                            .textFieldStyle(.roundedBorder)
                        Button(L10n.text("Apply fixed domain")) {
                            Task {
                                if await model.applyTunnel(mode: .named, serverURL: serverURL, tunnelToken: tunnelToken) {
                                    serverURL = model.service.configuredNamedTunnelOrigin()
                                    tunnelToken = ""
                                }
                            }
                        }
                        .disabled(
                            model.isBusy
                                || serverURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        )
                    }
                    .padding(13)
                }
                }

                if let message = model.message {
                    Text(message)
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)

                }
            }
        }
    }

    private var cloudflaredComponentDetail: String {
        if model.cloudflaredComponent.ready {
            if let version = model.cloudflaredComponent.version, !version.isEmpty {
                return L10n.format("Ready · %@", version)
            }
            return L10n.text("Ready")
        }
        if model.cloudflaredComponent.state == "broken" {
            return L10n.text("Needs repair")
        }
        return L10n.text("Not installed")
    }

    private func logLevelTitle(_ value: String) -> String {
        switch value {
        case "debug": return L10n.text("Debug")
        case "warn": return L10n.text("Warning")
        case "error": return L10n.text("Error")
        default: return L10n.text("Info")
        }
    }

    private func applyLogLevel(_ value: String) {
        guard let configuration = model.status.configuration,
              value != configuration.logLevel else { return }
        let settings = EditableServiceSettings(
            port: configuration.port,
            logLevel: value,
            mcpAppsMode: configuration.mcpAppsMode,
            browserEnabled: configuration.browserEnabled,
            browserCDPURL: configuration.browserCDPURL,
            browserReuseExistingCDP: configuration.browserReuseExistingCDP,
            acpEnabled: configuration.acpEnabled,
            acpProfiles: configuration.acpProfiles,
            acpDefaultProfile: configuration.acpDefaultProfile,
            traceMode: configuration.traceMode,
            traceRetentionDays: configuration.traceRetentionDays
        )
        Task { await model.applySettings(settings) }
    }

    private func recentP95(_ analytics: RuntimeAnalyticsPayload) -> Double {
        let values = analytics.recentCalls.map(\.durationMS).sorted()
        guard !values.isEmpty else { return 0 }
        let index = max(0, min(values.count - 1, Int(ceil(Double(values.count) * 0.95)) - 1))
        return values[index]
    }

    private func copyDiagnostics(_ analytics: RuntimeAnalyticsPayload) {
        let lines = [
            "AgentDock \(model.status.version ?? "unknown")",
            "runtime=\(model.status.loaded && model.status.healthy ? "running" : "stopped")",
            "total_calls=\(analytics.totalCalls)",
            "total_errors=\(analytics.totalErrors)",
            "recent_p95=\(RuntimeActivityFormat.duration(recentP95(analytics)))",
            "goroutines=\(analytics.process.goroutines)",
            "heap_alloc=\(RuntimeActivityFormat.bytes(analytics.process.heapAllocBytes))",
            "gc_cycles=\(analytics.process.gcCycles)",
            "uptime=\(RuntimeActivityFormat.uptime(analytics.process.uptimeMS))"
        ]
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(lines.joined(separator: "\n"), forType: .string)
    }

    private func displayedSecret(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "—" }
        return "••••••••••••"
    }

    private func publicEndpointStatusText(_ result: PublicEndpointCheckResult) -> String {
        if result.isReachable {
            guard let latency = result.latencyMilliseconds else { return L10n.text("Reachable") }
            return "\(latency) ms"
        }

        if let statusCode = result.httpStatusCode {
            let failed = L10n.text("Failed")
            if let latency = result.latencyMilliseconds {
                return "HTTP \(statusCode) · \(failed) · \(latency) ms"
            }
            return "HTTP \(statusCode) · \(failed)"
        }

        return L10n.text("Failed")
    }

    private func testPublicEndpoint(_ publicMCPURL: URL) async {
        guard !isTestingPublicEndpoint else { return }
        let lifecycleRevision = model.publicEndpointCheckRevision
        isTestingPublicEndpoint = true
        publicEndpointCheckResult = nil
        testedPublicMCPURL = nil

        let result = await PublicEndpointChecker().check(publicMCPURL: publicMCPURL)
        guard model.publicEndpointCheckRevision == lifecycleRevision,
              model.status.configuration?.publicMCPURL == publicMCPURL else {
            if model.publicEndpointCheckRevision == lifecycleRevision {
                isTestingPublicEndpoint = false
            }
            return
        }

        publicEndpointCheckResult = result
        testedPublicMCPURL = publicMCPURL
        isTestingPublicEndpoint = false
    }

    private func copy(_ value: String?) {
        guard let value, !value.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }
}

private extension Notification.Name {
    static let agentDockOpenPermissions = Notification.Name("AgentDockOpenPermissions")
}

// MARK: - Capability catalog

/// 能力目录二级页：先看全部条目，再搜索筛选，点条目进详情。
private struct CatalogSectionListView: View {
    @ObservedObject var model: ControlPanelModel
    let section: ControlPanelModel.CatalogSection
    @State private var query = ""

    private var entries: [ControlPanelModel.CatalogEntry] {
        let all = model.catalogEntries(for: section)
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return all }
        return all.filter { entry in
            entry.title.localizedCaseInsensitiveContains(trimmed)
                || (entry.subtitle?.localizedCaseInsensitiveContains(trimmed) ?? false)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 12) {
                Button {
                    model.selection = .page(.capabilities)
                } label: {
                    Label(L10n.text("Back"), systemImage: "chevron.left")
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)

                PageHeader(
                    title: section.title,
                    detail: L10n.format("%d items", entries.count)
                )

                SearchField(placeholder: L10n.text("Search"), text: $query)
            }
            .padding(.horizontal, 24)
            .padding(.top, 12)
            .padding(.bottom, 12)

            if entries.isEmpty {
                Text(L10n.text("No matching entries"))
                    .font(.system(size: 12.5))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 24)
                    .padding(.top, 8)
                Spacer(minLength: 0)
            } else {
                List(entries) { entry in
                    Button {
                        model.selection = entry.selection
                    } label: {
                        HStack(spacing: 10) {
                            Image(systemName: section.symbol)
                                .frame(width: 18, alignment: .leading)
                                .foregroundStyle(.secondary)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(entry.title).font(.system(size: 13))
                                if let subtitle = entry.subtitle, !subtitle.isEmpty {
                                    Text(subtitle)
                                        .font(.system(size: 11))
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                        .truncationMode(.middle)
                                }
                            }
                            Spacer(minLength: 12)
                            Image(systemName: "chevron.right")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(.tertiary)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
                .listStyle(.inset)
            }
        }
        // 目录页首屏就要看到条目：进入时补拉一次，避开上层页面的节流窗口。
        .task { await model.refreshRuntimeData() }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}

private struct CatalogDetailScroll<Content: View>: View {
    let title: String
    var detail: String?
    var onBack: (() -> Void)? = nil
    @ViewBuilder var content: () -> Content

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if let onBack {
                    Button(action: onBack) {
                        Label(L10n.text("Back"), systemImage: "chevron.left")
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
                }
                PageHeader(title: title, detail: detail)
                content()
            }
            .padding(.horizontal, 24)
            .padding(.bottom, 28)
            .frame(maxWidth: 920, alignment: .leading)
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }
}

private struct CatalogCard<Content: View>: View {
    let title: String
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(.secondary)
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color.primary.opacity(0.08), lineWidth: 1)
        )
    }
}

private struct CatalogKeyValueRow: View {
    let label: String
    let value: String
    var mono = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundStyle(.secondary)
                .frame(width: 118, alignment: .leading)
            Text(value)
                .font(.system(size: 12, design: mono ? .monospaced : .default))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

private struct CatalogUnavailable: View {
    let reason: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(L10n.text("Unavailable"))
                .font(.system(size: 15, weight: .semibold))
            Text(reason)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}

private struct ToolCatalogDetailView: View {
    @ObservedObject var model: ControlPanelModel
    let toolName: String

    var body: some View {
        if let tool = model.catalogTool(named: toolName) {
            CatalogDetailScroll(title: tool.name, detail: tool.title, onBack: { model.selection = .catalog(.tools) }) {
                if !tool.description.isEmpty {
                    Text(tool.description)
                        .font(.system(size: 12.5))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let annotations = tool.annotations {
                    CatalogCard(title: L10n.text("Annotations")) {
                        CatalogKeyValueRow(label: L10n.text("Read only"), value: yesNo(annotations.readOnlyHint))
                        if let destructive = annotations.destructiveHint {
                            CatalogKeyValueRow(label: L10n.text("Destructive"), value: yesNo(destructive))
                        }
                        CatalogKeyValueRow(label: L10n.text("Idempotent"), value: yesNo(annotations.idempotentHint))
                        if let openWorld = annotations.openWorldHint {
                            CatalogKeyValueRow(label: L10n.text("Open world"), value: yesNo(openWorld))
                        }
                    }
                }
                schemaBlock(L10n.text("Input schema"), tool.inputSchema)
                schemaBlock(L10n.text("Output schema"), tool.outputSchema)
            }
        } else {
            CatalogUnavailable(reason: L10n.text("The capability catalog is not available. Start AgentDock Core and try again."))
        }
    }

    private func yesNo(_ value: Bool) -> String {
        value ? L10n.text("Yes") : L10n.text("No")
    }

    @ViewBuilder private func schemaBlock(_ title: String, _ schema: RuntimeJSONValue?) -> some View {
        CatalogCard(title: title) {
            if let text = schema?.prettyText, !text.isEmpty {
                Text(text)
                    .font(.system(size: 11.5, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                Text("—").foregroundStyle(.secondary)
            }
        }
    }
}

private struct SkillCatalogDetailView: View {
    @ObservedObject var model: ControlPanelModel
    let skillRef: String

    var body: some View {
        if let skill = model.catalogSkill(reference: skillRef) {
            CatalogDetailScroll(title: skill.name, detail: skill.description, onBack: { model.selection = .catalog(.skills) }) {
                CatalogCard(title: L10n.text("Skill")) {
                    CatalogKeyValueRow(label: L10n.text("Source"), value: skill.sourceType)
                    CatalogKeyValueRow(label: L10n.text("Reference"), value: skill.skillRef, mono: true)
                    CatalogKeyValueRow(label: L10n.text("Content digest"), value: skill.contentDigest, mono: true)
                    CatalogKeyValueRow(label: L10n.text("Files"), value: "\(skill.fileCount)")
                    if let plugin = skill.pluginName, !plugin.isEmpty {
                        CatalogKeyValueRow(label: L10n.text("Plugin"), value: plugin)
                    }
                }
            }
        } else {
            CatalogUnavailable(reason: L10n.text("This Skill is no longer in the current index."))
        }
    }
}

private struct CommonSkillCatalogDetailView: View {
    @ObservedObject var model: ControlPanelModel
    let skillRef: String

    var body: some View {
        if let skill = model.catalogCommonSkill(reference: skillRef) {
            CatalogDetailScroll(title: skill.name, detail: skill.description, onBack: { model.selection = .catalog(.commonSkills) }) {
                CatalogCard(title: L10n.text("Skill")) {
                    CatalogKeyValueRow(label: L10n.text("Source"), value: skill.sourceType)
                    CatalogKeyValueRow(label: L10n.text("Reference"), value: skill.skillRef, mono: true)
                    CatalogKeyValueRow(label: L10n.text("File"), value: skill.file, mono: true)
                }
            }
        } else {
            CatalogUnavailable(reason: L10n.text("This shared Skill is no longer indexed."))
        }
    }
}

private struct RuleCatalogDetailView: View {
    @ObservedObject var model: ControlPanelModel
    let rulePath: String

    var body: some View {
        if let rule = model.catalogRule(path: rulePath) {
            CatalogDetailScroll(
                title: (rule.path as NSString).lastPathComponent,
                detail: rule.path,
                onBack: { model.selection = .catalog(.rules) }
            ) {
                CatalogCard(title: L10n.text("Rule")) {
                    CatalogKeyValueRow(label: L10n.text("Scope"), value: rule.scope)
                    CatalogKeyValueRow(label: L10n.text("Status"), value: rule.status)
                    if let size = rule.sizeBytes {
                        CatalogKeyValueRow(label: L10n.text("Size"), value: ByteCountFormatter.string(fromByteCount: size, countStyle: .file))
                    }
                    if let digest = rule.sha256, !digest.isEmpty {
                        CatalogKeyValueRow(label: L10n.text("SHA-256"), value: digest, mono: true)
                    }
                    if let reason = rule.reason, !reason.isEmpty {
                        CatalogKeyValueRow(label: L10n.text("Reason"), value: reason)
                    }
                    if let duplicate = rule.duplicateOf, !duplicate.isEmpty {
                        CatalogKeyValueRow(label: L10n.text("Duplicate of"), value: duplicate)
                    }
                }
                if let content = rule.content, !content.isEmpty {
                    CatalogCard(title: L10n.text("Content")) {
                        Text(content)
                            .font(.system(size: 11.5, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
        } else {
            CatalogUnavailable(reason: L10n.text("This rule file is no longer in the current scan."))
        }
    }
}

private struct PluginCatalogDetailView: View {
    @ObservedObject var model: ControlPanelModel
    let pluginName: String
    @State private var detail: RuntimePluginDetail?
    @State private var loading = false

    var body: some View {
        if let plugin = model.catalogPlugin(named: pluginName) {
            CatalogDetailScroll(title: plugin.name, detail: plugin.format, onBack: { model.selection = .catalog(.plugins) }) {
                CatalogCard(title: L10n.text("Plugin")) {
                    CatalogKeyValueRow(label: L10n.text("Version"), value: plugin.version)
                    CatalogKeyValueRow(label: L10n.text("Enabled"), value: plugin.enabled ? L10n.text("Yes") : L10n.text("No"))
                    CatalogKeyValueRow(label: L10n.text("Format"), value: plugin.format)
                    CatalogKeyValueRow(label: L10n.text("Skills"), value: "\(plugin.skillCount)")
                    CatalogKeyValueRow(label: L10n.text("MCP servers"), value: "\(plugin.mcpCount)")
                    if let provenance = plugin.provenance, !provenance.origin.isEmpty {
                        CatalogKeyValueRow(label: L10n.text("Provenance"), value: provenance.summary)
                    }
                    CatalogKeyValueRow(label: L10n.text("Package digest"), value: plugin.packageDigest, mono: true)
                }

                if let detail {
                    if let description = detail.description, !description.isEmpty {
                        CatalogCard(title: L10n.text("Description")) {
                            Text(description).font(.system(size: 12.5)).textSelection(.enabled)
                        }
                    }
                    if let skills = detail.skills, !skills.isEmpty {
                        CatalogCard(title: L10n.text("Skills")) {
                            ForEach(skills) { skill in
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(skill.name).font(.system(size: 12, weight: .medium))
                                    Text(skill.description)
                                        .font(.system(size: 11))
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                    if let mcp = detail.mcp, !mcp.isEmpty {
                        CatalogCard(title: L10n.text("MCP servers")) {
                            ForEach(mcp) { server in
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(server.name).font(.system(size: 12, weight: .medium))
                                    Text([server.transport, server.url ?? server.command ?? ""]
                                        .filter { !$0.isEmpty }
                                        .joined(separator: " · "))
                                        .font(.system(size: 10.5, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                    if let executables = detail.executables, !executables.isEmpty {
                        CatalogCard(title: L10n.text("Executables")) {
                            ForEach(executables, id: \.self) { path in
                                Text(path).font(.system(size: 11, design: .monospaced)).textSelection(.enabled)
                            }
                        }
                    }
                    if let warnings = detail.warnings, !warnings.isEmpty {
                        CatalogCard(title: L10n.text("Warnings")) {
                            ForEach(warnings, id: \.self) { warning in
                                Text(warning).font(.system(size: 11.5)).foregroundStyle(.orange).textSelection(.enabled)
                            }
                        }
                    }
                } else if loading {
                    ProgressView()
                }
            }
            .task(id: pluginName) {
                loading = true
                detail = await model.loadPluginDetail(name: pluginName)
                loading = false
            }
        } else {
            CatalogUnavailable(reason: L10n.text("This Plugin is no longer installed."))
        }
    }
}

// MARK: - Tasks

private struct TasksView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var query = ""
    @State private var statusFilter = "all"
    @State private var selectedTaskID: String?
    @State private var detail: RuntimeTaskDetail?
    @State private var expandedProjects: Set<String> = []
    @State private var autoRefresh = true

    private var allTasks: [RuntimeTaskRow] { model.runtimeTasks?.tasks ?? [] }

    private var filtered: [RuntimeTaskRow] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return allTasks.filter { task in
            if statusFilter != "all" && task.status != statusFilter { return false }
            guard !needle.isEmpty else { return true }
            let haystack = [task.title, task.project ?? "", task.device ?? ""].joined(separator: " ").lowercased()
            return haystack.contains(needle)
        }
    }

    private var groups: [(label: String, rows: [RuntimeTaskRow])] {
        var map: [String: [RuntimeTaskRow]] = [:]
        for task in filtered {
            map[task.project ?? "", default: []].append(task)
        }
        return map
            .map { key, rows in
                (label: key.isEmpty ? L10n.text("Unassigned") : key,
                 rows: rows.sorted { $0.updatedAt > $1.updatedAt })
            }
            .sorted { left, right in
                (left.rows.first?.updatedAt ?? "") > (right.rows.first?.updatedAt ?? "")
            }
    }

    private var statusSummary: [(String, Int, Color, String)] {
        let counts = Dictionary(grouping: allTasks, by: \.status).mapValues(\.count)
        return [
            (L10n.text("Active"), counts["active"] ?? 0, Color.accentColor, "active"),
            (L10n.text("Blocked"), counts["blocked"] ?? 0, Color.orange, "blocked"),
            (L10n.text("Completed"), counts["completed"] ?? 0, Color.secondary, "completed"),
        ]
    }

    private var isFiltering: Bool {
        statusFilter != "all" || !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func isExpanded(_ label: String) -> Bool {
        isFiltering || expandedProjects.contains(label)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 12) {
                PageHeader(title: L10n.text("Tasks"), detail: L10n.format("%d tasks", allTasks.count))
                HStack(spacing: 10) {
                    ForEach(statusSummary, id: \.0) { item in
                        Button {
                            statusFilter = statusFilter == item.3 ? "all" : item.3
                        } label: {
                            TaskStatusCard(
                                title: item.0,
                                count: item.1,
                                color: item.2,
                                selected: statusFilter == item.3
                            )
                        }
                        .buttonStyle(.plain)
                    }
                    Spacer(minLength: 0)
                }
                HStack(spacing: 10) {
                    SearchField(placeholder: L10n.text("Search title, project, or device"), text: $query)
                    Picker(L10n.text("Status"), selection: $statusFilter) {
                        Text(L10n.text("All")).tag("all")
                        Text(L10n.text("Active")).tag("active")
                        Text(L10n.text("Blocked")).tag("blocked")
                        Text(L10n.text("Completed")).tag("completed")
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .frame(width: 300)
                    Spacer()
                    AutoRefreshToggle(enabled: $autoRefresh)
                    Button(L10n.text("Refresh")) {
                        Task { await model.refreshRuntimeData(force: true) }
                    }
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider()

            if model.runtimeTasks == nil {
                CatalogUnavailable(reason: L10n.text("Tasks are unavailable while AgentDock Core is not running."))
            } else {
                HStack(spacing: 0) {
                    List(selection: $selectedTaskID) {
                        ForEach(groups, id: \.label) { group in
                            Section {
                                if isExpanded(group.label) {
                                    ForEach(group.rows) { task in
                                        TaskRowView(task: task)
                                            .tag(task.id)
                                    }
                                }
                            } header: {
                                Button {
                                    if expandedProjects.contains(group.label) {
                                        expandedProjects.remove(group.label)
                                    } else {
                                        expandedProjects.insert(group.label)
                                    }
                                } label: {
                                    HStack(spacing: 8) {
                                        Image(systemName: isExpanded(group.label) ? "chevron.down" : "chevron.right")
                                            .font(.system(size: 9, weight: .semibold))
                                            .foregroundStyle(.secondary)
                                            .frame(width: 10)
                                        Image(systemName: "folder")
                                            .font(.system(size: 11))
                                            .foregroundStyle(.secondary)
                                        Text(group.label)
                                            .font(.system(size: 12, weight: .medium))
                                            .lineLimit(1)
                                            .truncationMode(.middle)
                                            .help(group.label)
                                        Spacer(minLength: 8)
                                        Text("\(group.rows.count)")
                                            .font(.system(size: 10, weight: .semibold).monospacedDigit())
                                            .foregroundStyle(.secondary)
                                            .padding(.horizontal, 6)
                                            .padding(.vertical, 1.5)
                                            .background(Capsule().fill(Color.primary.opacity(0.07)))
                                    }
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                    // .sidebar 会把内容区渲染成侧栏灰底，这里只是分栏列表，应当跟窗口背景一致。
                    .listStyle(.inset)
                    .frame(minWidth: 300, idealWidth: 340, maxWidth: 420)

                    Divider()

                    taskDetailPane
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .task { await model.refreshRuntimeData(force: true) }
        .task(id: selectedTaskID) {
            guard let id = selectedTaskID else {
                detail = nil
                return
            }
            detail = await model.loadTaskDetail(id: id)
        }
        .autoRefresh(enabled: autoRefresh) { await model.refreshRuntimeData(force: true) }
    }

    @ViewBuilder private var taskDetailPane: some View {
        if let detail {
            TaskDetailContent(task: detail)
        } else if selectedTaskID != nil {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            VStack(spacing: 10) {
                Image(systemName: "square.split.2x1")
                    .font(.system(size: 26, weight: .light))
                    .foregroundStyle(.tertiary)
                Text(L10n.text("Select a task"))
                    .font(.system(size: 12.5))
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

private struct TaskStatusCard: View {
    let title: String
    let count: Int
    let color: Color
    let selected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 5) {
                Circle().fill(color).frame(width: 7, height: 7)
                Text(title)
                    .font(.system(size: 10.5, weight: .medium))
                    .lineLimit(1)
            }
            .foregroundStyle(.secondary)

            Text("\(count)")
                .font(.system(size: 20, weight: .semibold))
                .monospacedDigit()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .frame(minWidth: 104, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .fill(selected ? color.opacity(0.12) : Color(nsColor: .controlBackgroundColor))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .stroke(selected ? color.opacity(0.5) : Color.primary.opacity(0.07), lineWidth: 1)
        )
        .contentShape(Rectangle())
    }
}

private struct TaskRowView: View {
    let task: RuntimeTaskRow

    private var statusColor: Color {
        switch task.status {
        case "active": return .accentColor
        case "blocked": return .orange
        case "completed": return .secondary
        default: return .secondary
        }
    }

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Circle().fill(statusColor).frame(width: 7, height: 7).padding(.top, 5)
            VStack(alignment: .leading, spacing: 2) {
                Text(task.title).lineLimit(2)
                HStack(spacing: 8) {
                    if task.stepCount > 0 {
                        Text("\(task.completedStepCount)/\(task.stepCount)")
                            .monospacedDigit()
                    }
                    if let device = task.device, !device.isEmpty {
                        Text(device)
                    }
                    Text(RuntimeTimestamp.minute(task.updatedAt))
                        .monospacedDigit()
                        .help(RuntimeTimestamp.precise(task.updatedAt))
                }
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct TaskDetailContent: View {
    let task: RuntimeTaskDetail

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(task.title).font(.system(size: 18, weight: .semibold))
                    HStack(spacing: 10) {
                        Text(task.status)
                        Text(task.phase)
                        if let project = task.project, !project.isEmpty {
                            Text(project)
                        }
                        if let device = task.device, !device.isEmpty {
                            Text(device)
                        }
                    }
                    .font(.system(size: 11.5))
                    .foregroundStyle(.secondary)
                    HStack(spacing: 12) {
                        Text(RuntimeTimestamp.minute(task.updatedAt))
                            .help(RuntimeTimestamp.precise(task.updatedAt))
                        if let completed = task.completedAt {
                            Text(RuntimeTimestamp.minute(completed))
                                .help(RuntimeTimestamp.precise(completed))
                        }
                    }
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
                    .monospacedDigit()
                }

                if !task.goal.isEmpty {
                    CatalogCard(title: L10n.text("Goal")) {
                        Text(task.goal).font(.system(size: 12.5)).textSelection(.enabled)
                    }
                }
                if let blocker = task.blocker, !blocker.isEmpty {
                    CatalogCard(title: L10n.text("Blocker")) {
                        Text(blocker).font(.system(size: 12.5)).foregroundStyle(.orange).textSelection(.enabled)
                    }
                }
                if let conditions = task.conditions, !conditions.isEmpty {
                    CatalogCard(title: L10n.text("Conditions")) {
                        ForEach(conditions) { condition in
                            Text("• \(condition.text)").font(.system(size: 12)).textSelection(.enabled)
                        }
                    }
                }
                if let steps = task.steps, !steps.isEmpty {
                    CatalogCard(title: L10n.text("Steps")) {
                        ForEach(steps) { step in
                            HStack(alignment: .top, spacing: 8) {
                                Image(systemName: stepIcon(step.status))
                                    .font(.system(size: 11))
                                    .foregroundStyle(stepColor(step.status))
                                    .frame(width: 14)
                                Text(step.title).font(.system(size: 12))
                                Spacer()
                                Text(step.status).font(.system(size: 10.5)).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                if let summary = task.summary, !summary.isEmpty {
                    CatalogCard(title: L10n.text("Summary")) {
                        Text(summary).font(.system(size: 12.5)).textSelection(.enabled)
                    }
                }
                if let review = task.finalReview {
                    CatalogCard(title: L10n.text("Final review")) {
                        CatalogKeyValueRow(label: L10n.text("Status"), value: review.status)
                        if !review.summary.isEmpty {
                            Text(review.summary).font(.system(size: 12)).textSelection(.enabled)
                        }
                        ForEach(review.verifiedFacts ?? [], id: \.self) { fact in
                            Text("✓ \(fact)").font(.system(size: 11.5)).textSelection(.enabled)
                        }
                        ForEach(review.openRisks ?? [], id: \.self) { risk in
                            Text("! \(risk)").font(.system(size: 11.5)).foregroundStyle(.orange).textSelection(.enabled)
                        }
                    }
                }
                if let events = task.events, !events.isEmpty {
                    CatalogCard(title: L10n.text("Events")) {
                        ForEach(events.suffix(30)) { event in
                            HStack(alignment: .top, spacing: 8) {
                                Text(RuntimeTimestamp.minute(event.createdAt))
                                    .font(.system(size: 10.5, design: .monospaced))
                                    .foregroundStyle(.secondary)
                                    .help(RuntimeTimestamp.precise(event.createdAt))
                                Text(event.summary).font(.system(size: 11.5)).textSelection(.enabled)
                            }
                        }
                    }
                }
            }
            .padding(22)
            .frame(maxWidth: 760, alignment: .leading)
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }

    private func stepIcon(_ status: String) -> String {
        switch status {
        case "completed": return "checkmark.circle.fill"
        case "in_progress": return "circle.lefthalf.filled"
        default: return "circle"
        }
    }

    private func stepColor(_ status: String) -> Color {
        switch status {
        case "completed": return .green
        case "in_progress": return .accentColor
        default: return .secondary
        }
    }
}

// MARK: - Traces

private struct TracesView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var query = ""
    @State private var toolFilter = ""
    @State private var statusFilter = "all"
    @State private var payload: RuntimeTracesPayload?
    @State private var selectedTraceID: UInt64?
    @State private var detailRecord: RuntimeTraceRecord?
    @State private var detailLoading = false
    @State private var loading = false
    @State private var autoRefresh = true
    @State private var visibleCount = ListChunk.initial
    @State private var reloadTask: Task<Void, Never>?
    @State private var fullRecord: RuntimeTraceFullRecord?
    @State private var fullUnavailable = false
    @State private var fullLoading = false

    private var records: [RuntimeTraceRecord] { payload?.records ?? [] }
    private var visibleRecords: [RuntimeTraceRecord] { Array(records.prefix(visibleCount)) }

    /// 详情回读完成前先用列表摘要渲染元数据，避免选中后出现空白等待。
    private var selectedRecord: RuntimeTraceRecord? {
        if let detailRecord, detailRecord.id == selectedTraceID {
            return detailRecord
        }
        return records.first { $0.id == selectedTraceID }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 12) {
                PageHeader(title: L10n.text("Traces"), detail: statusDetail)
                HStack(spacing: 10) {
                    SearchField(placeholder: L10n.text("Search tool, arguments, or result"), text: $query)
                    Picker(L10n.text("Tool"), selection: $toolFilter) {
                        Text(L10n.text("All tools")).tag("")
                        ForEach(payload?.tools ?? [], id: \.self) { name in
                            Text(name).tag(name)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 190)
                    Picker(L10n.text("Status"), selection: $statusFilter) {
                        Text(L10n.text("All")).tag("all")
                        Text(L10n.text("Success")).tag("success")
                        Text(L10n.text("Error")).tag("error")
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .frame(width: 210)
                    Spacer()
                    AutoRefreshToggle(enabled: $autoRefresh)
                    Button(L10n.text("Refresh")) { Task { await reload() } }
                        .disabled(loading)
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider()

            if payload == nil {
                CatalogUnavailable(reason: L10n.text("Traces are unavailable while AgentDock Core is not running."))
            } else if payload?.effectiveMode == "off" {
                CatalogUnavailable(reason: L10n.text("Trace capture is off. Set AGENTDOCK_TRACE_MODE to memory or disk and restart AgentDock Core."))
            } else {
                HStack(spacing: 0) {
                    List(selection: $selectedTraceID) {
                        ForEach(Array(visibleRecords.enumerated()), id: \.element.id) { index, record in
                            TraceRowView(record: record)
                                .tag(record.id)
                                // 滚动到末行自动渲染下一批，大列表首帧只建 50 行。
                                .onAppear {
                                    if index == visibleRecords.count - 1 {
                                        visibleCount += ListChunk.size
                                    }
                                }
                        }
                    }
                    .listStyle(.inset)
                    .frame(minWidth: 320, idealWidth: 380, maxWidth: 460)

                    Divider()

                    traceDetailPane
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .task { await reload() }
        .task(id: selectedTraceID) {
            // 只有切换选中行才重置全文状态；自动刷新（reload→loadDetail）
            // 必须保留已加载的全文，否则 5 秒就把用户要看的正文抹掉。
            fullRecord = nil
            fullUnavailable = false
            await loadDetail()
        }
        .onChange(of: query) { _ in scheduleReload() }
        .onChange(of: toolFilter) { _ in scheduleReload() }
        .onChange(of: statusFilter) { _ in scheduleReload() }
        .autoRefresh(enabled: autoRefresh) { await reload() }
    }

    private var statusDetail: String {
        guard let payload else { return "" }
        let mode = payload.effectiveMode
        let retention = payload.status?.retentionDays.map { L10n.format("%d days", $0) } ?? "—"
        return L10n.format("%d records · %@ · retention %@", payload.count, mode, retention)
    }

    @ViewBuilder private var traceDetailPane: some View {
        if let record = selectedRecord {
            TraceDetailView(
                record: record,
                loadingDetail: detailLoading,
                full: fullRecord,
                fullUnavailable: fullUnavailable,
                fullLoading: fullLoading,
                onLoadFull: { Task { await loadFull() } }
            )
        } else {
            Text(L10n.text("Select a trace"))
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    /// 搜索输入防抖：过滤条件是 API 侧查询，不能每个按键打一次后端。
    private func scheduleReload() {
        reloadTask?.cancel()
        visibleCount = ListChunk.initial
        reloadTask = Task {
            try? await Task.sleep(nanoseconds: 250_000_000)
            if Task.isCancelled { return }
            await reload()
        }
    }

    private func reload() async {
        loading = true
        payload = await model.loadTraces(
            query: query,
            tool: toolFilter,
            source: "",
            status: statusFilter
        )
        if let selectedTraceID, !records.contains(where: { $0.id == selectedTraceID }) {
            self.selectedTraceID = nil
            self.detailRecord = nil
        }
        loading = false
        await loadDetail()
    }

    private func loadDetail() async {
        guard let id = selectedTraceID else {
            detailRecord = nil
            detailLoading = false
            return
        }
        // 自动刷新也走这里：已有该行详情时不再闪加载指示。
        if detailRecord?.id != id {
            detailLoading = true
        }
        let loaded = await model.loadTraceDetail(id: id)
        // 用户可能在请求返回前切换了选中行，晚到的响应不能覆盖当前详情。
        if selectedTraceID == id {
            detailRecord = loaded?.record
        }
        detailLoading = false
    }

    /// 全文来自旁路副本；缺失时（memory 模式/已清理）降级为预览。
    private func loadFull() async {
        guard let id = selectedTraceID else { return }
        fullLoading = true
        let loaded = await model.loadTraceDetail(id: id, full: true)
        if selectedTraceID == id {
            fullRecord = loaded?.full
            fullUnavailable = !(loaded?.fullAvailable ?? false)
        }
        fullLoading = false
    }
}

private struct TraceRowView: View {
    let record: RuntimeTraceRecord

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Circle()
                .fill(record.success ? Color.green : Color.red)
                .frame(width: 7, height: 7)
                .padding(.top, 5)
            VStack(alignment: .leading, spacing: 2) {
                Text(record.tool).font(.system(size: 12, weight: .medium))
                HStack(spacing: 8) {
                    Text(record.source)
                    Text(String(format: "%.1f ms", record.durationMS)).monospacedDigit()
                    Text(RuntimeTimestamp.minute(record.startedAt)).monospacedDigit()
                        .help(RuntimeTimestamp.precise(record.startedAt))
                    if let userAgent = record.userAgent, !userAgent.isEmpty {
                        Text(userAgent)
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .help(userAgent)
                    }
                }
                .font(.system(size: 10.5))
                .foregroundStyle(.secondary)
                if let summary = record.command?.summary, !summary.isEmpty {
                    Text(summary)
                        .font(.system(size: 10.5, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .help(summary)
                }
                if let code = record.errorCode, !code.isEmpty {
                    Text(code).font(.system(size: 10.5, design: .monospaced)).foregroundStyle(.red)
                }
            }
        }
        .padding(.vertical, 2)
        .contextMenu {
            Button(L10n.text("Copy tool name")) { copyToPasteboard(record.tool) }
            Button(L10n.text("Copy trace ID")) { copyToPasteboard(String(record.id)) }
        }
    }
}

// MARK: - Analytics dashboard

private enum AnalyticsSort: String, CaseIterable, Identifiable {
    case count, errorRate, p95

    var id: String { rawValue }

    var title: String {
        switch self {
        case .count: return L10n.text("Calls")
        case .errorRate: return L10n.text("Error rate")
        case .p95: return L10n.text("P95 latency")
        }
    }
}

private struct AnalyticsView: View {
    private static let barLimit = 24

    @ObservedObject var model: ControlPanelModel
    @State private var analytics: RuntimeAnalyticsPayload?
    @State private var query = ""
    @State private var sort: AnalyticsSort = .count
    @State private var autoRefresh = true

    private var running: Bool { model.status.loaded && model.status.healthy }

    private var stats: [RuntimeToolStats] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let filtered = (analytics?.toolStats ?? []).filter {
            needle.isEmpty || $0.tool.lowercased().contains(needle)
        }
        return filtered.sorted { left, right in
            switch sort {
            case .count:
                return left.count == right.count ? left.tool < right.tool : left.count > right.count
            case .errorRate:
                return left.errorRate == right.errorRate ? left.count > right.count : left.errorRate > right.errorRate
            case .p95:
                return left.p95DurationMS == right.p95DurationMS ? left.count > right.count : left.p95DurationMS > right.p95DurationMS
            }
        }
    }

    private var maxCount: Int { stats.map(\.count).max() ?? 0 }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                HStack(alignment: .top, spacing: 10) {
                    PageHeader(
                        title: L10n.text("Analytics"),
                        detail: L10n.text("Tool call counts and latency distribution.")
                    )
                    Spacer(minLength: 12)
                    AutoRefreshToggle(enabled: $autoRefresh)
                }

                if let analytics {
                    metrics(analytics)
                    chart
                    table
                } else {
                    CatalogUnavailable(
                        reason: running
                            ? L10n.text("Runtime analytics are unavailable.")
                            : L10n.text("Start AgentDock to view runtime activity.")
                    )
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 16)
            .padding(.bottom, 32)
            .frame(maxWidth: 960, alignment: .leading)
        }
        .task { await refreshAnalytics() }
        .autoRefresh(enabled: autoRefresh) { await refreshAnalytics() }
    }

    /// 指标卡在数据没变时保持不动：自动刷新不能把用户正要看的数字换掉。
    private func refreshAnalytics() async {
        guard running else {
            if analytics != nil { analytics = nil }
            return
        }
        let next = await model.service.runtimeAnalytics(configuration: model.status.configuration)
        if analytics == nil || next?.windowCalls != analytics?.windowCalls || next?.totalCalls != analytics?.totalCalls {
            analytics = next
        }
    }

    private func metrics(_ payload: RuntimeAnalyticsPayload) -> some View {
        let errorRate = payload.totalCalls == 0
            ? 0
            : Double(payload.totalErrors) / Double(payload.totalCalls)
        return HStack(spacing: 10) {
            AnalyticsMetric(
                title: L10n.text("Window calls"),
                value: "\(payload.windowCalls)",
                icon: "clock",
                caption: L10n.text("Last 5 minutes")
            )
            AnalyticsMetric(
                title: L10n.text("Total calls"),
                value: "\(payload.totalCalls)",
                icon: "sum"
            )
            AnalyticsMetric(
                title: L10n.text("Errors"),
                value: "\(payload.totalErrors)",
                icon: "exclamationmark.triangle",
                tint: payload.totalErrors > 0 ? .red : .secondary
            )
            AnalyticsMetric(
                title: L10n.text("Error rate"),
                value: String(format: "%.1f%%", errorRate * 100),
                icon: "percent",
                tint: errorRate > 0.05 ? .red : (errorRate > 0 ? .orange : .secondary)
            )
            AnalyticsMetric(
                title: L10n.text("Active calls"),
                value: "\(payload.activeCalls)",
                icon: "bolt.horizontal",
                tint: payload.activeCalls > 0 ? .accentColor : .secondary
            )
        }
    }

    private var chart: some View {
        CatalogCard(title: L10n.text("Tool calls")) {
            HStack(spacing: 10) {
                SearchField(placeholder: L10n.text("Search tools"), text: $query)
                Picker(L10n.text("Sort by"), selection: $sort) {
                    ForEach(AnalyticsSort.allCases) { option in
                        Text(option.title).tag(option)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 300)
                Spacer()
                Text(L10n.format("%d tools", stats.count))
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }
            .padding(.bottom, 6)

            if stats.isEmpty {
                Text(L10n.text("No tool calls recorded yet."))
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.vertical, 10)
            } else {
                ForEach(stats.prefix(Self.barLimit)) { item in
                    AnalyticsBarRow(stat: item, maxCount: maxCount)
                }
                if stats.count > Self.barLimit {
                    Text(L10n.format("Showing top %d of %d tools.", Self.barLimit, stats.count))
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .padding(.top, 4)
                }
            }
        }
    }

    private var table: some View {
        CatalogCard(title: L10n.text("Details")) {
            HStack(spacing: 8) {
                Text(L10n.text("Tool")).frame(maxWidth: .infinity, alignment: .leading)
                Text(L10n.text("Calls")).frame(width: 70, alignment: .trailing)
                Text(L10n.text("Errors")).frame(width: 70, alignment: .trailing)
                Text(L10n.text("Error rate")).frame(width: 90, alignment: .trailing)
                Text("P50").frame(width: 70, alignment: .trailing)
                Text("P95").frame(width: 70, alignment: .trailing)
                Text("P99").frame(width: 70, alignment: .trailing)
            }
            .font(.system(size: 10.5, weight: .semibold))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 8)
            .padding(.bottom, 4)
            Divider()
            ForEach(Array(stats.enumerated()), id: \.element.id) { index, item in
                AnalyticsTableRow(stat: item, isEven: index.isMultiple(of: 2))
            }
        }
    }

    private func formatMS(_ value: Double) -> String {
        analyticsDuration(value)
    }
}

private struct AnalyticsMetric: View {
    let title: String
    let value: String
    var icon: String
    var tint: Color = .secondary
    var caption: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 5) {
                Image(systemName: icon)
                    .font(.system(size: 10, weight: .semibold))
                Text(title)
                    .font(.system(size: 10.5, weight: .medium))
                    .lineLimit(1)
            }
            .foregroundStyle(.secondary)

            Text(value)
                .font(.system(size: 22, weight: .semibold))
                .monospacedDigit()
                .foregroundStyle(tint == .secondary ? Color.primary : tint)

            if let caption {
                Text(caption)
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, minHeight: 84, alignment: .topLeading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .stroke(Color.primary.opacity(0.07), lineWidth: 1)
        )
    }
}

private struct AnalyticsBarRow: View {
    let stat: RuntimeToolStats
    let maxCount: Int

    private var ratio: Double {
        guard maxCount > 0 else { return 0 }
        return min(1, Double(stat.count) / Double(maxCount))
    }

    private var barColor: Color {
        if stat.errorRate > 0.2 { return .red }
        if stat.errorCount > 0 { return .orange }
        return .accentColor
    }

    var body: some View {
        HStack(spacing: 10) {
            Text(stat.tool)
                .font(.system(size: 11.5, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(width: 168, alignment: .leading)

            GeometryReader { geometry in
                ZStack(alignment: .leading) {
                    Capsule()
                        .fill(Color.primary.opacity(0.06))
                    Capsule()
                        .fill(
                            LinearGradient(
                                colors: [barColor.opacity(0.7), barColor],
                                startPoint: .leading,
                                endPoint: .trailing
                            )
                        )
                        .frame(width: max(3, geometry.size.width * ratio))
                }
            }
            .frame(height: 8)

            Text("\(stat.count)")
                .font(.system(size: 11.5, weight: .medium).monospacedDigit())
                .frame(width: 40, alignment: .trailing)

            Text(stat.errorCount > 0 ? String(format: "%.0f%%", stat.errorRate * 100) : "—")
                .font(.system(size: 10.5).monospacedDigit())
                .foregroundStyle(stat.errorCount > 0 ? Color.red : Color.secondary)
                .frame(width: 46, alignment: .trailing)
        }
        .padding(.vertical, 3)
    }
}

private struct AnalyticsTableRow: View {
    let stat: RuntimeToolStats
    let isEven: Bool

    var body: some View {
        HStack(spacing: 8) {
            Text(stat.tool)
                .font(.system(size: 12, design: .monospaced))
                .frame(maxWidth: .infinity, alignment: .leading)
            Text("\(stat.count)").frame(width: 70, alignment: .trailing)
            Text("\(stat.errorCount)")
                .foregroundStyle(stat.errorCount > 0 ? Color.red : Color.secondary)
                .frame(width: 70, alignment: .trailing)
            Text(String(format: "%.1f%%", stat.errorRate * 100))
                .foregroundStyle(stat.errorRate > 0 ? Color.red : Color.secondary)
                .frame(width: 90, alignment: .trailing)
            Text(analyticsDuration(stat.p50DurationMS)).frame(width: 70, alignment: .trailing)
            Text(analyticsDuration(stat.p95DurationMS)).frame(width: 70, alignment: .trailing)
            Text(analyticsDuration(stat.p99DurationMS)).frame(width: 70, alignment: .trailing)
        }
        .font(.system(size: 11.5).monospacedDigit())
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(
            RoundedRectangle(cornerRadius: 5, style: .continuous)
                .fill(isEven ? Color.primary.opacity(0.035) : Color.clear)
        )
        .contextMenu {
            Button(L10n.text("Copy tool name")) { copyToPasteboard(stat.tool) }
        }
    }
}

private func analyticsDuration(_ value: Double) -> String {
    value >= 1000 ? String(format: "%.1fs", value / 1000) : String(format: "%.0fms", value)
}

// MARK: - File change history

private struct FileTreeNode: Identifiable {
    let id: String
    let name: String
    let path: String
    let isDirectory: Bool
    let changeCount: Int
    let latest: String?
    let children: [FileTreeNode]?
}

private final class FileTreeBuilder {
    let name: String
    let path: String
    let isDirectory: Bool
    var children: [String: FileTreeBuilder] = [:]
    var changeCount = 0
    var latest: String?

    init(name: String, path: String, isDirectory: Bool) {
        self.name = name
        self.path = path
        self.isDirectory = isDirectory
    }

    func freeze() -> FileTreeNode {
        let sorted = children.values
            .map { $0.freeze() }
            .sorted { left, right in
                if left.isDirectory != right.isDirectory { return left.isDirectory }
                return left.name.localizedStandardCompare(right.name) == .orderedAscending
            }
        return FileTreeNode(
            id: path,
            name: name,
            path: path,
            isDirectory: isDirectory,
            changeCount: changeCount,
            latest: latest,
            children: isDirectory ? sorted : nil
        )
    }
}

private enum FileTree {
    /// 去掉所有路径的公共目录前缀，让树从一个有意义的项目根开始。
    static func build(_ entries: [RuntimeFileChangeEntry]) -> (root: String, nodes: [FileTreeNode]) {
        guard !entries.isEmpty else { return ("", []) }
        let components = entries.map { ($0.path as NSString).pathComponents }
        var common = components.first ?? []
        for parts in components.dropFirst() {
            var index = 0
            while index < common.count, index < parts.count, common[index] == parts[index] {
                index += 1
            }
            common = Array(common.prefix(index))
        }
        // 公共部分是文件本身时退回父目录，避免根节点变成一个文件名。
        if common.count == components.first?.count, components.count == 1 || common.last?.contains(".") == true {
            common = Array(common.dropLast())
        }
        let root: String
        if common.isEmpty {
            root = "/"
        } else if common.first == "/" {
            // pathComponents 把绝对路径拆出前导 "/"，直接 join 会产生 "//a/b"。
            root = "/" + common.dropFirst().joined(separator: "/")
        } else {
            root = common.joined(separator: "/")
        }

        let tree = FileTreeBuilder(name: root, path: root, isDirectory: true)
        for entry in entries {
            let all = (entry.path as NSString).pathComponents
            let relative = all.count > common.count ? Array(all.dropFirst(common.count)) : [all.last ?? entry.path]
            var node = tree
            tree.changeCount += 1
            if tree.latest == nil || entry.startedAt > (tree.latest ?? "") { tree.latest = entry.startedAt }
            for (index, part) in relative.enumerated() {
                let isLeaf = index == relative.count - 1
                let path = (node.path == "/" ? "" : node.path) + "/" + part
                if let existing = node.children[part] {
                    node = existing
                } else {
                    let child = FileTreeBuilder(name: part, path: path, isDirectory: !isLeaf)
                    node.children[part] = child
                    node = child
                }
                node.changeCount += 1
                if node.latest == nil || entry.startedAt > (node.latest ?? "") { node.latest = entry.startedAt }
            }
        }
        return (root, tree.children.values.map { $0.freeze() }.sorted { left, right in
            if left.isDirectory != right.isDirectory { return left.isDirectory }
            return left.name.localizedStandardCompare(right.name) == .orderedAscending
        })
    }
}

private struct FilesView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var payload: RuntimeFileChangesPayload?
    @State private var query = ""
    @State private var selectedPath: String?
    @State private var selectedChangeID: String?
    @State private var loading = false

    private var entries: [RuntimeFileChangeEntry] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return (payload?.changes ?? []).filter {
            needle.isEmpty || $0.path.lowercased().contains(needle)
        }
    }

    private var tree: (root: String, nodes: [FileTreeNode]) {
        FileTree.build(entries)
    }

    private var selectedEntries: [RuntimeFileChangeEntry] {
        guard let selectedPath else { return [] }
        return entries.filter { $0.path == selectedPath }
    }

    private var selectedEntry: RuntimeFileChangeEntry? {
        selectedEntries.first { $0.id == selectedChangeID } ?? selectedEntries.first
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 12) {
                PageHeader(
                    title: L10n.text("Files"),
                    detail: L10n.text("Created and edited files recorded from tool calls.")
                )
                HStack(spacing: 10) {
                    TextField(L10n.text("Search paths"), text: $query)
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 300)
                    Spacer()
                    if let payload {
                        Text(L10n.format("%d changes · %@", payload.count, payload.effectiveMode))
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                    }
                    if let root = payload?.status?.root, !root.isEmpty {
                        Text(root)
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .help(root)
                    }
                    Button(L10n.text("Refresh")) { Task { await reload() } }
                        .disabled(loading)
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider()

            if payload == nil {
                CatalogUnavailable(reason: L10n.text("File history is unavailable while AgentDock Core is not running."))
            } else if payload?.effectiveMode == "off" {
                CatalogUnavailable(
                    reason: L10n.text("File history comes from tool call traces. Set AGENTDOCK_TRACE_MODE to memory or disk and restart AgentDock Core to record file_edit calls.")
                )
            } else if entries.isEmpty {
                CatalogUnavailable(reason: L10n.text("No file changes recorded yet. History starts when trace capture is enabled."))
            } else {
                HStack(spacing: 0) {
                    VStack(alignment: .leading, spacing: 0) {
                        Text(tree.root)
                            .font(.system(size: 10.5, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.head)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                        Divider()
                        List(selection: $selectedPath) {
                            OutlineGroup(tree.nodes, children: \.children) { node in
                                FileTreeRow(node: node)
                                    .tag(node.path)
                            }
                        }
                        .listStyle(.inset)
                    }
                    .frame(minWidth: 280, idealWidth: 340, maxWidth: 440)

                    Divider()

                    detailPane
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .task { await reload() }
    }

    @ViewBuilder private var detailPane: some View {
        if let node = treeNode(for: selectedPath) {
            VStack(alignment: .leading, spacing: 0) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(node.name)
                        .font(.system(size: 16, weight: .semibold))
                    Text(selectedPath ?? "")
                        .font(.system(size: 10.5, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                .padding(.horizontal, 18)
                .padding(.vertical, 12)

                Divider()

                let history = selectedEntries
                if history.count > 1 {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 8) {
                            ForEach(history) { entry in
                                Button {
                                    selectedChangeID = entry.id
                                } label: {
                                    FileChangeChip(entry: entry, selected: entry.id == selectedEntry?.id)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        .padding(.horizontal, 18)
                        .padding(.vertical, 8)
                    }
                    Divider()
                }

                if let entry = selectedEntry {
                    diffPane(entry)
                } else {
                    Text(L10n.text("Select a recorded change"))
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        } else {
            Text(L10n.text("Select a file to preview its diff"))
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func diffPane(_ entry: RuntimeFileChangeEntry) -> some View {
        ScrollView([.horizontal, .vertical]) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 10) {
                    Text(fileActionTitle(entry.action, fallback: entry.tool))
                        .font(.system(size: 11, weight: .medium))
                    Text("+\(entry.insertions ?? 0)")
                        .foregroundStyle(.green)
                    Text("-\(entry.deletions ?? 0)")
                        .foregroundStyle(.red)
                    Text(RuntimeTimestamp.minute(entry.startedAt))
                        .foregroundStyle(.secondary)
                        .help(RuntimeTimestamp.precise(entry.startedAt))
                    if entry.truncated == true {
                        Text(L10n.text("truncated"))
                            .foregroundStyle(.orange)
                    }
                    Spacer()
                }
                .font(.system(size: 11).monospacedDigit())
                .padding(.horizontal, 18)
                .padding(.vertical, 8)

                Divider()

                let lines = diffLines(entry.diff ?? "")
                if lines.isEmpty {
                    Text(L10n.text("No diff preview was recorded for this change."))
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                        .padding(18)
                } else {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(lines) { line in
                            Text(line.text.isEmpty ? " " : line.text)
                                .font(.system(size: 11.5, design: .monospaced))
                                .textSelection(.enabled)
                                .padding(.horizontal, 14)
                                .padding(.vertical, 1)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(line.kind.background)
                                .foregroundStyle(line.kind.foreground)
                        }
                    }
                    .padding(.vertical, 6)
                }
            }
            .frame(minWidth: 520, alignment: .leading)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private func treeNode(for path: String?) -> FileTreeNode? {
        guard let path else { return nil }
        func find(_ nodes: [FileTreeNode]) -> FileTreeNode? {
            for node in nodes {
                if node.path == path { return node }
                if let children = node.children, let hit = find(children) { return hit }
            }
            return nil
        }
        return find(tree.nodes)
    }

    private func reload() async {
        loading = true
        payload = await model.loadFileChanges(query: "")
        if let selectedPath, !entries.contains(where: { $0.path == selectedPath }) {
            self.selectedPath = nil
            selectedChangeID = nil
        }
        loading = false
    }
}

private func fileActionTitle(_ action: String, fallback: String) -> String {
    switch action {
    case "add": return L10n.text("Created")
    case "replace": return L10n.text("Edited")
    case "patch": return L10n.text("Patched")
    case "delete": return L10n.text("Deleted")
    case "move": return L10n.text("Moved")
    case "": return fallback
    default: return action
    }
}

private struct FileTreeRow: View {
    let node: FileTreeNode

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: node.isDirectory ? "folder" : "doc.text")
                .font(.system(size: 11))
                .foregroundStyle(node.isDirectory ? Color.secondary : Color.accentColor)
            Text(node.name)
                .font(.system(size: 12))
                .lineLimit(1)
            Spacer(minLength: 4)
            if node.changeCount > 0 {
                Text("\(node.changeCount)")
                    .font(.system(size: 10).monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
    }
}

private struct FileChangeChip: View {
    let entry: RuntimeFileChangeEntry
    let selected: Bool

    var body: some View {
        HStack(spacing: 6) {
            Text(RuntimeTimestamp.minute(entry.startedAt))
                .font(.system(size: 10.5).monospacedDigit())
            Text("+\(entry.insertions ?? 0)")
                .foregroundStyle(.green)
            Text("-\(entry.deletions ?? 0)")
                .foregroundStyle(.red)
        }
        .font(.system(size: 10.5))
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(selected ? Color.accentColor.opacity(0.18) : Color.primary.opacity(0.05))
        .clipShape(RoundedRectangle(cornerRadius: 5, style: .continuous))
    }
}

private struct FileDiffLine: Identifiable {
    enum Kind {
        case added, removed, header, context

        var background: Color {
            switch self {
            case .added: return Color.green.opacity(0.12)
            case .removed: return Color.red.opacity(0.12)
            case .header: return Color.primary.opacity(0.06)
            case .context: return .clear
            }
        }

        var foreground: Color {
            switch self {
            case .added: return .primary
            case .removed: return .primary
            case .header: return .secondary
            case .context: return .primary
            }
        }
    }

    let id: Int
    let kind: Kind
    let text: String
}

private func diffLines(_ diff: String) -> [FileDiffLine] {
    guard !diff.isEmpty else { return [] }
    return diff
        .split(separator: "\n", omittingEmptySubsequences: false)
        .enumerated()
        .map { index, raw in
            let line = String(raw)
            let kind: FileDiffLine.Kind
            if line.hasPrefix("+++") || line.hasPrefix("---") || line.hasPrefix("@@") || line.hasPrefix("diff ") || line.hasPrefix("index ") {
                kind = .header
            } else if line.hasPrefix("+") {
                kind = .added
            } else if line.hasPrefix("-") {
                kind = .removed
            } else {
                kind = .context
            }
            return FileDiffLine(id: index, kind: kind, text: line)
        }
}

// MARK: - Published artifacts

private struct PublishedView: View {
    @ObservedObject var model: ControlPanelModel
    @State private var payload: RuntimeArtifactsPayload?
    @State private var loading = false
    @State private var query = ""
    @State private var autoRefresh = true
    @State private var visibleCount = ListChunk.initial

    private var filteredItems: [RuntimeArtifactItem] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return (payload?.artifacts ?? []).filter {
            needle.isEmpty || $0.filename.lowercased().contains(needle) || $0.url.lowercased().contains(needle)
        }
    }
    private var visibleItems: [RuntimeArtifactItem] { Array(filteredItems.prefix(visibleCount)) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 12) {
                PageHeader(
                    title: L10n.text("Published"),
                    detail: L10n.text("Files shared through file_publish.")
                )
                HStack(spacing: 10) {
                    SearchField(placeholder: L10n.text("Search published files"), text: $query)
                    if let payload {
                        Text(L10n.format("%d files", payload.count))
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                        if !payload.publicBaseURL.isEmpty {
                            Text(payload.publicBaseURL)
                                .font(.system(size: 10, design: .monospaced))
                                .foregroundStyle(.tertiary)
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .help(payload.publicBaseURL)
                        }
                    }
                    Spacer()
                    AutoRefreshToggle(enabled: $autoRefresh)
                    Button(L10n.text("Refresh")) { Task { await reload() } }
                        .disabled(loading)
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 16)
            .padding(.bottom, 12)

            Divider()

            if payload == nil {
                CatalogUnavailable(reason: L10n.text("Published files are unavailable while AgentDock Core is not running."))
            } else {
                if payload?.configured == false {
                    HStack(spacing: 8) {
                        Image(systemName: "exclamationmark.triangle")
                            .foregroundStyle(.orange)
                        Text(L10n.text("No public address is configured. Links appear after configuring a public URL or tunnel."))
                            .font(.system(size: 11.5))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.horizontal, 24)
                    .padding(.vertical, 10)
                    Divider()
                }
                let items = filteredItems
                if items.isEmpty {
                    CatalogUnavailable(reason: query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        ? L10n.text("No published files.")
                        : L10n.text("No matching results."))
                } else {
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(visibleItems.enumerated()), id: \.element.id) { index, item in
                                PublishedRow(item: item)
                                    .onAppear {
                                        // 滚动到末行自动渲染下一批。
                                        if index == visibleItems.count - 1 {
                                            visibleCount += ListChunk.size
                                        }
                                    }
                                Divider().padding(.leading, 24)
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                }
            }
        }
        .task { await reload() }
        .autoRefresh(enabled: autoRefresh) { await reload() }
        .onChange(of: query) { _ in visibleCount = ListChunk.initial }
    }

    private func reload() async {
        loading = true
        payload = await model.loadArtifacts()
        loading = false
    }
}

private struct PublishedRow: View {
    let item: RuntimeArtifactItem

    private var expired: Bool {
        guard let date = RuntimeTimestamp.parse(item.expiresAt) else { return false }
        return date < Date()
    }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: item.archive ? "archivebox" : "doc")
                .font(.system(size: 14))
                .foregroundStyle(.secondary)
                .frame(width: 18)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(item.filename)
                        .font(.system(size: 12.5, weight: .medium))
                        .lineLimit(1)
                    if item.archive {
                        Text(L10n.text("Archive"))
                            .font(.system(size: 10))
                            .foregroundStyle(.secondary)
                    }
                }
                HStack(spacing: 12) {
                    Text(ByteCountFormatter.string(fromByteCount: item.sizeBytes, countStyle: .file))
                    Text(item.mimeType)
                    Text(RuntimeTimestamp.minute(item.createdAt))
                        .help(RuntimeTimestamp.precise(item.createdAt))
                    Text(L10n.format("Expires %@", RuntimeTimestamp.minute(item.expiresAt)))
                        .foregroundStyle(expired ? .red : .secondary)
                        .help(RuntimeTimestamp.precise(item.expiresAt))
                }
                .font(.system(size: 10.5).monospacedDigit())
                .foregroundStyle(.secondary)

                if !item.url.isEmpty {
                    HStack(spacing: 8) {
                        Text(item.url)
                            .font(.system(size: 10.5, design: .monospaced))
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                            .textSelection(.enabled)
                        Button(L10n.text("Open")) {
                            if let url = URL(string: item.url) { NSWorkspace.shared.open(url) }
                        }
                        .controlSize(.small)
                        Button(L10n.text("Copy link")) {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(item.url, forType: .string)
                        }
                        .controlSize(.small)
                    }
                    .padding(.top, 2)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 12)
    }
}
