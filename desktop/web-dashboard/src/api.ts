export type Status = {
  ok: boolean;
  source?: string;
  /** 服务端生成该响应的时间；缺失表示接口未提供，不能回退成页面刷新时间。 */
  generated_at?: string;
  service: string;
  version: string;
  agentdock_home: string;
  agentdock_default_dir: string;
  path_model: string;
  auth_enabled: boolean;
  browser_enabled: boolean;
  manual_llm_enabled?: boolean;
  memory_enabled: boolean;
  nexus_enabled: boolean;
  tool_count: number;
  tools: string[];
};

export type Overview = {
  ok: boolean;
  source: string;
  /** 服务端统计生成时间。 */
  generated_at?: string;
  tasks: {
    active: number;
    completed: number;
    blocked: number;
    active_recent_24h: number;
    /** 取样范围：扫描到的任务总数、统计上限与「近期活跃」窗口。 */
    scope?: {
      limit?: number;
      matched?: number;
      truncated?: boolean;
      recent_window_hours?: number;
    };
  };
  skills: { count: number };
  plugins: { count: number; available: boolean };
  mcp: { count: number };
};

export type ToolStat = {
  tool: string;
  count: number;
  error_count: number;
  error_rate: number;
  p50_duration_ms: number;
  p95_duration_ms: number;
  p99_duration_ms: number;
  input_tokens: number;
  output_tokens: number;
};

export type RecentCall = {
  id: number | string;
  tool: string;
  source: string;
  user_agent?: string;
  started_at: string;
  duration_ms: number;
  success: boolean;
  input_tokens?: number;
  output_tokens?: number;
  error_code?: string;
};

export type CallRange = '5m' | '1h' | '24h';
export type CallFilterKey = 'tool' | 'source' | 'status' | 'user_agent' | 'task_id' | 'error_code' | 'error_category' | 'stage';
export type CallFilters = Partial<Record<CallFilterKey, string>>;

export type CallMetrics = {
  count: number;
  error_count: number;
  avg_duration_ms: number;
  p50_duration_ms: number;
  p95_duration_ms: number;
  p99_duration_ms: number;
  max_duration_ms: number;
  input_tokens: number;
  output_tokens: number;
  insertions: number;
  deletions: number;
};

export type CallBucket = CallMetrics & { start: string; end: string };
export type CallGroup = CallMetrics & { key: string; category?: string };
export type CallStage = { name: string; started_offset_ms: number; duration_ms: number; success: boolean };
export type AnalyticsCall = RecentCall & {
  completed_at: string;
  task_id?: string;
  trace_id?: string;
  span_id?: string;
  error_category?: string;
  insertions?: number;
  deletions?: number;
  stages?: CallStage[];
};

export type CallAnalytics = {
  ok: true;
  source: 'agentdock-api';
  window: { from: string; to: string; covered_from: string; limited_by?: 'restart' | 'capacity' | 'retention' };
  pagination?: { page: number; page_size: number };
  totals: CallMetrics;
  buckets: CallBucket[];
  by_tool: CallGroup[];
  by_source: CallGroup[];
  by_client: CallGroup[];
  by_task: CallGroup[];
  by_error: CallGroup[];
  by_stage: CallGroup[];
  facets: { tools: string[]; sources: string[] };
  calls: AnalyticsCall[];
};

export type DailyToken = { date: string; input_tokens: number; output_tokens: number };

export type ProcessSnapshot = {
  goroutines: number;
  heap_alloc_bytes: number;
  heap_inuse_bytes: number;
  heap_sys_bytes: number;
  gc_cycles: number;
  uptime_ms: number;
};

export type Analytics = {
  ok: boolean;
  source: string;
  /** 进程启动时间，不是本快照的计算时间。 */
  started_at: string;
  /** 本快照的计算时间；与窗口起点、页面刷新时间都不同。 */
  generated_at?: string;
  /** 本快照对应的 Core 本机自然日，和按日 Token 桶使用相同时区。 */
  local_date?: string;
  total_calls: number;
  total_errors: number;
  window_calls: number;
  active_calls: number;
  total_input_tokens: number;
  total_output_tokens: number;
  /** 窗口调用与工具聚合使用的滚动窗口长度（分钟），由后端下发。 */
  window_minutes?: number;
  /** 窗口明细环保留的历史长度（分钟）。 */
  window_history_minutes?: number;
  /** 窗口明细环的条数上限；超限时窗口统计可能偏低。 */
  window_capacity?: number;
  window_limited?: boolean;
  /** 自然日 token 桶的保留天数。 */
  daily_token_retention_days?: number;
  /** 累计值是否从本地状态文件恢复（包含此前进程的累计）。 */
  totals_restored?: boolean;
  process?: ProcessSnapshot;
  tool_stats?: ToolStat[];
  recent_calls?: RecentCall[];
  daily_tokens?: DailyToken[];
};

export type Alert = {
  id: string;
  severity: string;
  kind: string;
  message: string;
  detail: string;
  first_seen: string;
  last_seen: string;
  active: boolean;
};

export type AlertRule = { kind?: string; threshold?: number; enabled?: boolean; min_calls?: number };

export type Alerts = {
  ok: boolean;
  source: string;
  /** 本次惰性评估的生成时间；resolved_count 只统计本次 Core 进程内的恢复。 */
  generated_at?: string;
  alerts: Alert[];
  resolved_count: number;
  rules: AlertRule[];
};

export type TaskStepRef = { id: string; title: string; status: string };

export type Task = {
  id: string;
  title: string;
  goal: string;
  status: string;
  phase: string;
  review_status: string;
  // 后端列表项按任务自身字段裁剪，以下字段可能缺省（omitempty）。
  project?: string;
  device?: string;
  blocker?: string;
  current_step?: TaskStepRef;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  step_count: number;
  completed_step_count: number;
  event_count: number;
  summary?: string;
};

export type Tasks = {
  ok: boolean;
  source: string;
  action: string;
  /** 本次列表载入的服务端时间。 */
  generated_at?: string;
  /** 本次请求生效的条数上限；count === limit 表示可能还有更早任务未载入。 */
  limit?: number;
  count: number;
  tasks: Task[];
};

export type TaskCondition = { id: string; text: string; created_at: string };
export type TaskStep = { id: string; title: string; phase?: string; status: string; updated_at: string };
export type TaskEvent = { type: string; summary: string; created_at: string };

export type TaskDetail = {
  ok: boolean;
  source: string;
  action: string;
  task: {
    schema_version?: number;
    id: string;
    title: string;
    goal: string;
    project?: string;
    status: string;
    phase: string;
    review_status?: string;
    blocker?: string;
    conditions: TaskCondition[];
    steps: TaskStep[];
    events: TaskEvent[];
    summary: string;
    created_at: string;
    updated_at: string;
    completed_at?: string;
  };
};

/** 任务活动接口的逐文件投影：只有文件维度字段，没有 FileChangeEntry 的调用维度字段。 */
export type TaskActivityChange = {
  trace_id: string;
  tool: string;
  started_at: string;
  path: string;
  action: string;
  /** 活动契约固定下发完整键集合，move 之外为空串。 */
  move_to: string;
  insertions: number;
  deletions: number;
  diff: string;
  /** 调用级标记：同一次调用的所有文件共享一个 diff 预览上限。 */
  truncated: boolean;
};

export type TaskActivity = {
  ok: boolean;
  source: string;
  task_id: string;
  mode: string;
  call_count: number;
  calls: TraceRecord[];
  change_count: number;
  file_changes: TaskActivityChange[];
};

export type TraceDetail = {
  ok: boolean;
  source: string;
  record: TraceRecord;
  full?: {
    arguments?: string;
    result?: string;
    error?: string;
    command?: { command?: string; stdout?: string; stderr?: string };
    /** 整次文件编辑的 diff，文件详情再按路径选出对应分节。 */
    file_diff?: string;
    file_diff_truncated?: boolean;
    /** 全文旁路本身也到 256 KiB/侧上限时置位。 */
    full_truncated?: boolean;
  } | null;
  full_available?: boolean;
};

/** trace 保留窗口状态：旧 Core 可能只给 mode 字符串，调用方需兼容两种形态。 */
export type TraceStatus = {
  mode: string;
  root?: string;
  retention_days?: number;
  max_records: number;
  count: number;
  buffered: number;
};

/** 私有图片 sidecar：data 是标准 base64，前端拼 data URL 直接渲染。 */
export type TraceImage = { ok: boolean; mime_type: string; data: string };

export type TraceRecord = {
  id: number;
  tool: string;
  source: string;
  user_agent?: string;
  // 上游客户端请求头（Nexus 转发而来，键全小写）；不是本机连接的头。
  // 老版本 Core 不下发这个字段，此处必须按可选处理。
  upstream_headers?: Record<string, string>;
  // 图片 sidecar 索引：只有 view_image 一类返回内联图像的工具才有。
  // 字节不在这里，要点开详情时通过 /traces/image 单独拉。
  image_preview?: { mime_type: string; size_bytes: number };
  started_at: string;
  duration_ms: number;
  success: boolean;
  error_code?: string;
  error_category?: string;
  /** 记录级截断标记：任一侧预览被截到 8 KiB 时为 true（summary 视图保留）。 */
  truncated?: boolean;
  arguments?: string;
  result?: string;
  command?: {
    summary?: string;
    command?: string;
    workdir?: string;
    execution_mode?: string;
    timeout_ms?: number;
    session_id?: string;
    status?: string;
    elapsed_ms?: number;
    stdout?: string;
    stdout_bytes?: number;
    stderr?: string;
    stderr_bytes?: number;
    exit_code?: number;
    command_ok?: boolean;
    timed_out?: boolean;
    stdout_truncated?: boolean;
    stderr_truncated?: boolean;
    command_preview_truncated?: boolean;
    stdout_preview_truncated?: boolean;
    stderr_preview_truncated?: boolean;
  };
  // file_edit 的结构化文件变更（与 /file-changes 同源），有 diff 可直接渲染。
  file_changes?: FileChange[];
};

export type Traces = {
  ok: boolean;
  source: string;
  count: number;
  /** 新 Core 返回 TraceStatus；旧 Core 的 mode=off 响应只有 mode 字符串。 */
  status?: TraceStatus | string;
  mode?: string;
  view: string;
  tools: string[];
  records: TraceRecord[];
  /** Web 摘要附带的目录归属，按追踪 ID 索引。 */
  project_paths?: Record<string, string>;
  /** 读写文件调用的文件名，按追踪 ID 索引。 */
  file_names?: Record<string, string[]>;
  /** 是否还有更早的匹配记录；旧 Core 不提供时为 undefined。 */
  has_more?: boolean;
};

export type TraceQueryOptions = {
  limit?: number;
  tool?: string;
  status?: 'success' | 'error';
  outcome?: 'success' | 'error';
  projects?: boolean;
  /**
   * opt-in ID 游标：0 表示游标第一页（无上界，仍按 ID 降序），正数表示
   * 只取更早记录；缺省（undefined）保持旧列表行为。
   */
  beforeId?: number;
  view?: 'summary' | 'full';
};

/** /file-changes 与 trace 记录里的文件变更：后端用 omitempty，空字段可能缺席。 */
export type FileChange = {
  id: number;
  trace_id?: string;
  tool: string;
  source: string;
  user_agent?: string;
  started_at: string;
  success: boolean;
  path: string;
  action: string;
  /** move 的目标路径；其他动作为空。 */
  move_to?: string;
  project_root?: string;
  insertions?: number;
  deletions?: number;
  diff?: string;
  /** 调用级标记：同一次调用的所有文件共享一个 diff 预览上限。 */
  truncated?: boolean;
};

export type FileChanges = {
  ok: boolean;
  source: string;
  count: number;
  status?: TraceStatus | string;
  mode?: string;
  view?: 'summary';
  has_more?: boolean;
  changes: FileChange[];
};

export type ArtifactItem = {
  artifact_id: string;
  filename: string;
  mime_type: string;
  size_bytes: number;
  sha256: string;
  created_at: string;
  expires_at: string;
  archive: boolean;
  width?: number;
  height?: number;
  url: string;
};

export type Artifacts = {
  ok: boolean;
  source: string;
  public_base_url: string;
  configured: boolean;
  artifacts: ArtifactItem[];
  count: number;
};

export type ArtifactPublishPayload = {
  path?: string;
  filename?: string;
  mime_type?: string;
  data_base64?: string;
  retention_seconds?: number;
};

export type ArtifactMutationResult = {
  ok: boolean;
  source: string;
  action: 'publish' | 'extend' | 'revoke' | string;
  artifact_id?: string;
  artifact?: ArtifactItem;
};

export type SkillItem = {
  name: string;
  skill?: string;
  description: string;
  source_type: string;
  skill_ref: string;
  file_count?: number;
  content_digest?: string;
};

export type Skills = { ok: boolean; source: string; action: string; count: number; skills: SkillItem[] };

export type PluginItem = { name: string; [k: string]: unknown };
export type Plugins = { ok: boolean; source: string; count: number; plugins: PluginItem[] };

export type MCPServer = { name: string; [k: string]: unknown };
export type MCPs = { ok: boolean; source: string; action: string; count: number; servers: MCPServer[] };

export type Catalog = {
  ok: boolean;
  source: string;
  tool_count: number;
  workdir: string;
  workspace_root: string;
  tools: { name: string; title?: string; description: string; read_only_hint?: boolean }[];
  rules: { scope: string; path: string; status: string; size_bytes?: number }[];
  common_skills: { root: string; total: number; truncated: boolean; items: SkillItem[] };
};

export class DashboardRequestError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  // 超时覆盖响应体。外部取消用于停止已经离开或隐藏的页面请求。
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 25_000);
  const abort = () => ctrl.abort();
  if (signal?.aborted) abort();
  signal?.addEventListener('abort', abort, { once: true });
  try {
    const res = await fetch(`/dashboard/api${path}`, { cache: 'no-store', signal: ctrl.signal });
    let data;
    try {
      data = await res.json();
    } catch (error) {
      if (ctrl.signal.aborted) throw error;
      throw new DashboardRequestError(`HTTP ${res.status} · 响应不是 JSON`, res.status);
    }
    if (!res.ok || (data && data.ok === false)) {
      throw new DashboardRequestError(data?.error || data?.code || `HTTP ${res.status}`, res.status);
    }
    return data as T;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', abort);
  }
}

export type AppConfig = {
  ok: boolean;
  source: string;
  values: Record<string, string>;
  keys: string[];
  restartable: boolean;
  config_file: string;
  restart_hint: string;
};

export type ConfigUpdateResult = { ok: boolean; source: string; updated: number; hint: string };
export type ConfigRestartResult = { ok: boolean; source: string; message: string };

async function post<T>(path: string, body: unknown, timeoutMs = 40_000): Promise<T> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  let res: Response;
  try {
    res = await fetch(`/dashboard/api${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      cache: 'no-store',
      signal: ctrl.signal,
    });
  } finally {
    clearTimeout(timer);
  }
  let data: any;
  try { data = await res.json(); } catch { throw new Error(`HTTP ${res.status} · 响应不是 JSON`); }
  if (!res.ok || (data && data.ok === false)) throw new Error(data?.error || data?.code || `HTTP ${res.status}`);
  return data as T;
}

async function del<T>(path: string): Promise<T> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 25_000);
  let res: Response;
  try {
    res = await fetch(`/dashboard/api${path}`, {
      method: 'DELETE',
      cache: 'no-store',
      signal: ctrl.signal,
    });
  } finally {
    clearTimeout(timer);
  }
  let data: any;
  try { data = await res.json(); } catch { throw new Error(`HTTP ${res.status} · 响应不是 JSON`); }
  if (!res.ok || (data && data.ok === false)) throw new Error(data?.error || data?.code || `HTTP ${res.status}`);
  return data as T;
}

export type ManualLLMQueueEntry = {
  request_id: string;
  response_id: string;
  session_id: string;
  model: string;
  status: 'queued' | 'claimed' | 'streaming' | 'completed' | 'failed' | 'cancelled' | string;
  turn: number;
  created_at: string;
  claimed_at?: string;
  completed_at?: string;
  message_count: number;
  new_message_count: number;
  tool_count: number;
  preview?: string;
  copy_prompt: string;
  error_message?: string;
};

export type ManualLLMTuning = {
  preview_bytes: number;
  slice_bytes: number;
  max_slice_bytes: number;
  wait_ms: number;
  max_wait_ms: number;
  max_pending: number;
  cache_max_responses: number;
  cache_ttl_sec: number;
  keepalive_ms: number;
  cache_max_image_mb: number;
  token_count_mode?: 'tiktoken' | 'chars' | string;
};

export type ManualLLMQueue = {
  ok: boolean;
  enabled: boolean;
  responses_path: string;
  mcp_endpoint: string;
  idle_prompt: string;
  tuning?: ManualLLMTuning;
  stats: {
    queued_count: number;
    active_count: number;
    completed_count: number;
    sessions_count: number;
  };
  requests: ManualLLMQueueEntry[];
  /** 实际观测到的模型名（桥接无供应商目录，仅由请求体 model 字段自报推导）。 */
  models?: ModelUsage[];
};

/** 一条已观测模型记录，与 GET /v1/models 的 data 条目同源。 */
export type ModelUsage = {
  id: string;
  requests: number;
  /** 首次出现时间（Unix 秒）。 */
  created: number;
  /** 最后出现时间（RFC3339）。 */
  last_seen: string;
};

export type ManualLLMCancelResult = {
  ok: boolean;
  request: ManualLLMQueueEntry;
};

export type ManualLLMToggleResult = {
  ok: boolean;
  enabled: boolean;
  changed: boolean;
  persisted: boolean;
  persist_note?: string;
};

export type RemoteNexusStatus = {
  paired: boolean;
  error?: string;
  endpoint?: string;
  node_id?: string;
  device_id?: string;
  device_token_stored?: boolean;
  server_edition?: string;
  server_version?: string;
  protocol_version?: string;
  contract_version?: string;
  is_workers?: boolean;
};

export type RemoteTunnelStatus = {
  mode: string;
  running?: boolean;
  ready?: boolean;
  startup_enabled?: boolean;
  public_url?: string;
  dependency_state?: string;
  component_version?: string;
  connection_state?: string;
  recovery_count?: number;
};

export type RemoteAccess = {
  ok: boolean;
  source: string;
  auth_token_configured: boolean;
  configurable: boolean;
  nexus: RemoteNexusStatus;
  nexus_endpoint_env: string;
  nexus_device_identity: string;
  agentdock_home: string;
  tunnel?: RemoteTunnelStatus | null;
  tunnel_error?: string;
};

export type RemoteMutationResult = {
  ok: boolean;
  source: string;
  restart_hint?: string;
  mode?: string;
  paired?: boolean;
  endpoint?: string;
  node_id?: string;
};

export const api = {
  status: () => get<Status>('/status'),
  config: () => get<AppConfig>('/config'),
  configUpdate: (updates: Record<string, string>) => post<ConfigUpdateResult>('/config', updates),
  configRestart: () => post<ConfigRestartResult>('/config/restart', {}),
  overview: () => get<Overview>('/overview'),
  analytics: () => get<Analytics>('/analytics'),
  callAnalytics: async (query: string, signal: AbortSignal) => {
    const data = await get<CallAnalytics>(`/analytics/calls?${query}`, signal);
    if (data?.ok !== true || data.source !== 'agentdock-api' || !data.window || !data.totals || !data.facets ||
        ![data.buckets, data.by_tool, data.by_source, data.by_client, data.by_task, data.by_error, data.by_stage,
          data.facets.tools, data.facets.sources, data.calls].every(Array.isArray)) {
      throw new DashboardRequestError('调用分析响应格式不兼容', 200);
    }
    return data;
  },
  alerts: () => get<Alerts>('/alerts'),
  tasks: (limit = 10000) => get<Tasks>(`/tasks?limit=${limit}`),
  taskDetail: (id: string) => get<TaskDetail>(`/tasks/${encodeURIComponent(id)}`),
  taskActivity: (id: string) => get<TaskActivity>(`/tasks/${encodeURIComponent(id)}/activity`),
  traces: (options: TraceQueryOptions = {}, signal?: AbortSignal) => {
    const params = new URLSearchParams({ limit: String(options.limit ?? 80), view: options.view ?? 'summary' });
    if (options.beforeId !== undefined) params.set('before_id', String(options.beforeId));
    if (options.tool) params.set('tool', options.tool);
    if (options.status) params.set('status', options.status);
    if (options.outcome) params.set('outcome', options.outcome);
    if (options.projects) params.set('projects', '1');
    return get<Traces>(`/traces?${params.toString()}`, signal);
  },
  traceDetail: (id: number, full = false, signal?: AbortSignal) => get<TraceDetail>(`/traces/detail?id=${id}&full=${full ? 1 : 0}`, signal),
  traceImage: (id: number) => get<TraceImage>(`/traces/image?id=${id}`),
  fileChanges: () => get<FileChanges>('/file-changes?view=summary'),
  artifacts: () => get<Artifacts>('/artifacts'),
  artifactPublish: (payload: ArtifactPublishPayload) => post<ArtifactMutationResult>('/artifacts', payload),
  artifactExtend: (id: string, hours: number) => post<ArtifactMutationResult>(`/artifacts/${encodeURIComponent(id)}/extend`, { hours }),
  artifactRevoke: (id: string) => del<ArtifactMutationResult>(`/artifacts/${encodeURIComponent(id)}`),
  artifactContentUrl: (id: string, download = false) =>
    `/dashboard/api/artifacts/${encodeURIComponent(id)}/content${download ? '?download=1' : ''}`,
  skills: () => get<Skills>('/skills'),
  plugins: () => get<Plugins>('/plugins'),
  mcp: () => get<MCPs>('/mcp'),
  catalog: () => get<Catalog>('/catalog'),
  manualLLM: () => get<ManualLLMQueue>('/manual-llm'),
  manualLLMCancel: (request_id: string, reason = 'cancelled from dashboard') =>
    post<ManualLLMCancelResult>('/manual-llm/cancel', { request_id, reason }),
  manualLLMToggle: (enabled: boolean, persist = true) =>
    post<ManualLLMToggleResult>('/manual-llm/toggle', { enabled, persist }),
  remoteAccess: () => get<RemoteAccess>('/remote'),
  remoteTokenUpdate: (token: string) => post<RemoteMutationResult>('/remote/token', { token }),
  nexusPair: (endpoint: string, code: string, name?: string) =>
    post<RemoteMutationResult>('/remote/nexus/pair', { endpoint, code, name }),
  // quick/named 首次配置可能下载 cloudflared 组件，预算放宽到 150 秒。
  tunnelConfigure: (mode: string, serverURL?: string, tunnelToken?: string) =>
    post<RemoteMutationResult>('/remote/tunnel', { mode, server_url: serverURL, tunnel_token: tunnelToken }, 150_000),
};

export type EventKind = 'call' | 'task' | 'artifact' | 'ping';
export type RuntimeEvent = {
  kind: EventKind;
  at: string;
  payload: Record<string, unknown>;
};

/** 订阅 SSE 事件流。返回取消订阅函数。onError 在连接出错时触发（浏览器会自动重连）。 */
export function subscribeEvents(
  onEvent: (e: RuntimeEvent) => void,
  onStatus: (connected: boolean) => void,
): () => void {
  const source = new EventSource('/dashboard/api/events');
  const kinds: EventKind[] = ['call', 'task', 'artifact'];
  const handlers = kinds.map((kind) => {
    const handler = (ev: MessageEvent) => {
      try {
        onEvent({ kind, at: new Date().toISOString(), payload: JSON.parse(ev.data) });
      } catch {
        /* ignore malformed frame */
      }
    };
    source.addEventListener(kind, handler as EventListener);
    return { kind, handler };
  });
  source.onopen = () => onStatus(true);
  source.onerror = () => onStatus(false);
  return () => {
    handlers.forEach(({ kind, handler }) => source.removeEventListener(kind, handler));
    source.close();
  };
}
