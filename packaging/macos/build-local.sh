#!/bin/zsh
# 本地一键构建 macOS App：i18n 校验 → Swift 类型检查 → Core/core-skills 载荷 →
# .app/.zip/.dmg → 安装到 /Applications → 加载 Core → 启动 App。
# 只装了 Command Line Tools 的机器会自动选一个仍用 propertyWrapper 实现 SwiftUI
# @State 的旧 SDK；ad-hoc 签名下 Core 由 launchctl bootstrap 手工加载，绕过
# SMAppService 的 LWCR 校验（重启后重跑本脚本即可恢复）。
set -euo pipefail

ROOT_DIR="${0:A:h:h:h}"
OUTPUT_DIR="$ROOT_DIR/dist/macos-app"
PAYLOAD_ROOT="$ROOT_DIR/dist/macos-payload"
INSTALL_DIR="${AGENTDOCK_MACOS_INSTALL_DIR:-/Applications}"
APP_NAME="AgentDock.app"
CORE_LABEL="com.uvwt.agentdock.core"
MIN_VERSION="13.0"

BUILD_ONLY=false
VERSION="0.0.0-dev"

usage() {
  cat <<'USAGE'
用法：
  packaging/macos/build-local.sh [版本] [--build-only]

选项：
  --build-only  只产出 dist/macos-app 下的 .app/.zip/.dmg，不安装、不启动

环境变量：
  AGENTDOCK_MACOS_INSTALL_DIR  安装目录，默认 /Applications
  AGENTDOCK_MACOS_SDK_PATH     显式指定 SDK；默认自动选择可用 SDK
  AGENTDOCK_CODESIGN_IDENTITY  转发给 build-app.sh，默认 -（ad-hoc）
USAGE
}

die() { print -u2 -- "ERROR: $*"; exit 1 }

for argument in "$@"; do
  case "$argument" in
    --build-only) BUILD_ONLY=true ;;
    -h|--help) usage; exit 0 ;;
    -*) die "未知参数：$argument" ;;
    *) VERSION="$argument" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "缺少 go"
command -v python3 >/dev/null 2>&1 || die "缺少 python3"
command -v xcrun >/dev/null 2>&1 || die "缺少 xcrun（需要 Xcode Command Line Tools）"
command -v curl >/dev/null 2>&1 || die "缺少 curl"

# macOS 26+ SDK 把 SwiftUI 的 @State 实现成宏，宏插件 SwiftUIMacros 只随完整
# Xcode 分发；只有 Command Line Tools 时必须回退到仍用 propertyWrapper 的 SDK。
select_sdk() {
  if [[ -n "${AGENTDOCK_MACOS_SDK_PATH:-}" ]]; then
    [[ -d "$AGENTDOCK_MACOS_SDK_PATH" ]] || die "AGENTDOCK_MACOS_SDK_PATH 不存在：$AGENTDOCK_MACOS_SDK_PATH"
    print -r -- "$AGENTDOCK_MACOS_SDK_PATH"
    return
  fi
  local default_sdk plugins candidate interface
  default_sdk="$(xcrun --sdk macosx --show-sdk-path)"
  plugins="$(dirname "$(xcrun -f swiftc)")/../lib/swift/host/plugins"
  if [[ -f "$plugins/libSwiftUIMacros.dylib" ]]; then
    print -r -- "$default_sdk"
    return
  fi
  for candidate in "${(@f)$(ls -d "$(xcode-select -p)"/SDKs/MacOSX*.sdk 2>/dev/null | sort -Vr)}"; do
    interface="$candidate/System/Library/Frameworks/SwiftUICore.framework/Modules/SwiftUICore.swiftmodule/arm64e-apple-macos.swiftinterface"
    if [[ -f "$interface" ]] && ! grep -qs "macro State()" "$interface"; then
      print -r -- "$candidate"
      return
    fi
  done
  die "当前工具链缺少 SwiftUIMacros 插件且无旧 SDK 可回退；请安装完整 Xcode 或设置 AGENTDOCK_MACOS_SDK_PATH"
}

SDK_PATH="$(select_sdk)"
ARCH="$(uname -m)"
print -u2 -- "==> SDK: $SDK_PATH"

print -u2 -- "==> 校验本地化与 Swift 类型"
python3 "$ROOT_DIR/scripts/test/check-macos-i18n.py"
xcrun swiftc -swift-version 5 -typecheck \
  -target "$ARCH-apple-macosx$MIN_VERSION" \
  -sdk "$SDK_PATH" \
  "$ROOT_DIR/desktop/macos/AgentDockApp/Sources"/*.swift

print -u2 -- "==> 构建 Core 与 core-skills 载荷"
rm -rf "$PAYLOAD_ROOT"
mkdir -p "$PAYLOAD_ROOT/bin" "$PAYLOAD_ROOT/share/agentdock" "$PAYLOAD_ROOT/offline"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" go build -trimpath \
    -ldflags "-s -w -X github.com/uvwt/agentdock/internal/buildinfo.Version=$VERSION" \
    -o "$PAYLOAD_ROOT/bin/agentdock" ./cmd/agentdock
)
python3 "$ROOT_DIR/packaging/build-core-skill-bundle.py" \
  --output "$PAYLOAD_ROOT/share/agentdock/core-skills"
archive="agentdock_darwin_${ARCH}.tar.gz"
tar -C "$PAYLOAD_ROOT" -czf "$PAYLOAD_ROOT/offline/$archive" bin/agentdock share/agentdock/core-skills
(cd "$PAYLOAD_ROOT/offline" && shasum -a 256 "$archive" > "$archive.sha256")

print -u2 -- "==> 打包 App（.app / .zip / .dmg）"
AGENTDOCK_MACOS_SDK_PATH="$SDK_PATH" \
AGENTDOCK_MACOS_ARCHES="$ARCH" \
AGENTDOCK_MACOS_APP_OUTPUT_DIR="$OUTPUT_DIR" \
AGENTDOCK_MACOS_OFFLINE_PAYLOAD_DIR="$PAYLOAD_ROOT/offline" \
  "$ROOT_DIR/packaging/macos/build-app.sh" "$VERSION"

if [[ "$BUILD_ONLY" == true ]]; then
  print -u2 -- "==> 仅构建完成：$OUTPUT_DIR/$APP_NAME"
  exit 0
fi

TARGET="$INSTALL_DIR/$APP_NAME"
if [[ ! -w "$INSTALL_DIR" && ! -w "$TARGET" ]]; then
  die "无法写入 ${INSTALL_DIR}；请用有写权限的账号或设置 AGENTDOCK_MACOS_INSTALL_DIR"
fi

print -u2 -- "==> 安装到 $TARGET"
USER_DOMAIN="gui/$(id -u)"
pkill -f "$TARGET/Contents/MacOS/AgentDock" 2>/dev/null || true
launchctl bootout "$USER_DOMAIN/$CORE_LABEL" 2>/dev/null || true
sleep 2
ditto "$OUTPUT_DIR/$APP_NAME" "$TARGET"
codesign --verify --deep --strict "$TARGET"

# ad-hoc 签名重签后 cdhash 变化，SMAppService 里记录的 LWCR 不匹配，launchd 会以
# OS_REASON_CODESIGNING 拒绝 spawn；手工 bootstrap 的 job 不经过该约束。
# launchd 只接受 /tmp 下、扩展名为 .plist 且权限 644 的文件：mktemp 默认落在
# ${TMPDIR}（/var/folders/...）且权限 600，且无 .plist 后缀时 bootstrap 会以
# Input/output error 失败。
BOOTSTRAP_PLIST="$(mktemp /tmp/agentdock-core.XXXXXX).plist"
mv "${BOOTSTRAP_PLIST%.plist}" "$BOOTSTRAP_PLIST"
chmod 644 "$BOOTSTRAP_PLIST"
python3 - "$TARGET" "$BOOTSTRAP_PLIST" <<'PY'
import sys
from pathlib import Path

app, output = sys.argv[1], sys.argv[2]
source = Path(f"{app}/Contents/Library/LaunchAgents/com.uvwt.agentdock.core.plist").read_text()
replaced = source.replace(
    "<key>BundleProgram</key>\n  <string>Contents/Helpers/agentdock</string>",
    "<key>Program</key>\n  <string>" + app + "/Contents/Helpers/agentdock</string>",
)
if replaced == source:
    sys.exit("LaunchAgent 定义缺少 expected BundleProgram，App 打包结构可能已变化")
Path(output).write_text(replaced)
PY

print -u2 -- "==> 加载 Core"
# bootout 与 bootstrap 之间存在短暂的 job 移除窗口，首次失败时清一次再重试。
if ! launchctl bootstrap "$USER_DOMAIN" "$BOOTSTRAP_PLIST" 2>/dev/null; then
  launchctl bootout "$USER_DOMAIN/$CORE_LABEL" 2>/dev/null || true
  sleep 2
  launchctl bootstrap "$USER_DOMAIN" "$BOOTSTRAP_PLIST" \
    || die "无法加载 Core LaunchAgent：$BOOTSTRAP_PLIST"
fi
launchctl kickstart -k "$USER_DOMAIN/$CORE_LABEL" || die "无法启动 Core"

ready=false
for _ in {1..20}; do
  if curl -fsS -m 1 -o /dev/null "http://127.0.0.1:8765/"; then
    ready=true
    break
  fi
  sleep 0.5
done
[[ "$ready" == true ]] || die "Core 未在 10 秒内就绪；用 log show --predicate 'process == \"agentdock\"' --last 2m 排查"

print -u2 -- "==> 启动 App"
open "$TARGET"
print -u2 -- "==> 完成：${TARGET}（${VERSION}）"
