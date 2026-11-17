#!/bin/zsh
# 构建随 Core 分发的只读 Web 仪表盘，并把产物放进 go:embed 目录。
# Core 的 go build 会把 internal/httpx/dashboard/dist 打进二进制。dist 产物
# 随仓库提交，所以不跑本脚本也能编译；本脚本用于前端改动后刷新产物。
set -euo pipefail

ROOT_DIR="${0:A:h:h:h}"
DASHBOARD_DIR="$ROOT_DIR/desktop/web-dashboard"
EMBED_DIR="$ROOT_DIR/internal/httpx/dashboard/dist"

die() { print -u2 -- "ERROR: $*"; exit 1 }

# CI 或仅需 Swift/Go 类型检查时可跳过；跳过时用仓库里已提交的 dist 产物。
if [[ "${AGENTDOCK_SKIP_DASHBOARD:-0}" == "1" ]]; then
  print -u2 -- "==> 跳过 Web 仪表盘构建（AGENTDOCK_SKIP_DASHBOARD=1）"
  exit 0
fi

command -v pnpm >/dev/null 2>&1 || die "缺少 pnpm；无法构建 Web 仪表盘。可设置 AGENTDOCK_SKIP_DASHBOARD=1 跳过。"
[[ -d "$DASHBOARD_DIR" ]] || die "缺少仪表盘源码目录：$DASHBOARD_DIR"

print -u2 -- "==> 安装 Web 仪表盘依赖（pnpm）"
(
  cd "$DASHBOARD_DIR"
  pnpm install --frozen-lockfile
  print -u2 -- "==> 构建 Web 仪表盘（pnpm build）"
  pnpm build
)

[[ -f "$DASHBOARD_DIR/dist/index.html" ]] || die "仪表盘构建未产出 dist/index.html"

print -u2 -- "==> 同步 dist 到 go:embed 目录"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
ditto "$DASHBOARD_DIR/dist" "$EMBED_DIR"
find "$EMBED_DIR" -type d -exec chmod 0755 {} +
find "$EMBED_DIR" -type f -exec chmod 0644 {} +

print -u2 -- "==> Web 仪表盘就绪：$EMBED_DIR/index.html"
