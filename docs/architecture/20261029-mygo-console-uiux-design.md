---
id: "2026-10-29-mygo-console-uiux-design"
title: "MyGo 控制台 UI/UX 设计（只读观测台 7 页）"
status: "proposed"
created: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "uiux", "design", "console", "cross-platform"]
---

# MyGo 控制台 UI/UX 设计

## 0. 范围与依据

本文档是 AgentDock 控制面板 MyGo 跨平台版本（AgentDock Console）的
UI/UX 设计规格，只定界面与交互，不含实现代码。

- 决策依据：`20261029-mygo-cross-platform-ui-adr.md`（增量并存路线）。
- 对等基线：macOS 版 SwiftUI 控制面板
  （`desktop/macos/AgentDockApp/Sources/
  NativeControlPanelWindowController.swift` 4114 行、10 页）
  中的只读观测功能。
- 实现载体：`desktop/console` 独立 Go 模块。数据层
  `internal/runtimeapi` 只读本机 `/internal/runtime/*` HTTP API
  （18 个只读端点），UI 层 `internal/ui` 按页面组织，
  领域薄组件在 `internal/ui/kit`，入口 `cmd/console`。
- 渲染模型：即时模式，每帧重建 view 函数；状态集中在 app struct；
  异步取数走 goroutine，完成后 `win.Invalidate()` 唤醒绘制。
- 页面清单（7 页）：概览 Overview、能力目录 Catalog、任务 Tasks、
  文件变更 Files、发布 Published、追踪 Traces、统计 Analytics。
  macOS 版的连接/能力开关/设置等写页与系统集成不在此列（见 §8）。

## 1. 设计目标与原则

1. **原生观感**：使用 MyGo `ui` 内置构件的默认材质与度量，间距取
   `Theme.Spacing`（默认 4）的倍数，圆角取 `Theme.Radius`，让
   macOS/Windows/Linux 各自跟随后端平台观感，不另造风格。
2. **只读优先**：本期全部页面只消费数据，不产生写操作；控件只用
   查询、筛选、选中、复制、打开链接五类交互。破坏性操作为零。
3. **键盘可达**：所有交互不依赖鼠标——侧栏、表格、树、搜索框、
   工具条按钮均可 Tab 到达并用方向键/回车操作（构件自带）。
4. **三态齐全**：每个数据区必须同时设计空（Empty）、加载（Loading）、
   错误（Error）三态；Core 未运行时显示不可用说明，不空白、不崩溃。
5. **浅深双主题自适应**：颜色全部取自 `c.Theme()`，跟随系统浅/深
   外观自动切换，不硬编码色值，不提供手动主题开关（P2）。

## 2. 整体布局

窗口 1180x740（最小 900x600）。左侧 `ui.Sidebar` 固定分组导航，
右侧内容区 = 页头（标题 + 副标题 + 工具条）+ 主体。主体为全宽
单列页（概览、统计、发布）或 `ui.Split` 左右分栏页
（能力、任务、文件、追踪：左列表/树，右详情）。

侧栏分组（与导航状态一一对应，app struct 单一选中值）：

| 分组 | 页面 | 路由 id |
|---|---|---|
| 观测 | 概览 / 统计 / 追踪 | overview / analytics / traces |
| 资产 | 能力 / 文件 / 发布 | catalog / files / published |
| 工作 | 任务 | tasks |

整窗线框：

```text
+--------------------------------------------------------------------------+
| AgentDock Console                                     [_][ ^ ][ X ]      |
+------------+-------------------------------------------------------------+
|  观测      |  概览  运行状态与最近活动                    [刷新]         |
|   概览     +-------------------------------------------------------------+
|   统计     |                                                             |
|   追踪     |  页头下方：主体区                                           |
|  资产      |                                                             |
|   能力     |  +---------------------------------+---------------------+  |
|   文件     |  | 左栏（列表 / 树 / 图表）        | 右栏（详情）        |  |
|   发布     |  |                                 |                     |  |
|  工作      |  |  全宽页（概览/统计/发布）只用   |  详情随行选中刷新   |  |
|   任务     |  |  这一整条区域，不再分左右栏     |                     |  |
|            |  +---------------------------------+---------------------+  |
|            |                                                             |
| ● 运行中   |                                                             |
| v0.7.1     |                                                             |
+------------+-------------------------------------------------------------+
```

结构说明：

- 侧栏（宽 200 DIP）：`ui.Sidebar` + 三个 `ui.SidebarSection`
  （观测/资产/工作）+ `ui.SidebarItem`；底部固定 Core 状态行
  （kit.StatusChip + 版本号）。
- 页头（高 64 DIP）：kit.PageHeader，左标题 + 副标题，右侧工具条
  （`ui.Toolbar`：刷新按钮、筛选控件、统计文本）。
- 主体：`ui.SplitPercent`（左 34% / 右 66%）用于分栏页；全宽页用
  `ui.Scroll` 单列。
- 页面切换由侧栏选中驱动（`ui.Sidebar` 的 selected 状态），不引入
  路由地址栏；深链与多窗口见 §8。

## 3. 逐页设计

### 3.1 概览 Overview（全宽）

一眼看清：Core 是否在运行、版本与主页目录、资产计数、任务三态计数、
核心能力开关快照（只读）、最近调用活动。功能对等 macOS 版 Home。

```text
+---------------------------------------------------------------------+
| 概览   运行状态与最近活动                              [刷新]       |
+---------------------------------------------------------------------+
| +----------------------------------------------------------------+  |
| |  ● 运行中   AgentDock v0.7.1         ~/.agentdock              |  |
| +----------------------------------------------------------------+  |
| |  Skills 12  |  MCP 3  |  插件 2  |  任务 5/2/1  |  24h 调用 87 |  |
| +----------------------------------------------------------------+  |
| |  核心能力                                                      |  |
| |   浏览器自动化                            [已启用]          >  |  |
| |   记忆                                    [未启用]          >  |  |
| |   远程连接                                [需关注]          >  |  |
| +----------------------------------------------------------------+  |
| |  最近活动                                                      |  |
| |   file_edit  ·  local  ·  2 分钟前               ● 成功        |  |
| |   web_fetch  ·  acp  ·  5 分钟前                 ● 失败        |  |
| +----------------------------------------------------------------+  |
+---------------------------------------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头 | kit.PageHeader + ui.Toolbar |
| 状态横幅（版本/主页/状态） | kit.SectionCard + kit.StatusChip |
| 计数指标行 | kit.MetricCard（ui.Row 排布） |
| 核心能力清单 | ui.List + ui.ListState + kit.StatusChip |
| 行尾跳转箭头 | ui.Icon（Lucide chevron-right） |
| 最近活动列表 | ui.List + ui.Textf + kit.StatusChip |
| 全页滚动 | ui.Scroll / ui.ScrollBoth |

交互：点核心能力行跳转能力页；点最近活动行跳转追踪页并预选该记录；
刷新按钮重新拉取 status/overview/diagnostics。三态：加载显示
ui.Spinner + 骨架占位行；Core 未运行显示 kit.NoticeBanner + 说明文案；
错误显示 kit.StateView 错误态（文案 + 重试按钮）。

### 3.2 能力目录 Catalog（左树右详情）

对等 macOS 侧栏能力目录：工具（含 input/output schema 与注解）、
技能、公共技能、规则、插件（inspect 详情）。只读，不提供开关。

```text
+---------------------------------------------------------------------+
| 能力   提供给 AI 的工具、技能、规则与插件               [刷新]      |
+---------------------------+-----------------------------------------+
| [搜索能力]   [全部 ▾]     |  file_edit                              |
|                           |  读写文件、按内容替换                   |
|  ▼ 工具 (12)              |  +----------------------------------+   |
|     file_edit           * |  | 注解                             |   |
|     web_fetch             |  |  只读 否   破坏性 否             |   |
|  ▶ 技能 (4)               |  |  幂等 否   开放世界 是           |   |
|  ▶ 公共技能 (2)           |  +----------------------------------+   |
|  ▶ 规则 (3)               |  +----------------------------------+   |
|  ▶ 插件 (2)               |  | 输入 schema                      |   |
|                           |  | { "type": "object", ... }        |   |
|                           |  +----------------------------------+   |
|                           |  +----------------------------------+   |
|                           |  | 输出 schema                      |   |
|                           |  | { ... }                          |   |
|                           |  +----------------------------------+   |
+---------------------------+-----------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头 | kit.PageHeader |
| 搜索过滤 | ui.SearchField |
| 类型过滤 | ui.Select / ui.SelectBase |
| 分组目录树 | ui.Tree + ui.TreeItem |
| 左右分栏 | ui.SplitPercent |
| 详情卡（技能/规则/插件） | kit.SectionCard + kit.KeyValueRow |
| 注解是/否徽标 | kit.StatusChip |
| schema / 规则正文 | kit.MonospaceBlock |
| 插件 inspect 异步详情 | ui.Spinner + kit.KeyValueRow |
| 插件告警列表 | ui.Text（Warning 着色） |
| 选中指示 | ui.ListState.Selected / ui.Icon |

交互：搜索即时过滤树节点（本地）；点树节点右侧详情即时切换；
插件详情是二级异步请求（plugins/{name}），返回前显示 ui.Spinner；
schema 与规则内容文本可选中复制。三态：空（kit.EmptyState
"无匹配能力"）；加载（ui.Spinner）；错误（kit.StateView +
Core 不可用文案，见 20261029-console-readonly-pages 验收项）。

### 3.3 任务 Tasks（左分组列表右详情）

对等 macOS TasksView：搜索、状态筛选、状态统计（可点击过滤）、
按项目分组折叠、任务详情（目标/阻塞/条件/步骤/摘要/终审/事件）。

```text
+---------------------------------------------------------------------+
| 任务   12 个任务                                    [刷新]          |
+---------------------------+-----------------------------------------+
| [标题/项目/设备]          |  修复打包流水线                         |
| [全部|进行|阻塞|完成]     |  active · phase:build · desk · mac      |
|                           |  更新 2 分钟前                          |
|  ▼ desk (5)               |  +----------------------------------+   |
|     修复打包流水线        |  | 目标                             |   |
|     补齐遥测字段          |  | 三平台产物统一签名发布           |   |
|  ▶ api (4)                |  +----------------------------------+   |
|  ▶ 未分配 (3)             |  +----------------------------------+   |
|                           |  | 步骤                             |   |
|  进行 5  阻塞 2  完成 5   |  |  ✓ 生成产物                      |   |
|                           |  |  ◐ 签名公证                      |   |
|                           |  |  ○ 上传通道                      |   |
|                           |  +----------------------------------+   |
|                           |  +----------------------------------+   |
|                           |  | 事件（近 30 条）                 |   |
|                           |  | 12:03 step 2 进入公证            |   |
|                           |  +----------------------------------+   |
+---------------------------+-----------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头（N 个任务） | kit.PageHeader |
| 状态统计（可点击） | kit.StatusChip（ui.Button 包裹） |
| 搜索 | ui.SearchField |
| 状态筛选 | ui.Segmented / ui.SegmentedBase |
| 项目分组折叠 | ui.Outline + ui.OutlineState |
| 行（状态点/步骤进度/时间） | kit.StatusChip + ui.Textf |
| 详情卡 | kit.SectionCard + kit.KeyValueRow |
| 步骤状态 | kit.StatusChip（成功/进行/待办） |
| 事件时间戳 | ui.Textf（monospace） |
| 详情滚动 | ui.Scroll / ui.ScrollBoth |

交互：状态统计点击切换筛选（再点还原"全部"）；筛选或搜索时分组
自动全展开，否则按记忆的展开集；点行右侧详情即时切换，任务详情为
二级请求（tasks/{id}）；刷新后选中项消失则清空选中。三态：空
（kit.EmptyState "无任务"）；加载（左列表 ui.Spinner、右详情
ui.Spinner）；错误（kit.StateView + Core 不可用文案）。

### 3.4 文件变更 Files（左路径树右 diff）

对等 macOS FilesView：公共根推导、路径树（变更计数）、同一文件的
多次变更 chip 横排、diff 着色、路径搜索。数据来自 trace 的
file_edit 记录。

```text
+---------------------------------------------------------------------+
| 文件   58 处变更 · memory · 根 /u/demo                  [刷新]      |
+---------------------------+-----------------------------------------+
| [搜索路径]                |  main.go                                |
|                           |  /u/demo/cmd/main.go                    |
|  /u/demo                  |                                         |
|  ▼ cmd (12)               |  [12:03 +42 -7][11:58 +4 -1][11:20 ...] |
|     ▼ internal (8)        |  +----------------------------------+   |
|        ui.go            4 |  | +func NewPanel(w *Window) {      |   |
|        kit.go           3 |  | +  w.Title = "AgentDock"         |   |
|     main.go             4 |  | -  w.Title = "Dock"              |   |
|  ▶ pkg (6)                |  |  }                               |   |
|                           |  +----------------------------------+   |
|                           |  +42 -7   12:03   Edited   [截断]       |
+---------------------------+-----------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头（变更数·模式·根） | kit.PageHeader + kit.StatusChip |
| 路径搜索 | ui.SearchField |
| 公共根 + 路径树 | ui.Outline + ui.OutlineState |
| 树行图标/计数 | ui.Icon + ui.Badge |
| 变更 chip 横排 | kit.StatusChip（变更变体）+ ui.ScrollHorizontal |
| diff 着色行 | kit.DiffText（解析 unified diff 文本） |
| +N/-N 统计 | ui.Textf（Success / Danger） |
| diff 区滚动 | ui.ScrollBoth |
| 双栏对比 diff | ui/editor.DiffView（P1，暂不做，见 §6） |

交互：搜索即时过滤路径（本地）；点树节点右侧显示该路径全部变更，
chip 切换所看变更；diff 行文本可选中；截断标记（truncated）用
Warning 色标注。三态：空（kit.EmptyState "尚无文件变更"）；
trace 关闭（kit.NoticeBanner，提示设置 AGENTDOCK_TRACE_MODE）；
错误（kit.StateView）。

### 3.5 发布 Published（全宽列表）

对等 macOS PublishedView：已发布文件列表（签名链接）、打开/复制链接、
未配置公网地址提示、过期标记与过滤。

```text
+--------------------------------------------------------------------+
| 发布   7 个文件 · https://x.example       [全部|有效|过期]  [刷新] |
+--------------------------------------------------------------------+
| ! 未配置公网地址；配置公网 URL 或隧道后才会出现链接。              |
+--------------------------------------------------------------------+
| 文件            大小    类型    创建     过期    操作              |
| report.zip      2.1 MB  archive 12:03    +2d     [打开] [复制链接] |
| log.txt         14 KB   text    11:40    已过期  [打开] [复制链接] |
| chart.png       88 KB   image   09:12    +1h     [打开] [复制链接] |
+--------------------------------------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头（计数 + 公网基址） | kit.PageHeader + ui.Text（monospace） |
| 过期过滤 | ui.Segmented / ui.SegmentedBase |
| 未配置公网提示 | kit.NoticeBanner（Warning） |
| 文件列表 | ui.Table + ui.TableColumn（Sortable） |
| 归档徽标 | ui.Badge |
| 过期状态 | kit.StatusChip（有效 TextMuted / 过期 Warning） |
| 打开 / 复制链接 | ui.Button + ui.Toast 反馈 |
| 链接文本 | kit.MonospaceBlock（单行模式，可选中） |
| 空态 | kit.EmptyState |

交互：复制链接写系统剪贴板并弹 ui.Toast（约 2s 自动消失）；打开
链接交系统集成层调起默认浏览器（本模块只发指令）；过期行链接
降饱和显示并保留复制。三态：空（kit.EmptyState "无已发布文件"）；
加载（ui.Spinner）；错误（kit.StateView）。

### 3.6 追踪 Traces（左记录右详情）

对等 macOS TracesView：搜索（工具/入参/结果）、工具下拉筛选、
状态筛选、记录列表（工具/来源/耗时/时间/UA/错误码）、详情
（入参出参 JSON、UA、错误、截断提示）。唯一开启 5s 轮询的页面。

```text
+---------------------------------------------------------------------+
| 追踪   231 条 · memory · 保留 3 天                    [刷新]        |
+---------------------------+-----------------------------------------+
| [工具/入参/结果]          |  file_edit                              |
| [工具 ▾] [全部|成功|失败] |  local · 12.4 ms · 12:03:41.220         |
|                           |  UA: codex-cli/1.2 (macOS)              |
|  ● file_edit   12.4 ms    |  +----------------------------------+   |
|     local · 12:03         |  | 入参                             |   |
|  ● web_fetch   840 ms     |  | { "path": "main.go", ... }       |   |
|     acp · 11:58           |  +----------------------------------+   |
|  ○ shell_exec  3.2 s      |  +----------------------------------+   |
|     acp · 11:40  EFAIL    |  | 结果                             |   |
|                           |  | { "bytes": 4210, ... }           |   |
|                           |  +----------------------------------+   |
|                           |  错误: code=EFAIL  输出超时          |  |
+---------------------------+-----------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头（条数·模式·保留期） | kit.PageHeader + kit.StatusChip |
| 搜索（API 查询参数） | ui.SearchField |
| 工具筛选 | ui.Select / ui.SelectBase |
| 状态筛选 | ui.Segmented / ui.SegmentedBase |
| 刷新（加载中禁用） | ui.Button + ui.Spinner |
| 记录列表 | ui.List + ui.ListState |
| 成功/失败圆点 | kit.StatusChip |
| 入参/出参 JSON | kit.MonospaceBlock |
| UA / 错误码 | kit.KeyValueRow + ui.Textf（monospace） |
| 截断提示（8 KiB） | ui.Text（Warning） |

交互：搜索词随 API 查询参数提交（其余页为本地过滤）；筛选变更即
重新拉取；点行右侧详情即时切换；5s 轮询仅本页开启，数据变化时
`win.Invalidate()`，未变化不重绘；刷新期间按钮禁用。三态：空
（kit.EmptyState）；trace 关闭（kit.NoticeBanner，提示
AGENTDOCK_TRACE_MODE）；错误（kit.StateView）。

### 3.7 统计 Analytics（全宽）

对等 macOS AnalyticsView：指标卡（窗口调用/总调用/错误/错误率/
进行中）、工具调用条形图（Top 24）、明细表（调用数/错误数/错误率/
P50/P95/P99）、搜索、三种排序。

```text
+--------------------------------------------------------------------+
| 统计   工具调用计数与耗时分布                        [刷新]        |
+--------------------------------------------------------------------+
| [窗口调用 87] [总调用 2310] [错误 12] [错误率 0.5%] [进行中 1]     |
+--------------------------------------------------------------------+
| [搜索工具]  [调用数|错误率|P95]                          24 个工具 |
|  file_edit   ##############################  87                    |
|  web_fetch   ##################  52                                |
|  shell_exec  #########  27                                         |
|  ...（Top 24，超出显示"共 N 个工具，展示前 24"）                   |
+--------------------------------------------------------------------+
|  工具        调用   错误   错误率   P50     P95     P99            |
|  file_edit     87      1    1.1%   12ms    40ms    90ms            |
|  web_fetch     52      0    0.0%  620ms   1.2s    2.0s             |
+--------------------------------------------------------------------+
```

组件映射：

| 页面元素 | mygo 构件 / kit 组件 |
|---|---|
| 页头 | kit.PageHeader |
| 指标卡行 | kit.MetricCard（ui.Row） |
| 条形图 | ui/chart.Plot + chart.Rect + chart.Axes |
| 图内数值标注 | chart.Anchor + ui.Textf |
| 工具搜索 | ui.SearchField |
| 排序切换 | ui.Segmented / ui.SegmentedBase |
| 明细表 | ui.Table + ui.TableColumn（Sortable） |
| 错误列着色 | ui.Textf（Danger） |
| 耗时格式化 | ui.Textf（monospace） |
| 空数据 | kit.EmptyState |

交互：搜索即时过滤工具；排序切换与表头点击排序共享同一排序
函数（调用数/错误率/P95，同值按工具名稳定排序）；条形与表格同源，
悬停条形显示 tooltip（ui.TooltipBase）。三态：空（kit.EmptyState
"尚无调用记录"）；Core 未运行（kit.NoticeBanner "启动 AgentDock
后查看"）；错误（kit.StateView）。

## 4. 设计令牌

颜色只取 `c.Theme()`（跟随系统浅/深外观），不硬编码色值；允许对
主题色做透明度派生（如选中底色 = Accent 12%），不引入新色相。

| 用途 | Theme 字段 | 典型场景 |
|---|---|---|
| 成功 | Success | 成功状态、正向 diff 行、完成步骤 |
| 失败 | Danger | 失败 trace、错误码、负向 diff 行 |
| 警示 | Warning | 阻塞任务、截断/未配置提示、告警 |
| 主操作 | Accent | 主按钮、选中行、活跃状态、焦点强调 |
| 正文 | Text | 标题与正文 |
| 次要 | TextMuted | 副标题、时间戳、占位符 |
| 面 | Surface | 卡片与控件面（hover/pressed 用 |
|  |  | SurfaceHover / SurfacePressed） |
| 描边 | Border | 卡片描边、分隔线、表格线 |

补充令牌：焦点环 Focus；选中文本 Selection；Tooltip/Toast 用
Inverse + InverseText；滚动条 Scrollbar（宽度随主题）。

间距与圆角：

- 间距一律 `t.Space(n)`（Theme.Spacing，缺省 4）的倍数：
  行内 4 / 卡片内 8 / 区块间 16 / 页头下 12。
- 圆角一律 `Theme.Radius`（缺省 6）：卡片、chip、按钮同值，
  不另设圆角阶梯。
- 窗口默认 1180x740，最小 900x600；侧栏 200 DIP；页头 64 DIP。

字体：

| 用途 | 字体 |
|---|---|
| 正文 / 标签 | system-ui（Theme.Font 留空取系统字体） |
| 数据 / 代码 / 路径 / 时间戳 | monospace（等宽 family） |

字号以 `Theme.FontSize`（13/14）为 1 倍基准：标签 0.85、正文 1、
小节标题 1.1、页头标题 1.4、指标数字 1.6。数字一律等宽数位
（monospace 家族），保证列对齐。

## 5. 交互规范

- **刷新节奏**：进入页面或切换页面即拉取一次；页头「刷新」按钮
  手动刷新（加载中禁用）；**5s 轮询仅追踪页**开启；其余页不轮询。
  请求节流下限由 `internal/runtimeapi` 集中配置（目录 20s、
  trace/任务 5s、健康 2.5s），页面不自建循环。
- **异步取数**：goroutine 内请求，完成后写 app struct 并
  `win.Invalidate()`；刷新后若选中项从结果中消失则清空选中
  （对齐 macOS 行为）。
- **破坏性操作**：本期只读，无破坏性操作。约定：未来写操作页的
  破坏性动作必须 `ui.AlertDialog` 二次确认后才执行（本期只定
  规范，不实现）。
- **行选中即详情**：表格/列表/树选中行后右侧详情即时更新；二级
  详情（任务详情、插件 inspect、trace 入出参）单独请求并显示
  局部 ui.Spinner，不阻塞列表。
- **搜索**：追踪页搜索词随 API 查询参数提交；其余页为本地内存
  过滤，输入即生效（无需回车）。
- **排序**：ui.Table 表头点击排序（Sortable + ListState.SortBy，
  Cmd/Shift 点击为多列排序，构件自带）；统计页用 ui.Segmented
  三选排序（调用数/错误率/P95），两者映射到同一排序函数。
- **链接**：「复制链接」写剪贴板 + ui.Toast 短反馈；「打开」交
  系统集成层调起默认浏览器；过期链接保留复制、打开前提示过期。
- **⌘K 不做**：全局搜索命令面板明确不在本期（P1，另立
  global-search issue）；本期仅页内 SearchField。

## 6. 组件来源决策

统一策略：**MyGo 内置 `ui` + 自研领域薄组件（kit）**，图标内嵌
Lucide SVG（ISC 许可），以 `ui.MustParseSVG` + `ui.Icon` 渲染。
kit 位于 `desktop/console/internal/ui/kit`，只做组合与领域语义，
不重造构件、不覆盖主题度量。

| 需求 | 来源 | 说明 |
|---|---|---|
| 分组导航侧栏 | mygo 内置 | ui.Sidebar / SidebarItem |
|  |  | / SidebarSection |
| 页头（标题+工具条） | kit 自研 | PageHeader = Box 组合 |
| 卡片 / 小节 | kit 自研 | SectionCard（Box+Border） |
| 键值行 | kit 自研 | KeyValueRow |
| 状态徽标 / 变更 chip | kit 自研 | StatusChip（组合 Badge） |
| 指标卡 | kit 自研 | MetricCard |
| JSON / 代码块 | kit 自研 | MonospaceBlock |
| diff 着色行 | kit 自研 | DiffText（解析 unified 文本） |
| 空态 | kit 自研 | EmptyState |
| 三态容器 / 通知条 | kit 自研 | StateView / NoticeBanner |
| 表格（排序/选中） | mygo 内置 | ui.Table + ListState |
| 可折叠分组树 | mygo 内置 | ui.Outline / ui.Tree |
| 搜索框 | mygo 内置 | ui.SearchField |
| 分段筛选 / 下拉 | mygo 内置 | ui.Segmented / ui.Select |
| 左右分栏 | mygo 内置 | ui.SplitPercent |
| 条形图 | mygo 内置 | ui/chart.Plot 等 |
| 进度/加载/计量 | mygo 内置 | ui.Spinner / Progress / Meter |
| 反馈浮层 | mygo 内置 | ui.Toast / TooltipBase |
| 图标 | 内嵌资源 | Lucide SVG（ISC）+ ui.Icon |
| 双栏对比 diff | 暂不做 | ui/editor.DiffView 需结构 |
|  |  | 化 diff 模型，P1 再评估 |
| 命令面板 / 全局搜索 | 暂不做 | P1 另立 global-search issue |
| 聊天组件 | 暂不做 | 非控制台需求 |
| 手动主题切换 | 暂不做 | 仅系统外观自适应，P2 |
| 托盘 / 窗口菜单 | 暂不做 | 系统集成留原生壳（ADR） |
| onevra 组件库 | 不引入 | 见下方否决理由 |
| 宫廷哥特组件库 | 不引入 | 见下方否决理由 |

否决理由（评估有据，均不引入第三方组件库）：

- **onevra**（Oneo 的 MyGo 组件库，258 star；含 Lucide 图标、
  命令面板、聊天组件）：处于 0.x 快速迭代，API 与视觉不稳；
  其设计原则是"MyGo 官方补齐后即移除自有版本"，依赖面会随
  上游演进而反复变化；整包引入与"薄组件"策略冲突。否决。
- **ELY-Open-Source/MujicaUI**（宫廷哥特风格，
  12 star）：视觉调性与运维控制台不符；社区规模小、维护
  不确定。否决。

## 7. 可访问性与键盘

- **焦点环**：一律由主题 `Focus` 色由构件绘制，kit 不自绘、不隐藏。
- **语义 Role**：使用 mygo 构件自带的 Role（Button/Table/Tree/
  Sidebar/SearchField 等）；kit 只组合构件，不重写 Role，不把
  可点区域伪装成静态文本。
- **文本可选中**：正文、JSON、路径、URL、diff 行、UA、错误信息
  全部可选中复制（构件文本选择能力）。
- **不自造光标样式**：悬停/拖拽/文本输入光标全部用构件默认，
  禁止自定义 Cursor。
- **键盘路径**：Tab 顺序按构建顺序；侧栏方向键切换 + 回车进入；
  表格/列表/树方向键移动选中、回车展开或聚焦详情；SearchField
  聚焦即输入、Esc 清空（构件行为）；工具条按钮均可 Tab 到达。
  快捷键只用构件内置，不注册全局快捷键（⌘K 等见 §8）。
- **对比度**：只用主题色对（Text/TextMuted on Surface/Background），
  已由主题保证对比度；不自造低对比配色。
- **文本缩放 / 高对比**：字号基于 `Theme.FontSize` 派生，随系统
  设置变化；不写死像素字号。

## 8. 非目标（P1/P2 留白）

| 项 | 阶段 | 说明 |
|---|---|---|
| 全局搜索命令面板（⌘K） | P1 | 另立 global-search issue |
| 写操作页（能力开关、任务操作、发布撤销等） | P1 | 另立 console-write-pages |
| 多设备 / 机群视图 | P2 | 另立 fleet-view issue |
| 告警与事件订阅 | P2 | 另立 events-and-alerts issue |
| 系统集成（托盘/服务/权限/更新） | P1 | 另立 system-integration |
| SSE / 推送刷新 | P2 | 数据层先轮询（console-data-layer 决策） |
| 深链、多窗口 | P2 | 待 mygo-shell-spike 结论 |
| 手动主题切换、i18n 文案框架 | P2 | 本期仅系统外观自适应 |

## 关联

- ADR：`2026-10-29-mygo-cross-platform-ui-adr.md`
- Issue：`docs/issues/跨平台UI/20261029-mygo-console-implementation.md`、
  `20261029-console-readonly-pages.md`、`20261029-console-data-layer.md`
- 对等基线：`desktop/macos/AgentDockApp/Sources/
  NativeControlPanelWindowController.swift`
- 构件核对：mygo v0.7.1 `ui` 包（69 内置构件）与 `ui/chart`、
  `ui/editor` 源码
