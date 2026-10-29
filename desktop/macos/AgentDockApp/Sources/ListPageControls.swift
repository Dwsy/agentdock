import AppKit
import SwiftUI

// MARK: - Search

/// 列表页统一搜索框：放大镜图标 + 清空按钮。
/// 各页面自己持有 query 状态与过滤逻辑，这里只统一外观和清空交互。
struct SearchField: View {
    let placeholder: String
    @Binding var text: String

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            TextField(placeholder, text: $text)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
            if !text.isEmpty {
                Button {
                    text = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 11))
                        .foregroundStyle(.tertiary)
                }
                .buttonStyle(.plain)
                .help(L10n.text("Clear search"))
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 6, style: .continuous)
                .stroke(Color.primary.opacity(0.12), lineWidth: 1)
        )
        .frame(maxWidth: 300)
    }
}

// MARK: - Copy

func copyToPasteboard(_ text: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
}

/// 小号复制按钮：详情页每个可复制字段都挂一个，和正文选择共存。
struct CopyButton: View {
    let text: String
    var label: String = L10n.text("Copy")

    var body: some View {
        Button(label) {
            copyToPasteboard(text)
        }
        .buttonStyle(.link)
        .font(.system(size: 10.5))
    }
}

// MARK: - Auto refresh

/// 列表页自动刷新开关：5 秒轮询，可暂停。
/// 暂停是显式状态而不是取消任务：用户暂停后切页回来仍应保持暂停。
struct AutoRefreshToggle: View {
    @Binding var enabled: Bool

    var body: some View {
        Button {
            enabled.toggle()
        } label: {
            Image(systemName: enabled ? "pause.circle" : "play.circle")
                .font(.system(size: 13))
        }
        .buttonStyle(.plain)
        .foregroundStyle(enabled ? Color.secondary : Color.accentColor)
        .help(enabled ? L10n.text("Pause auto refresh") : L10n.text("Resume auto refresh"))
    }
}

private struct AutoRefreshModifier: ViewModifier {
    let enabled: Bool
    let action: () async -> Void

    func body(content: Content) -> some View {
        content.task(id: enabled) {
            guard enabled else { return }
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                if Task.isCancelled { break }
                await action()
            }
        }
    }
}

extension View {
    /// 挂载 5 秒自动刷新循环；视图消失或 enabled 变 false 时循环自动取消。
    func autoRefresh(enabled: Bool, _ action: @escaping () async -> Void) -> some View {
        modifier(AutoRefreshModifier(enabled: enabled, action: action))
    }
}

// MARK: - Chunked list rendering

/// 长列表分批渲染的批次大小。只影响渲染量：列表端点已有条数上限，
/// 分批是为了让 500 行 diff/trace 不在首帧全部构建 Text。
enum ListChunk {
    static let size = 50

    /// 首批渲染条数；过滤条件变化后调用方重置回这个值。
    static var initial: Int { size }
}
