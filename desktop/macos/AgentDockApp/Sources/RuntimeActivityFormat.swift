import Foundation

/// 运行态活动的共用展示格式化。
/// 托盘面板与控制面板对时长/相对时间必须口径一致，否则同一行数据
/// 在两处显示成不同样子会让排障者误判时间线，所以抽成共享枚举。
enum RuntimeActivityFormat {
    private static let fractionalDateFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()
    private static let basicDateFormatter = ISO8601DateFormatter()

    static func duration(_ milliseconds: Double) -> String {
        if milliseconds < 1_000 { return String(format: "%.0f ms", milliseconds) }
        if milliseconds < 60_000 { return String(format: "%.2f s", milliseconds / 1_000) }
        return String(format: "%.1f min", milliseconds / 60_000)
    }

    static func bytes(_ value: UInt64) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(value), countStyle: .memory)
    }

    static func uptime(_ milliseconds: Int64) -> String {
        let seconds = max(0, milliseconds / 1_000)
        if seconds < 60 { return "\(seconds) s" }
        if seconds < 3_600 { return "\(seconds / 60) min" }
        if seconds < 86_400 { return "\(seconds / 3_600) h" }
        return "\(seconds / 86_400) d"
    }

    static func source(_ value: String) -> String {
        switch value.lowercased() {
        case "nexus": return L10n.text("Remote")
        case "internal": return L10n.text("Local")
        case "mcp": return "MCP"
        default: return value
        }
    }

    /// 相对时间带秒粒度：最近调用「几秒前发生」是排障时最直观的时间线线索，
    /// 只给「刚刚」会把刚发生的调用和一分钟前的调用混在一起。
    static func relativeTime(_ value: String) -> String {
        guard let date = fractionalDateFormatter.date(from: value) ?? basicDateFormatter.date(from: value) else { return value }
        let seconds = max(0, Int(Date().timeIntervalSince(date)))
        if seconds < 60 { return L10n.format("%d sec ago", max(1, seconds)) }
        if seconds < 3_600 { return L10n.format("%d min ago", seconds / 60) }
        return L10n.format("%d hr ago", seconds / 3_600)
    }

    static func stage(_ name: String) -> String {
        switch name {
        case "mcp.refresh": return L10n.text("MCP initialize & discover")
        case "mcp.remote_call": return L10n.text("MCP remote call")
        case "command.start": return L10n.text("Command start")
        case "command.foreground_wait": return L10n.text("Foreground wait")
        default: return name
        }
    }
}
