export type Status = {
  ok: boolean;
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
  tasks: { active: number; completed: number; blocked: number; active_recent_24h: number };
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
  window: { from: string; to: string; covered_from: string; limited_by?: 'restart' | 'capacity' };
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
  started_at: string;
  total_calls: number;
  total_errors: number;
  window_calls: number;
  active_calls: number;
  total_input_tokens: number;
  total_output_tokens: number;
  process: ProcessSnapshot;
  tool_stats: ToolStat[];
  recent_calls: RecentCall[];
  daily_tokens: DailyToken[];
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

export type Alerts = {
  ok: boolean;
  source: string;
  alerts: Alert[];
  resolved_count: number;
  rules: unknown[];
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

export type Tasks = { ok: boolean; source: string; action: string; count: number; tasks: Task[] };

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

export type TaskActivity = {
  ok: boolean;
  source: string;
  task_id: string;
  mode: string;
  call_count: number;
  calls: TraceRecord[];
  change_count: number;
  file_changes: FileChange[];
};

export type TraceDetail = {
  ok: boolean;
  source: string;
  record: TraceRecord;
  full?: { arguments?: string; result?: string; error?: string; command?: { command?: string; stdout?: string; stderr?: string } } | null;
  full_available?: boolean;
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
  };
  // file_edit 的结构化文件变更（与 /file-changes 同源），有 diff 可直接渲染。
  file_changes?: FileChange[];
};

export type Traces = {
  ok: boolean;
  source: string;
  count: number;
  status: string;
  view: string;
  tools: string[];
  records: TraceRecord[];
};

export type FileChange = {
  id: number;
  tool: string;
  source: string;
  started_at: string;
  success: boolean;
  path: string;
  action: string;
  project_root: string;
  insertions: number;
  deletions?: number;
  diff: string;
};

export type FileChanges = { ok: boolean; source: string; count: number; status: string; changes: FileChange[] };

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
  traces: (limit = 50) => get<Traces>(`/traces?limit=${limit}`),
  traceDetail: (id: number, full = false) => get<TraceDetail>(`/traces/detail?id=${id}&full=${full ? 1 : 0}`),
  traceImage: (id: number) => get<TraceImage>(`/traces/image?id=${id}`),
  fileChanges: (limit = 50) => get<FileChanges>(`/file-changes?limit=${limit}`),
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
