import AppKit
import SwiftUI

// MARK: - Lightweight syntax highlighting

enum TraceCodeLanguage {
    case shell
    case json
    case log
    case plain
}

/// 高亮只区分少量 token：目标是让人眼在等宽输出里快速定位命令、键、字符串与
/// 日志级别。完整语法分析既没必要，也会让长输出逐行解析变慢。
private enum TraceTokenKind {
    case plain, command, flag, string, key, number, keyword, comment, variable, punctuation
    case logError, logWarning, logInfo, logDebug
}

private struct TraceToken {
    let text: String
    let kind: TraceTokenKind
}

/// 非 private：TraceResultCards.swift 的带行号代码块复用同一套高亮。
enum TraceCodeHighlighter {
    /// 用 Text 拼接而不是 AttributedString 累加：两种写法渲染结果一致，
    /// 选 Text 是为了少一层 AttributedString 转换开销。
    static func styled(_ line: String, language: TraceCodeLanguage) -> Text {
        tokens(line, language: language).reduce(Text("")) { partial, token in
            var piece = Text(token.text).font(font(for: token.kind))
            if let color = color(for: token.kind) {
                piece = piece.foregroundColor(color)
            }
            return partial + piece
        }
    }

    private static func font(for kind: TraceTokenKind) -> Font {
        switch kind {
        case .command, .keyword:
            return .system(size: 11.5, weight: .semibold, design: .monospaced)
        default:
            return .system(size: 11.5, design: .monospaced)
        }
    }

    private static func color(for kind: TraceTokenKind) -> Color? {
        switch kind {
        case .plain: return nil
        case .command: return .primary
        case .flag: return .cyan
        case .string: return .green
        case .key: return .purple
        case .number: return .orange
        case .keyword: return .orange
        case .comment: return .secondary
        case .variable: return .pink
        case .punctuation: return .secondary
        case .logError: return .red
        case .logWarning: return .orange
        case .logInfo: return .blue
        case .logDebug: return .secondary
        }
    }

    private static func tokens(_ line: String, language: TraceCodeLanguage) -> [TraceToken] {
        switch language {
        case .shell: return shellTokens(line)
        case .json: return jsonTokens(line)
        case .log: return logTokens(line)
        case .plain: return [TraceToken(text: line, kind: .plain)]
        }
    }

    // MARK: Shell

    private static func shellTokens(_ line: String) -> [TraceToken] {
        var tokens: [TraceToken] = []
        let characters = Array(line)
        var index = 0
        var wordIndex = 0
        var plain = ""

        func flushPlain() {
            guard !plain.isEmpty else { return }
            tokens.append(TraceToken(text: plain, kind: .plain))
            plain = ""
        }

        while index < characters.count {
            let character = characters[index]
            if character == " " || character == "\t" {
                plain.append(character)
                index += 1
                continue
            }
            if character == "#" {
                flushPlain()
                tokens.append(TraceToken(text: String(characters[index...]), kind: .comment))
                return tokens
            }
            if character == "'" || character == "\"" {
                flushPlain()
                var literal = String(character)
                index += 1
                while index < characters.count {
                    let current = characters[index]
                    literal.append(current)
                    index += 1
                    if current == "\\" && character == "\"" && index < characters.count {
                        literal.append(characters[index])
                        index += 1
                        continue
                    }
                    if current == character {
                        break
                    }
                }
                tokens.append(TraceToken(text: literal, kind: .string))
                wordIndex += 1
                continue
            }
            if let (operatorText, nextIndex) = shellOperator(characters, at: index) {
                flushPlain()
                tokens.append(TraceToken(text: operatorText, kind: .punctuation))
                index = nextIndex
                wordIndex += 1
                continue
            }
            if character == "$" {
                flushPlain()
                var variable = "$"
                index += 1
                while index < characters.count, isVariableCharacter(characters[index]) {
                    variable.append(characters[index])
                    index += 1
                }
                tokens.append(TraceToken(text: variable, kind: .variable))
                wordIndex += 1
                continue
            }
            var word = ""
            while index < characters.count {
                let current = characters[index]
                if current == " " || current == "\t" || current == "'" || current == "\"" || current == "$" || shellOperator(characters, at: index) != nil {
                    break
                }
                word.append(current)
                index += 1
            }
            guard !word.isEmpty else {
                index += 1
                continue
            }
            // 词之前必须先吐出待发空白：否则空白会堆到后面的引号/操作符之前，
            // 渲染出来变成 “lib.tssearch  'x'”——参数之间的空格消失。
            flushPlain()
            tokens.append(TraceToken(text: word, kind: shellWordKind(word, wordIndex: wordIndex)))
            wordIndex += 1
        }
        flushPlain()
        return tokens
    }

    private static func shellWordKind(_ word: String, wordIndex: Int) -> TraceTokenKind {
        if word.hasPrefix("-"), word.count > 1 { return .flag }
        if wordIndex == 0 { return .command }
        if let separator = word.firstIndex(of: "="), separator != word.startIndex {
            let name = word[..<separator]
            if name.allSatisfy({ $0.isLetter || $0.isNumber || $0 == "_" }) { return .variable }
        }
        return .plain
    }

    private static func isVariableCharacter(_ character: Character) -> Bool {
        character.isLetter || character.isNumber || character == "_" || character == "{" || character == "}"
    }

    private static func shellOperator(_ characters: [Character], at index: Int) -> (String, Int)? {
        let character = characters[index]
        guard "|&;<>".contains(character) else { return nil }
        var end = index + 1
        if end < characters.count, characters[end] == character || (character == "&" && characters[end] == ">") {
            end += 1
        }
        return (String(characters[index..<end]), end)
    }

    // MARK: JSON

    private static func jsonTokens(_ line: String) -> [TraceToken] {
        var tokens: [TraceToken] = []
        let characters = Array(line)
        var index = 0
        var plain = ""

        func flushPlain() {
            guard !plain.isEmpty else { return }
            tokens.append(TraceToken(text: plain, kind: .plain))
            plain = ""
        }

        while index < characters.count {
            let character = characters[index]
            if character == "\"" {
                flushPlain()
                var literal = "\""
                index += 1
                var escaped = false
                while index < characters.count {
                    let current = characters[index]
                    literal.append(current)
                    index += 1
                    if escaped {
                        escaped = false
                        continue
                    }
                    if current == "\\" {
                        escaped = true
                        continue
                    }
                    if current == "\"" {
                        break
                    }
                }
                // 键名判断：字符串后紧跟冒号（允许空格）才是 key，否则是值。
                var lookahead = index
                while lookahead < characters.count, characters[lookahead] == " " {
                    lookahead += 1
                }
                let isKey = lookahead < characters.count && characters[lookahead] == ":"
                tokens.append(TraceToken(text: literal, kind: isKey ? .key : .string))
                continue
            }
            if character.isNumber || character == "-" {
                flushPlain()
                var number = ""
                while index < characters.count, characters[index].isNumber || "+-.eE".contains(characters[index]) {
                    number.append(characters[index])
                    index += 1
                }
                tokens.append(TraceToken(text: number, kind: .number))
                continue
            }
            if character.isLetter {
                flushPlain()
                var word = ""
                while index < characters.count, characters[index].isLetter {
                    word.append(characters[index])
                    index += 1
                }
                let kind: TraceTokenKind = (word == "true" || word == "false" || word == "null") ? .keyword : .plain
                tokens.append(TraceToken(text: word, kind: kind))
                continue
            }
            if "{}[],:".contains(character) {
                flushPlain()
                tokens.append(TraceToken(text: String(character), kind: .punctuation))
                index += 1
                continue
            }
            plain.append(character)
            index += 1
        }
        flushPlain()
        return tokens
    }

    // MARK: Log

    private static let logLevels: [(text: String, kind: TraceTokenKind)] = [
        ("ERROR", .logError),
        ("FATAL", .logError),
        ("WARNING", .logWarning),
        ("WARN", .logWarning),
        ("INFO", .logInfo),
        ("DEBUG", .logDebug),
        ("TRACE", .logDebug)
    ]

    private static func logTokens(_ line: String) -> [TraceToken] {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.hasPrefix("{") || trimmed.hasPrefix("[") {
            return jsonTokens(line)
        }
        // 必须在原字符串上做大小写不敏感匹配：先 uppercased() 再复用索引会在
        // ß 等大小写转换改变长度的字符上错位，甚至越界。
        var best: (range: Range<String.Index>, kind: TraceTokenKind)?
        for level in logLevels {
            guard let range = line.range(of: level.text, options: .caseInsensitive) else { continue }
            if best == nil || range.lowerBound < best!.range.lowerBound {
                best = (range, level.kind)
            }
        }
        guard let match = best else {
            return [TraceToken(text: line, kind: .plain)]
        }
        var tokens: [TraceToken] = []
        if match.range.lowerBound > line.startIndex {
            tokens.append(TraceToken(text: String(line[line.startIndex..<match.range.lowerBound]), kind: .plain))
        }
        tokens.append(TraceToken(text: String(line[match.range]), kind: match.kind))
        if match.range.upperBound < line.endIndex {
            tokens.append(TraceToken(text: String(line[match.range.upperBound...]), kind: .plain))
        }
        return tokens
    }
}

// MARK: - Rendering

/// 等宽代码块：按行懒渲染，超过一页再增量展开，避免 8 KiB 输出一次性
/// 构造上千个 Text。列表页不渲染正文，只有详情页按需加载后才走到这里。
struct TraceCodeBlock: View {
    private static let pageSize = 400

    let language: TraceCodeLanguage
    private let lines: [String]
    @State private var visibleLines = TraceCodeBlock.pageSize

    init(text: String, language: TraceCodeLanguage) {
        self.language = language
        self.lines = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            LazyVStack(alignment: .leading, spacing: 1) {
                ForEach(0..<min(lines.count, visibleLines), id: \.self) { index in
                    TraceCodeHighlighter.styled(lines[index], language: language)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .textSelection(.enabled)

            if lines.count > visibleLines {
                Button(L10n.format("Load %d more lines", min(TraceCodeBlock.pageSize, lines.count - visibleLines))) {
                    visibleLines += TraceCodeBlock.pageSize
                }
                .buttonStyle(.link)
                .font(.system(size: 11))
            }
        }
    }
}

/// 与 CatalogCard 视觉一致；CatalogCard 是控制面板主文件的 fileprivate 组件，
/// trace 渲染单独成文件时不能复用，只能复制同一份样式。
/// 非 private：TraceResultCards.swift 的专用结果卡共用同一套外观。
struct TraceSurfaceCard<Trailing: View, Content: View>: View {
    /// nil 表示分区名由外层 tab 提供，卡片不画标题行。
    let title: String?
    var trailing: Trailing
    @ViewBuilder var content: () -> Content

    init(title: String?, trailing: Trailing, @ViewBuilder content: @escaping () -> Content) {
        self.title = title
        self.trailing = trailing
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let title {
                HStack(alignment: .firstTextBaseline) {
                    Text(title)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 12)
                    trailing
                }
            }
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

extension TraceSurfaceCard where Trailing == EmptyView {
    init(title: String?, @ViewBuilder content: @escaping () -> Content) {
        self.init(title: title, trailing: EmptyView(), content: content)
    }
}

/// 一次解析的结果：值树 + 叶子计数。解析在 TraceDetailView init 完成，
/// body 重估与开关切换不再重复 parse；叶子数用于超大正文回退分页代码块。
struct TraceJSONDocument {
    let value: Any
    let valueCount: Int

    static func parse(_ text: String?) -> TraceJSONDocument? {
        guard let text, !text.isEmpty, let data = text.data(using: .utf8),
              let value = try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) else { return nil }
        return TraceJSONDocument(value: value, valueCount: countLeaves(value))
    }

    private static func countLeaves(_ value: Any) -> Int {
        switch value {
        case let nested as [String: Any]:
            return nested.values.reduce(1) { $0 + countLeaves($1) }
        case let array as [Any]:
            return array.reduce(1) { $0 + countLeaves($1) }
        default:
            return 1
        }
    }
}

/// JSON 结构化卡片：把 Arguments/Result 的 JSON 文本解析成键值卡片，
/// 对象/数组递归展开并缩进。截断预览可能不是合法 JSON，解析失败时
/// 回退到原始代码块渲染，保证任何内容都可见。
private struct TraceJSONCardView: View {
    /// 卡片渲染的叶子值上限。全文旁路单侧可达 256 KiB，递归 VStack 一次性
    /// 构造上千个 Text 会卡详情页；超限回退到分页代码块。
    private static let maxRenderedValues = 600

    let document: TraceJSONDocument?
    let fallbackText: String

    var body: some View {
        if let document, document.valueCount <= Self.maxRenderedValues {
            VStack(alignment: .leading, spacing: 4) {
                TraceJSONValueView(key: nil, value: document.value)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .textSelection(.enabled)
        } else {
            TraceCodeBlock(text: fallbackText, language: .json)
        }
    }
}

/// 递归键值行：标量渲染成 label + value 一行（复用 TraceMetadataRow 的
/// 固定标签宽度，与命令卡元数据视觉一致）；容器按键名/索引展开为嵌套块。
private struct TraceJSONValueView: View {
    let key: String?
    let value: Any

    var body: some View {
        switch value {
        case let nested as [String: Any]:
            container(key: key, isEmpty: nested.isEmpty, emptyText: "{}") {
                ForEach(nested.keys.sorted(), id: \.self) { childKey in
                    TraceJSONValueView(key: childKey, value: nested[childKey].map { $0 as Any } ?? NSNull())
                }
            }
        case let array as [Any]:
            container(key: key, isEmpty: array.isEmpty, emptyText: "[]") {
                ForEach(array.indices, id: \.self) { index in
                    TraceJSONValueView(key: "[\(index)]", value: array[index])
                }
            }
        default:
            TraceMetadataRow(label: key ?? "", value: Self.scalarText(value))
        }
    }

    @ViewBuilder private func container(
        key: String?, isEmpty: Bool, emptyText: String,
        @ViewBuilder children: () -> some View
    ) -> some View {
        if isEmpty {
            TraceMetadataRow(label: key ?? "", value: emptyText)
        } else {
            VStack(alignment: .leading, spacing: 4) {
                if let key {
                    Text(key)
                        .font(.system(size: 11.5, weight: .medium))
                        .foregroundStyle(.secondary)
                }
                children()
            }
            .padding(.leading, key == nil ? 0 : 14)
            .overlay(alignment: .leading) {
                // 嵌套层级用左侧竖线标记，缩进过深时仍能看出归属。
                if key != nil {
                    Rectangle()
                        .fill(Color.primary.opacity(0.12))
                        .frame(width: 1)
                        .padding(.leading, 4)
                }
            }
        }
    }

    private static func scalarText(_ value: Any) -> String {
        switch value {
        case is NSNull:
            return "null"
        case let flag as Bool:
            return flag ? "true" : "false"
        case let number as NSNumber:
            return number.stringValue
        default:
            return "\(value)"
        }
    }
}

struct TraceMetadataRow: View {
    let label: String
    let value: String
    var mono = true

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundStyle(.secondary)
                .frame(width: 118, alignment: .leading)
            Text(value)
                .font(.system(size: 11.5, design: mono ? .monospaced : .default))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct TraceBadge: View {
    let text: String
    var color: Color = .secondary

    var body: some View {
        Text(text)
            .font(.system(size: 9.5, weight: .semibold))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(color.opacity(0.14))
            .foregroundStyle(color)
            .clipShape(Capsule())
    }
}

enum TraceFormat {
    static func bytes(_ value: Int) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(value), countStyle: .memory)
    }

    static func duration(milliseconds: Int) -> String {
        if milliseconds < 1_000 { return "\(milliseconds) ms" }
        return String(format: "%.2f s", Double(milliseconds) / 1_000)
    }
}

/// exec_command 详情：命令、运行参数、退出状态与分段输出。
/// 正文只在详情接口回读后渲染，列表页只显示 summary 一行。
/// full 非空时优先渲染旁路全文副本（已脱敏）。
struct TraceCommandCard: View {
    let command: RuntimeTraceCommand
    var full: RuntimeTraceFullCommand? = nil

    /// 全文副本优先：预览只是全文的前 8 KiB。
    private var commandText: String? { full?.command ?? command.command }
    private var stdoutText: String? { full?.stdout ?? command.stdout }
    private var stderrText: String? { full?.stderr ?? command.stderr }

    private var hasCommandBody: Bool {
        !(commandText ?? "").isEmpty
    }

    var body: some View {
        TraceSurfaceCard(title: nil) {
            if let summary = command.summary, !summary.isEmpty, !hasCommandBody {
                Text(summary)
                    .font(.system(size: 11.5, design: .monospaced))
                    .textSelection(.enabled)
            }
            if let body = commandText, !body.isEmpty {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text("$")
                        .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                        .foregroundStyle(.secondary)
                    TraceCodeBlock(text: body, language: .shell)
                    if full == nil && command.commandPreviewTruncated == true {
                        TraceBadge(text: L10n.text("truncated"))
                    }
                    copyButton(body)
                }
            }

            VStack(alignment: .leading, spacing: 4) {
                if let workdir = command.workdir, !workdir.isEmpty {
                    TraceMetadataRow(label: L10n.text("Working directory"), value: workdir)
                }
                if let runtime = command.runtime, !runtime.isEmpty {
                    let label = command.wslDistribution.map { "\(runtime) (\($0))" } ?? runtime
                    TraceMetadataRow(label: L10n.text("Runtime"), value: label)
                }
                if let mode = command.executionMode, !mode.isEmpty {
                    TraceMetadataRow(label: L10n.text("Mode"), value: mode)
                }
                if let timeout = command.timeoutMS {
                    TraceMetadataRow(label: L10n.text("Timeout"), value: TraceFormat.duration(milliseconds: timeout))
                }
                if let action = command.action, !action.isEmpty {
                    TraceMetadataRow(label: L10n.text("Action"), value: action)
                }
                if let session = command.sessionID, !session.isEmpty {
                    TraceMetadataRow(label: L10n.text("Session"), value: session)
                }
                if let status = command.status, !status.isEmpty {
                    TraceMetadataRow(label: L10n.text("Status"), value: status)
                }
                if let exitCode = command.exitCode {
                    TraceMetadataRow(label: L10n.text("Exit code"), value: "\(exitCode)")
                }
                if command.timedOut == true {
                    TraceBadge(text: L10n.text("Timed out"), color: .orange)
                }
            }
            .padding(.top, 2)

            // 摘要视图没有输出正文（stdout/stderr 被省略），此时不渲染两个空块，
            // 等详情回读后再出现，避免选中瞬间的误导性“—”占位。
            if stdoutText != nil || stderrText != nil {
                TraceOutputSection(
                    title: L10n.text("Stdout"),
                    text: stdoutText,
                    totalBytes: command.stdoutBytes,
                    toolTruncated: command.stdoutTruncated,
                    previewTruncated: full == nil ? command.stdoutPreviewTruncated : nil
                )
                TraceOutputSection(
                    title: L10n.text("Stderr"),
                    text: stderrText,
                    totalBytes: command.stderrBytes,
                    toolTruncated: command.stderrTruncated,
                    previewTruncated: full == nil ? command.stderrPreviewTruncated : nil
                )
            }
        }
    }

    private func copyButton(_ text: String) -> some View {
        Button(L10n.text("Copy")) {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
        }
        .buttonStyle(.link)
        .font(.system(size: 10.5))
    }
}

struct TraceOutputSection: View {
    let title: String
    let text: String?
    let totalBytes: Int?
    let toolTruncated: Bool?
    let previewTruncated: Bool?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(title)
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundStyle(.secondary)
                if let totalBytes, totalBytes > 0 {
                    Text(TraceFormat.bytes(totalBytes))
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                }
                if toolTruncated == true {
                    TraceBadge(text: L10n.text("output truncated"), color: .orange)
                }
                if previewTruncated == true {
                    TraceBadge(text: L10n.text("truncated"))
                }
                if let text, !text.isEmpty {
                    CopyButton(text: text)
                }
            }
            if let text, !text.isEmpty {
                TraceCodeBlock(text: text, language: .log)
            } else {
                Text("—").foregroundStyle(.secondary)
            }
        }
    }
}

// MARK: - Trace detail

/// 详情页分区。命令类工具多出命令分区，带错误码的记录多出错误分区。
enum TraceDetailTab: String, CaseIterable {
    case command
    case arguments
    case result
    case error

    var title: String {
        switch self {
        case .command: return L10n.text("Command")
        case .arguments: return L10n.text("Arguments")
        case .result: return L10n.text("Result")
        case .error: return L10n.text("Error")
        }
    }
}

/// 调用追踪详情页。record 可能是列表摘要（正文还没回来）也可能是完整详情；
/// 摘要记录只渲染元数据，正文与命令输出等 detail 回读后再出现。
/// 预览被截断时提供「加载全文」：全文来自旁路副本，缺失时降级为预览。
/// 入参/出参/命令/错误以 tab 分区，同一时间只渲染一个分区。
struct TraceDetailView: View {
    let record: RuntimeTraceRecord
    var loadingDetail = false
    var full: RuntimeTraceFullRecord?
    var fullUnavailable = false
    var fullLoading = false
    var onLoadFull: (() -> Void)? = nil

    /// Arguments/Result 的渲染模式开关：卡片（结构化键值）还是原始 JSON 文本。
    /// 用户级配置，跨详情页与启动会话持久化。
    @AppStorage("TraceRenderJSONAsCards") private var renderJSONAsCards = true

    /// 分区选择同样是用户级偏好：换选中行（甚至重启 App）都停在上次看的分区，
    /// 否则每选一条记录都要重新点回原来那一栏。
    @AppStorage("TraceDetailTab") private var selectedTab: TraceDetailTab = .arguments

    /// 解析/解码只在视图重建时执行一次；body 重估（开关切换、加载态变化）
    /// 直接复用，不再对同一份正文反复 parse/decode。
    private let argumentsJSON: TraceJSONDocument?
    private let resultJSON: TraceJSONDocument?
    /// 已知工具的 Result 专用卡片模型；非专用工具或解析失败时为 nil。
    private let typedResult: TraceTypedResult?
    private let imagePreviewLoader: ((UInt64) async -> RuntimeTraceImagePayload?)?

    init(
        record: RuntimeTraceRecord,
        loadingDetail: Bool = false,
        full: RuntimeTraceFullRecord? = nil,
        fullUnavailable: Bool = false,
        fullLoading: Bool = false,
        onLoadFull: (() -> Void)? = nil,
        onLoadImagePreview: ((UInt64) async -> RuntimeTraceImagePayload?)? = nil
    ) {
        self.record = record
        self.loadingDetail = loadingDetail
        self.full = full
        self.fullUnavailable = fullUnavailable
        self.fullLoading = fullLoading
        self.onLoadFull = onLoadFull
        self.imagePreviewLoader = onLoadImagePreview
        let resultText = full?.result ?? record.result
        self.argumentsJSON = TraceJSONDocument.parse(full?.arguments ?? record.arguments)
        self.resultJSON = TraceJSONDocument.parse(resultText)
        self.typedResult = TraceTypedResult.decode(tool: record.tool, text: resultText)
    }

    private var command: RuntimeTraceCommand? { record.command }

    private var argumentsText: String? { full?.arguments ?? record.arguments }
    private var resultText: String? { full?.result ?? record.result }
    private var errorText: String? { full?.error ?? record.error }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                tabBar
                activeTabContent

                if loadingDetail {
                    ProgressView()
                        .controlSize(.small)
                }

                fullTextFooter
            }
            .padding(22)
            .frame(maxWidth: 860, alignment: .leading)
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }

    /// 当前记录可用的分区。顺序固定，同一用户在不同记录间不用重新找位置。
    private var tabs: [TraceDetailTab] {
        var tabs: [TraceDetailTab] = []
        if command != nil { tabs.append(.command) }
        tabs.append(contentsOf: [.arguments, .result])
        if let code = record.errorCode, !code.isEmpty { tabs.append(.error) }
        return tabs
    }

    /// 记录不支持已记住的分区（例如从 exec_command 切到 file_edit）时回落入参，
    /// 但不覆盖偏好：切回命令类记录仍回到命令分区。
    private var activeTab: TraceDetailTab {
        tabs.contains(selectedTab) ? selectedTab : .arguments
    }

    private var tabSelection: Binding<TraceDetailTab> {
        Binding(get: { activeTab }, set: { selectedTab = $0 })
    }

    private var tabBar: some View {
        HStack(spacing: 12) {
            Picker("", selection: tabSelection) {
                ForEach(tabs, id: \.self) { tab in
                    Text(tab.title).tag(tab)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            Spacer(minLength: 12)
            // 渲染模式只决定入参/出参的 JSON 呈现，其他分区不显示这个开关。
            if activeTab == .arguments || activeTab == .result {
                renderModeToggle
            }
        }
    }

    @ViewBuilder private var activeTabContent: some View {
        switch activeTab {
        case .command:
            if let command {
                TraceCommandCard(command: command, full: full?.command)
            }
        case .arguments:
            payloadCard(argumentsText, document: argumentsJSON, language: .json)
        case .result:
            resultSection
        case .error:
            errorCard
        }
    }

    /// 错误分区：卡片标题由 tab 承担，错误正文为空时保留占位符。
    private var errorCard: some View {
        TraceSurfaceCard(title: nil) {
            if let code = record.errorCode, !code.isEmpty {
                TraceMetadataRow(label: L10n.text("Code"), value: code)
            }
            if let error = errorText, !error.isEmpty {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(error).font(.system(size: 12)).textSelection(.enabled)
                    CopyButton(text: error)
                }
            } else {
                Text("—").foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder private var fullTextFooter: some View {
        if record.truncated == true {
            VStack(alignment: .leading, spacing: 6) {
                if full != nil {
                    Text(full?.fullTruncated == true
                         ? L10n.text("Full text is truncated to 256 KiB per side.")
                         : L10n.text("Showing full text."))
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                } else if fullUnavailable {
                    Text(L10n.text("Full text is unavailable. Showing the 8 KiB preview."))
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                } else {
                    Text(L10n.text("Payload preview is truncated to 8 KiB per side."))
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                    Button(L10n.text("Load full text")) {
                        onLoadFull?()
                    }
                    .disabled(fullLoading || onLoadFull == nil)
                    if fullLoading {
                        ProgressView()
                            .controlSize(.small)
                    }
                }
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(record.tool).font(.system(size: 18, weight: .semibold))
                CopyButton(text: record.tool)
            }
            HStack(spacing: 10) {
                Text(record.source)
                Text(String(format: "%.1f ms", record.durationMS)).monospacedDigit()
                Text(record.success ? L10n.text("Success") : L10n.text("Error"))
                Text(RuntimeTimestamp.precise(record.startedAt)).monospacedDigit()
            }
            .font(.system(size: 11.5))
            .foregroundStyle(.secondary)
            if let userAgent = record.userAgent, !userAgent.isEmpty {
                Text(userAgent)
                    .font(.system(size: 10.5, design: .monospaced))
                    .foregroundStyle(.tertiary)
                    .textSelection(.enabled)
            }
        }
    }

    /// 结果区：优先工具专用卡（文件变更 / 任务状态 / 能力索引 / 文件操作），
    /// 无专用卡或关闭卡片模式时回退通用 JSON 渲染。
    @ViewBuilder private var resultSection: some View {
        if renderJSONAsCards, let changes = record.fileChanges, !changes.isEmpty {
            TraceFileChangesCard(changes: changes, rawText: resultText ?? "")
        } else if renderJSONAsCards, let typedResult {
            TraceTypedResultCard(
                result: typedResult,
                rawText: resultText ?? "",
                traceID: record.id,
                imagePreview: record.imagePreview,
                onLoadImagePreview: imagePreviewLoader
            )
        } else {
            payloadCard(resultText, document: resultJSON, language: .log)
        }
    }

    /// JSON 分区卡片不画标题：入参/出参名由 tab 标注，卡片只渲染正文与复制按钮。
    @ViewBuilder private func payloadCard(_ text: String?, document: TraceJSONDocument?, language: TraceCodeLanguage) -> some View {
        TraceSurfaceCard(title: nil) {
            if let text, !text.isEmpty {
                HStack(alignment: .top, spacing: 8) {
                    if renderJSONAsCards {
                        TraceJSONCardView(document: document, fallbackText: text)
                    } else {
                        TraceCodeBlock(text: text, language: language)
                    }
                    CopyButton(text: text)
                }
            } else {
                Text("—").foregroundStyle(.secondary)
            }
        }
    }

    private var renderModeToggle: some View {
        Toggle(L10n.text("Render as cards"), isOn: $renderJSONAsCards)
            .toggleStyle(.checkbox)
            .controlSize(.small)
            .font(.system(size: 11))
    }
}
