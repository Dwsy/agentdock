# AgentDock MCP Tool Token Efficiency Audit

- **项目目录：** `~/Dev/AI/agentdock`
- **审计日期：** 2026-10-29
- **范围：** 32 个内置 MCP 工具、主要输入/输出 Schema、关键处理器、共享协议契约与测试
- **方式：** 只读静态审计；审计期间未修改项目源码
- **验证：** `go test ./...` 全部通过；代码审计期间工作树干净。报告文件是本次唯一新增的未跟踪文件

## 1. 执行摘要

当前 MCP 工具设计中存在多个可明确优化的 token 消耗点，主要集中在：

1. **返回模型已经知道的内容**：编辑 Patch 后原样返回 Patch、写入后返回完整文档、同一组数据塞进多个字段。
2. **默认输出预算过大**：文件读取默认 256 KiB、命令 stdout/stderr 各默认 64 KiB、ACP 事件默认每页 100 条。
3. **Schema 与协议封装冗余**：MCP 结果同时包含结构化数据和格式化 JSON 文本；动态 MCP 调用还会嵌套重复上游内容；部分工具的输入/输出 Schema 重叠。

| 优先级 | 问题 | 预期收益 |
|---|---|---|
| P1 | `file_edit` 默认返回大型 `diff_preview`，Patch 输入还会被回显 | 编辑类调用直接减少输出 |
| P1 | `recall_write` 返回 Diff 加完整文档，文档字段可能重复 | 减少长文档修改的输出 |
| P1 | `mcp_tool_call` 嵌套上游原始结果，同时再次返回上游 `content` | 减少动态 MCP 调用中的重复数据 |
| P1 | `recall_maintain` 列表缺少本地默认上限，部分长度参数缺少硬上限 | 避免不可控的大响应 |
| P1 | MCP 通用响应同时包含 `structuredContent` 和 JSON 文本 | 系统性减少冗余序列化和潜在重复上下文 |

P1 表示建议优先处理，不代表已经测得确定的 token 节省比例。真实收益需要用目标宿主的 tokenizer 和调用记录验证。

## 2. 详细发现

### P1-1：`file_edit` 的 `diff_preview` 默认过于激进

**证据：** `internal/tool/file/contract.go:67-83`、`internal/tool/file/git_apply_patch.go:15-52`、`internal/tool/file/patch.go:139-148`

当前行为：
- `max_diff_bytes` 默认 65,536 字节（64 KiB），最大允许 4 MiB。
- 普通 Git Patch 路径会把输入 Patch 作为 `diff_preview` 返回，等于把模型刚提交的内容再发回去。
- 结构化 Patch 路径会生成实际文件差异预览。
- 结构化 Patch 分支把预览上限写死为 65,536 字节，没有使用请求里的 `max_diff_bytes`。

**建议：**
1. 写入成功时默认只返回 `summary`、`affected_files`、`files_changed`、`insertions`、`deletions` 等回执信息。
2. 对输入 Patch 不默认回显原始文本。
3. `dry_run=true` 时保留预览能力，但默认限制在约 4 KiB。
4. 增加显式 `include_diff_preview` 参数，只有确实需要检查差异时才返回。
5. 修复结构化 Patch 分支忽略 `max_diff_bytes` 的问题，并增加测试。

不要彻底删除 Diff：检查实际修改、排查错误和 dry-run 时仍然需要它。问题是默认返回过多，而不是 Diff 本身没有价值。

### P1-2：MCP 通用响应重复序列化结果

**证据：** `internal/mcp/server.go:297-310,350-355`

普通工具响应会同时返回：
- `structuredContent`：结构化结果。
- `content`：通过 `json.MarshalIndent` 生成的格式化 JSON 文本。

同一份结果因此有两种表示。对于大型文件、命令输出和 Diff，这会增加传输数据；如果宿主把两种表示都纳入模型上下文，还会产生重复 token。格式化 JSON 的缩进与换行也增加了文本体积。

**建议：**
1. 先把文本回退表示改为紧凑 JSON，减少无意义的缩进。
2. 对支持结构化输出的客户端，优先消费 `structuredContent`，避免把相同 JSON 文本重复传入模型。
3. 验证宿主行为后，再评估可选文本回退模式；不要未经兼容性验证就直接删掉文本内容。
4. 增加输出字节统计，观察实际宿主是否消费两种表示。

这项改造覆盖整个内置 MCP 响应路径，潜在收益广，但兼容性风险也较高。

### P1-3：`mcp_tool_call` 再次嵌套上游内容

**证据：** `internal/mcp/server.go:313-325` 及 `internal/tool/mcp/service_actions.go` 中的动态调用路径。

当前结果可能同时包含外层 `structuredContent` 中的完整上游 `result`、上游 `result.content` 和 `result.structuredContent`，以及外层再次转发的上游 `content`。上游文本因此可能出现两次。上游返回图片时，嵌套结果还可能包含 Base64 图片数据，放大重复开销。相比之下，`view_image` 已有专门逻辑，不把内部 Base64 放进结构化结果。

**建议：**
- 默认返回规范化结果：`name`、`isError`、上游结构化结果（如有）和必要元数据。
- 不要把完整上游 `content` 再嵌入结构化结果。
- 如确实需要原始响应，增加显式 `include_raw_result` 选项。
- 图片、音频等大型二进制内容优先使用引用或 Artifact 机制，避免反复把 Base64 当普通文本序列化。

### P1-4：`recall_write` 写入成功后返回 Diff 和完整文档

**证据：** `internal/tool/recall/memory.go:51-70`、`internal/tool/recall/memory_edit.go:97-115`

`recall_read` 已有较好的精简策略：默认移除重复的 `content`、`raw_content`，只有显式请求原文时才返回完整内容。写入路径没有完全复用这一策略。

`memoryPatch` 成功后会同时返回 `diff`、`path`/`changed`/`dry_run`/`confirmed` 等状态，以及完整的 `writeResult["recall"])。后端 Recall 对象可能同时带有 `content` 和 `body`，因此同一份文档可能重复返回。模型刚请求修改文档，工具又把差异和全文一起交回来。

**建议：**
1. 默认只返回写入回执：`path`、`changed`、`written`、`size_bytes`、`operation_count`、`change_count`。
2. Diff 默认返回短预览，或由显式选项控制。
3. 完整文档只在 `include_raw=true` 时返回。
4. 写入结果复用读取路径的文档压缩逻辑，避免 `content` 和 `body` 同时暴露。
5. 对 `max_bytes` 同时设置运行时硬上限和 Schema 上限。

当前编辑 Diff 默认上限约为 60,000 字节，但共享输入 Schema 对 `max_bytes` 没有最大值约束。

### P1-5：`recall_maintain` 存在输出上限缺口

**证据：** `internal/tool/recall/recall.go:155-177`、`internal/tool/recall/memory.go:27-35`、`internal/tool/recall/memory_edit.go:204-240`，以及 `agentdock-protocol v0.9.0` 的 `mcpcontract/input.go:39-48`。

`action=list` 没有传入 `max_entries` 时，本地使用 0；而 `memoryList` 只有在值大于 0 时才会把参数传给后端，因此 AgentDock 本身没有保证默认列表大小，实际限制取决于后端。`action=lint` 默认最多扫描 200 个条目、返回 200 个 finding，但相关 Schema 参数是普通整数，没有最大值约束。

**建议：**
- `list` 默认 100 条，硬上限 500 条。
- `lint` 默认 50 个 finding，硬上限 200 个。
- 在运行时做兜底限制，不只依赖模型遵守描述。
- 返回 `truncated` 和 `next_cursor` 或等价的继续读取信号。

相关输入 Schema 位于独立的 `agentdock-protocol` 依赖，需要同步更新共享协议包并升级依赖。

## 3. 默认输出预算过大

以下是建议的起始参数，不是已在生产负载下验证的最优值。

| 工具 | 当前默认 | 建议初始值 |
|---|---|---|
| `read_file` | 256 KiB；最大 4 MiB | 默认 32 KiB，按需扩大 |
| `exec_command` | stdout、stderr 各 64 KiB | 共享的 16 KiB 总预算 |
| `search_text` | 100 个匹配项；最大 1000 | 默认 25 个，最大 200 |
| `list_dir` | 200 个条目；最大 5000 | 默认 50 个条目 |
| `acp_prompt action=events` | 默认 100 条；最大 200 条 | 默认 20 条 |
| `private_note_manage action=read` | 默认 256,000 字节；最大 1 MiB | 默认 64 KiB，按需扩大 |
| `browser_act` / `browser_snapshot` | 8000 字符、40 个交互元素 | 默认 3000 字符、20 个元素 |

### 3.1 `read_file`

**证据：** `internal/tool/file/contract.go:41`、`internal/tool/file/runtime_files.go:73-78`

默认输出上限为 262,144 字节，最大 4 MiB。常见代码定位任务通常只需要一个函数或一个文件片段。建议默认值降至 32 KiB，复用已有的 `start_line`、`end_line`、`next_start_line` 分段读取能力。

### 3.2 命令输出

**证据：** `internal/tool/command/command.go:206-208`、`internal/tool/command/session/session.go:583-595`

stdout 和 stderr 分别应用 65,536 字节上限，因此默认合计可能达到 128 KiB；Schema 中的 `max_output_bytes` 容易被理解为总预算。建议将两条流纳入同一总预算，默认 16 KiB。超限时保留截断标志、字节数和继续读取能力；大结果可先写文件再按需读取。

### 3.3 `search_text`

**证据：** `internal/tool/file/search.go:213-228`

每个匹配项同时返回 `preview` 和 `match_text`，两者都可能达到 500 字符，还会带上 `before`、`after`、`context_start_line`、`context_end_line`。建议默认只返回 25 个匹配项及短 `snippet`；如果 `match_text` 已包含在预览中则省略重复文本。仅在显式请求上下文行时返回上下文数组和范围字段。

### 3.4 `list_dir`

**证据：** `internal/tool/file/contract.go:113-129`

每个条目都包含 `name`、`path`、`type`、`size_bytes`、`modified`、`is_hidden`。默认浅层列表里 `name` 和 `path` 经常重复；不包含隐藏文件时，`is_hidden` 也经常恒为 false。建议默认返回 `path`、`type`；详细模式再返回大小和修改时间等元数据。递归模式仍须保留必要的相对路径信息。

### 3.5 ACP 事件

**证据：** `internal/tool/acp/contract.go`、`internal/tool/acp/actions.go:384-402`、`internal/acp/types.go:13-18,346-374`

事件接口默认返回 100 条、最多 200 条。事件保留上限为 4 MiB，单次事件更新可保留至 512 KiB，因此事件页可能很大。建议默认每页 20 条，并对单次事件文本设 32–64 KiB 输出预算。超出时返回截断元数据，使用已有的 `after_seq`、`next_seq` 继续读取。保留当前增量游标设计。

## 4. 重复字段与 Schema 重叠

### 4.1 `workflow_template_manage list` 重复返回模板列表

**证据：** `internal/tool/task/workflow_actions.go:63-76`

代码将规范化后的 `items` 赋给 `templates`，但没有删除原来的 `items`。同一模板列表因此出现两次，而输出 Schema 主要描述 `templates`。建议规范化成功后删除原始 `items`，只保留一个规范字段。

### 4.2 `plugin_manage inspect` 重复返回插件信息

**证据：** `internal/tool/plugin/service.go:155-164`

`name`、`version`、`enabled`、`package_digest` 同时出现在顶层和嵌套的 `plugin` 对象中。建议选择一种规范表示：完整对象承载详细信息时，顶层只保留必要摘要，不要重复完整字段。

### 4.3 `task_create` 与 `task_manage` 的 Schema 重叠

**证据：** `internal/tool/task/contract.go:54-83,125-130`

项目已经有独立的 `task_create`，但 `task_manage` 仍包含 `action=create` 及创建任务的全部字段。此外，两者共用 `ManageOutputSchema`，使 `task_create` 也暴露管理操作才需要的 `tasks`、`final_review`、`review_status` 等输出字段。

**建议：** 为 `task_create` 定义独立且精简的输出 Schema；在兼容性允许的版本中，将它作为唯一创建入口；迁移期保留 `task_manage action=create`，避免破坏现有客户端。

### 4.4 `mcp_manage refresh` 返回完整工具列表

**证据：** `internal/tool/mcp/service_actions.go:81-87`

`refresh` 同时返回 `tools` 数组和可从数组长度推导的 `tool_count`。上游工具较多时，完整列表可能很大。建议默认只返回刷新状态和工具数量；需要查看工具时使用已有的 `mcp_tool_search` 或 `mcp_tool_inspect`。确需完整列表时再启用 `include_tools=true`。

### 4.5 `workflow_template_manage vector_index` 返回原始索引 JSON

**证据：** `internal/tool/task/workflow_actions.go:79-89`

原始索引 JSON 被放进 `content`，大型索引可能形成不必要的大文本响应。建议默认返回索引可用性、状态、条目数量和模型名称；明确请求导出或检查内容时才返回完整 JSON。

## 5. 工具定义本身的上下文开销

**证据：** `internal/mcp/server.go:248-257`

工具目录会为定义暴露 `inputSchema` 和 `outputSchema`。这是工具定义加载阶段的开销，不同于每次调用的结果开销；是否全部进入模型提示词取决于宿主客户端。

较值得关注的联合 Schema 包括：`task_manage`、`acp_session`、`mcp_manage`、`recall_write`、`private_note_manage`、`workflow_template_manage`。

建议先测量实际 `tools/list` 的 token 化大小，再精简重复描述、让输出 Schema 与实际行为一致。不要为了减少字段盲目拆分所有工具；拆分同样会增加工具名称、描述和选择空间。

## 6. 32 个内置 MCP 工具盘点

| 类别 | 工具 |
|---|---|
| 上下文 | `agentdock_context`、`workspace_context` |
| 文件与命令 | `exec_command`、`session_observe`、`session_act`、`read_file`、`list_dir`、`search_text`、`file_edit`、`file_publish`、`view_image` |
| 浏览器 | `browser_session`、`browser_act`、`browser_snapshot` |
| 动态 MCP | `mcp_manage`、`mcp_tool_search`、`mcp_tool_inspect`、`mcp_tool_call` |
| 插件与 Skill | `plugin_manage`、`skill_manage` |
| ACP | `acp_session`、`acp_prompt`、`acp_interaction` |
| Task 与工作流 | `task_create`、`task_manage`、`workflow_template_manage` |
| Recall 与知识管理 | `evolve`、`recall_search`、`recall_read`、`recall_write`、`recall_maintain`、`private_note_manage` |

这里的 32 个是项目注册的内置工具，不包括运行时连接的第三方 MCP 服务所提供的上游工具。

## 7. 建议保留的好设计

- **`recall_read`**：默认移除重复的 `content`、`raw_content`，仅显式请求时返回原文。
- **`private_note_manage`**：搜索默认只返回元数据，不搜索或返回明文内容。
- **`mcp_tool_search`**：默认只返回轻量工具摘要；完整 Schema 通过 `mcp_tool_inspect` 按需读取。
- **`task_manage list`**：使用精简任务摘要，而不是返回每个任务的完整状态。
- **`view_image`**：图片 Base64 不重复放入结构化结果。
- **`read_file`**：提供截断状态和 `next_start_line`，支持分段读取。
- **ACP 事件接口**：提供序号游标和分页能力。
- **浏览器快照**：已有文本长度和交互元素数量上限。

原则是保留必要的状态、证据和继续读取能力，减少重复正文和不必要的默认展开。

## 8. 推荐改造顺序

### 第一阶段：高收益、尽量不破坏兼容性

1. 精简 `file_edit` 默认输出，避免回显原始 Patch；修复结构化 Patch 忽略 `max_diff_bytes` 的问题。
2. 精简 `recall_write` 写入回包，默认不再返回完整文档。
3. 规范化 `workflow_template_manage list`，删除重复的 `items`。
4. 将 MCP JSON 文本回退改为紧凑序列化，并核实宿主是否重复消费两种响应表示。
5. 为 `mcp_tool_call` 增加规范化结果和输出大小限制。

### 第二阶段：统一输出预算

1. 降低 `read_file`、`exec_command`、`search_text`、`list_dir` 的默认输出量。
2. 为 ACP 事件设置更小的默认分页和事件文本输出上限。
3. 为 `recall_write.max_bytes`、`recall_maintain.max_entries)、`max_findings` 设置 Schema 上限和运行时硬限制。
4. 为大型输出统一返回 `truncated`、实际字节数和继续读取方式。

### 第三阶段：Schema 与度量体系

1. 缩小 `task_create` 输出 Schema。
2. 评估 `task_manage action=create` 的兼容迁移。
3. 精简其他动作联合 Schema 中重复的描述。
4. 采集工具输出字节数、截断状态、`content` 与 `structuredContent` 序列化大小及估算 token 数；不要为了度量而记录敏感正文。

## 9. 验证结果与限制

- 已执行 `go test ./...`，全部通过，耗时约 81 秒。
- 审计开始前 `git status --short` 为空；审计未修改项目源码。报告文件是随后新增的唯一未跟踪文件。
- 报告依据静态源码、Schema、测试及字节上限分析。
- 尚未接入实际宿主 tokenizer 或生产调用日志，因此没有声称某项改造能精确节省多少 token。
- 审计期间未修改项目源码；当前唯一新增内容是本报告文件 `docs/audits/mcp-tool-token-efficiency-audit-2026-10-29.md`。

## 10. 最终建议

第一轮不要大规模重构工具体系。先修复 `file_edit`、`recall_write`、动态 MCP 响应中的重复数据，再统一默认输出预算和硬上限。这些改造对 token 消耗的改善最直接，也最容易在保留现有工具名称与调用方式的前提下验证。
