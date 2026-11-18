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

async function get<T>(path: string): Promise<T> {
  // 上游 AgentDock 在忙时可能几秒才回甚至超时；加超时避免无限挂起。
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 25_000);
  let res: Response;
  try {
    res = await fetch(`/dashboard/api${path}`, { cache: 'no-store', signal: ctrl.signal });
  } finally {
    clearTimeout(timer);
  }
  let data: any;
  try {
    data = await res.json();
  } catch {
    throw new Error(`HTTP ${res.status} · 响应不是 JSON`);
  }
  if (!res.ok || (data && data.ok === false)) {
    throw new Error(data?.error || data?.code || `HTTP ${res.status}`);
  }
  return data as T;
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

async function post<T>(path: string, body: unknown): Promise<T> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 40_000);
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

export type ManualLLMQueue = {
  ok: boolean;
  enabled: boolean;
  responses_path: string;
  mcp_endpoint: string;
  idle_prompt: string;
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

export const api = {
  status: () => get<Status>('/status'),
  config: () => get<AppConfig>('/config'),
  configUpdate: (updates: Record<string, string>) => post<ConfigUpdateResult>('/config', updates),
  configRestart: () => post<ConfigRestartResult>('/config/restart', {}),
  overview: () => get<Overview>('/overview'),
  analytics: () => get<Analytics>('/analytics'),
  alerts: () => get<Alerts>('/alerts'),
  tasks: (limit = 24) => get<Tasks>(`/tasks?limit=${limit}`),
  taskDetail: (id: string) => get<TaskDetail>(`/tasks/${encodeURIComponent(id)}`),
  taskActivity: (id: string) => get<TaskActivity>(`/tasks/${encodeURIComponent(id)}/activity`),
  traces: (limit = 50) => get<Traces>(`/traces?limit=${limit}`),
  traceDetail: (id: number, full = false) => get<TraceDetail>(`/traces/detail?id=${id}&full=${full ? 1 : 0}`),
  traceImage: (id: number) => get<TraceImage>(`/traces/image?id=${id}`),
  fileChanges: (limit = 50) => get<FileChanges>(`/file-changes?limit=${limit}`),
  skills: () => get<Skills>('/skills'),
  plugins: () => get<Plugins>('/plugins'),
  mcp: () => get<MCPs>('/mcp'),
  catalog: () => get<Catalog>('/catalog'),
  manualLLM: () => get<ManualLLMQueue>('/manual-llm'),
  manualLLMCancel: (request_id: string, reason = 'cancelled from dashboard') =>
    post<ManualLLMCancelResult>('/manual-llm/cancel', { request_id, reason }),
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
