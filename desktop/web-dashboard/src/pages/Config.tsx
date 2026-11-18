import { useEffect, useMemo, useState } from 'react';
import {
  Settings2, Save, RotateCw, AlertTriangle, CheckCircle2, Server,
  Monitor, Bug, FileClock, Wifi, RefreshCw, Info,
} from 'lucide-react';
import { api, type AppConfig } from '../api';
import { Panel, SectionTitle } from '../ui';

type FieldKind = 'bool' | 'int' | 'enum' | 'text';

type Field = {
  key: string;
  label: string;
  kind: FieldKind;
  options?: { value: string; label: string }[];
  hint?: string;
  min?: number;
  max?: number;
};

const FIELDS: { group: string; icon: any; fields: Field[] }[] = [
  {
    group: '服务', icon: Server, fields: [
      { key: 'AGENTDOCK_HOST', label: '监听地址', kind: 'text', hint: '0.0.0.0 允许局域网访问，需配合鉴权' },
      { key: 'AGENTDOCK_PORT', label: '端口', kind: 'int', min: 1, max: 65535 },
      { key: 'AGENTDOCK_LOG_LEVEL', label: '日志级别', kind: 'enum', options: ['debug', 'info', 'warn', 'error'].map(v => ({ value: v, label: v })) },
    ],
  },
  {
    group: '能力', icon: Monitor, fields: [
      { key: 'AGENTDOCK_MCP_APPS_MODE', label: 'MCP Apps 模式', kind: 'enum', options: [{ value: 'full', label: '完整' }, { value: 'compact', label: '紧凑' }, { value: 'off', label: '关闭' }] },
      { key: 'AGENTDOCK_BROWSER_ENABLED', label: '浏览器自动化', kind: 'bool' },
      { key: 'AGENTDOCK_BROWSER_CDP_URL', label: 'CDP 地址', kind: 'text', hint: '留空则自动发现或新启浏览器' },
      { key: 'AGENTDOCK_BROWSER_REUSE_EXISTING_CDP', label: '复用已有浏览器', kind: 'bool' },
      { key: 'AGENTDOCK_ACP_ENABLED', label: 'ACP 编码代理', kind: 'bool' },
      { key: 'AGENTDOCK_ACP_DEFAULT_PROFILE', label: '默认 ACP Profile', kind: 'text' },
      { key: 'AGENTDOCK_MANUAL_LLM_ENABLED', label: '手动推理桥接', kind: 'bool', hint: '开放 /v1/responses（SSE）与精简 2 工具 /mcp/manual；支持热切换，其余调优参数重启生效' },
    ],
  },
  {
    group: '手动推理调优', icon: Monitor, fields: [
      { key: 'AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES', label: '摘要预览上限 (B)', kind: 'int', min: 256, max: 32768, hint: 'wait 单条消息内联预览字节阈值（默认 1800），超出需按需切片' },
      { key: 'AGENTDOCK_MANUAL_LLM_SLICE_BYTES', label: '默认切片大小 (B)', kind: 'int', min: 512, max: 131072, hint: 'history/prompt 默认单次 UTF-8 分片字节数（默认 4096）' },
      { key: 'AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES', label: '最大切片上限 (B)', kind: 'int', min: 512, max: 131072, hint: 'history/prompt 单次 max_bytes 允许上限（默认 16384）' },
      { key: 'AGENTDOCK_MANUAL_LLM_WAIT_MS', label: '默认长轮询 (ms)', kind: 'int', min: 1000, max: 120000, hint: 'llm_wait 默认等待与提示词默认 wait_ms（默认 30000）' },
      { key: 'AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS', label: '最大长轮询上限 (ms)', kind: 'int', min: 1000, max: 120000, hint: 'wait_ms 硬上限，建议低于反向代理/Workers 超时（默认 55000）' },
      { key: 'AGENTDOCK_MANUAL_LLM_MAX_PENDING', label: '待处理队列上限', kind: 'int', min: 1, max: 512, hint: '同时排队或进行中的 /v1/responses 请求数上限（默认 32）' },
      { key: 'AGENTDOCK_MANUAL_LLM_CACHE_MAX_RESPONSES', label: '完成轮次缓存上限', kind: 'int', min: 16, max: 4096, hint: '保留历史切片、previous_response_id 与增量去重缓存（默认 256）' },
      { key: 'AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC', label: '会话缓存保留 (s)', kind: 'int', min: 60, max: 86400, hint: '已完成请求与会话上下文缓存有效期（默认 1800 秒）' },
      { key: 'AGENTDOCK_MANUAL_LLM_KEEPALIVE_MS', label: 'SSE 心跳间隔 (ms)', kind: 'int', min: 1000, max: 60000, hint: '人工推理期间 /v1/responses 保活帧间隔（默认 15000）' },
      { key: 'AGENTDOCK_MANUAL_LLM_CACHE_MAX_IMAGE_MB', label: '历史图片内存上限 (MB)', kind: 'int', min: 1, max: 512, hint: '已完结轮次在内存中保留的图片二进制字节预算（默认 32 MB）' },
      { key: 'AGENTDOCK_USAGE_COUNT_MODE', label: 'Token 计算方式', kind: 'enum', options: [{ value: 'tiktoken', label: 'tiktoken (cl100k_base 默认)' }, { value: 'chars', label: 'chars (按字符数 / 4)' }], hint: '控制可观测统计与手动推理 SSE usage 的 token 估算方式，默认 tiktoken' },
    ],
  },
  {
    group: '追踪与抓包', icon: FileClock, fields: [
      { key: 'AGENTDOCK_TRACE_MODE', label: '追踪模式', kind: 'enum', options: [{ value: 'disk', label: '磁盘' }, { value: 'memory', label: '内存' }, { value: 'off', label: '关闭' }] },
      { key: 'AGENTDOCK_TRACE_RETENTION_DAYS', label: '追踪保留天数', kind: 'int', min: 1, max: 3650 },
      { key: 'AGENTDOCK_NETWORK_CAPTURE_ENABLED', label: '网络抓包', kind: 'bool' },
      { key: 'AGENTDOCK_NETWORK_CAPTURE_MAX', label: '抓包记录上限', kind: 'int', min: 1, max: 100000 },
    ],
  },
];

function FieldRow({ field, value, onChange }: { field: Field; value: string; onChange: (v: string) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border/50 bg-card/40 px-3 py-2.5">
      <div className="min-w-0 flex-1">
        <div className="text-xs font-medium">{field.label}</div>
        {field.hint && <div className="mt-0.5 text-[11px] text-muted-foreground">{field.hint}</div>}
        <div className="mt-0.5 font-mono text-[10px] text-muted-foreground/60">{field.key}</div>
      </div>
      {field.kind === 'bool' ? (
        <button
          onClick={() => onChange(value === 'true' ? 'false' : 'true')}
          className={`relative h-6 w-11 shrink-0 rounded-full transition-colors ${value === 'true' ? 'bg-emerald-500/80' : 'bg-muted'}`}
          role="switch"
          aria-checked={value === 'true'}
        >
          <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all ${value === 'true' ? 'left-[22px]' : 'left-0.5'}`} />
        </button>
      ) : field.kind === 'enum' ? (
        <select value={value} onChange={(e) => onChange(e.target.value)} className="glass-control shrink-0 rounded-md px-2 py-1.5 text-xs outline-none">
          {field.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      ) : (
        <input
          type={field.kind === 'int' ? 'number' : 'text'}
          value={value}
          min={field.min}
          max={field.max}
          onChange={(e) => onChange(e.target.value)}
          className="glass-control w-48 shrink-0 rounded-md px-2 py-1.5 font-mono text-xs outline-none"
        />
      )}
    </div>
  );
}

export default function ConfigPage() {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);

  const load = () => {
    setLoading(true);
    api.config()
      .then((c) => { setConfig(c); setDraft({ ...c.values }); })
      .catch((e) => setMsg({ type: 'err', text: `加载配置失败：${e?.message || e}` }))
      .finally(() => setLoading(false));
  };
  useEffect(() => { load(); }, []);

  const changed = useMemo(() => {
    if (!config) return [] as string[];
    return Object.keys(draft).filter((k) => draft[k] !== config.values[k]);
  }, [config, draft]);

  const setField = (key: string, value: string) => setDraft((d) => ({ ...d, [key]: value }));

  const save = async () => {
    if (!changed.length) return;
    setSaving(true);
    setMsg(null);
    const updates: Record<string, string> = {};
    changed.forEach((k) => (updates[k] = draft[k]));
    try {
      const r = await api.configUpdate(updates);
      setMsg({ type: 'ok', text: `已保存 ${r.updated} 项。${r.hint}` });
      // 回显以新值为准（进程内配置要重启才变，但文件已保存）
      setConfig((c) => (c ? { ...c, values: { ...c.values, ...updates } } : c));
    } catch (e: any) {
      setMsg({ type: 'err', text: `保存失败：${e?.message || e}` });
    } finally {
      setSaving(false);
    }
  };

  const restart = async () => {
    if (!window.confirm('重启 Core 会中断当前正在运行的任务，确定继续？')) return;
    setRestarting(true);
    setMsg(null);
    try {
      const r = await api.configRestart();
      setMsg({ type: 'ok', text: r.message || 'Core 已重启' });
      setTimeout(load, 1500);
    } catch (e: any) {
      setMsg({ type: 'err', text: `重启失败：${e?.message || e}` });
    } finally {
      setRestarting(false);
    }
  };

  if (loading && !config) {
    return <div className="flex items-center justify-center py-24 text-muted-foreground"><RefreshCw size={18} className="mr-3 animate-spin" /> 加载配置…</div>;
  }
  if (!config) {
    return (
      <Panel className="p-8 text-center">
        <AlertTriangle size={28} className="mx-auto text-red-500" />
        <div className="mt-3 text-sm">{msg?.text || '配置不可用'}</div>
        <button onClick={load} className="glass-control mt-4 rounded-lg px-4 py-2 text-sm">重试</button>
      </Panel>
    );
  }

  return (
    <div className="space-y-5">
      <Panel className="p-5">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-base font-semibold">运行配置</h2>
              {changed.length > 0 && <span className="rounded bg-amber-500/15 px-2 py-0.5 text-[11px] text-amber-500">{changed.length} 项未保存</span>}
            </div>
            <p className="mt-1.5 flex items-center gap-1.5 text-xs text-muted-foreground">
              <Info size={12} /> {config.restart_hint}
            </p>
            {config.config_file && <p className="mt-1 font-mono text-[11px] text-muted-foreground/70">{config.config_file}</p>}
          </div>
          <div className="flex gap-2">
            <button onClick={load} className="glass-control flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs text-muted-foreground hover:text-foreground">
              <RefreshCw size={13} /> 重新加载
            </button>
            <button onClick={save} disabled={!changed.length || saving} className="glass-primary flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40">
              <Save size={13} /> {saving ? '保存中…' : '保存'}
            </button>
            <button onClick={restart} disabled={!config.restartable || restarting} className="glass-control flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs text-amber-500 disabled:opacity-40" title={config.restartable ? '重启 Core' : 'Core 未由 launchd 管理，无法从这里重启'}>
              <RotateCw size={13} /> {restarting ? '重启中…' : '重启 Core'}
            </button>
          </div>
        </div>
        {msg && (
          <div className={`mt-4 flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${msg.type === 'ok' ? 'bg-emerald-500/10 text-emerald-500' : 'bg-red-500/10 text-red-500'}`}>
            {msg.type === 'ok' ? <CheckCircle2 size={13} /> : <AlertTriangle size={13} />} {msg.text}
          </div>
        )}
        {!config.restartable && (
          <div className="mt-3 flex items-center gap-2 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-500">
            <AlertTriangle size={13} /> 当前 Core 不是由 launchd 管理，自动重启不可用；保存后请手动重启 Core 生效。
          </div>
        )}
      </Panel>

      <div className="grid gap-5 lg:grid-cols-2">
        {FIELDS.map((group) => (
          <Panel key={group.group} className="p-5">
            <SectionTitle icon={group.icon} title={group.group} hint={`${group.fields.length} 项`} />
            <div className="space-y-2">
              {group.fields.map((f) => (
                <FieldRow key={f.key} field={f} value={draft[f.key] ?? ''} onChange={(v) => setField(f.key, v)} />
              ))}
            </div>
          </Panel>
        ))}
      </div>

      <Panel className="flex items-start gap-2.5 p-4 text-xs text-muted-foreground">
        <Bug size={15} className="mt-0.5 shrink-0" />
        <div>
          敏感项（访问令牌、OAuth 密钥、设备身份、配对码）不在此展示或修改。配置写入 <code className="rounded bg-muted/40 px-1">agentdock.env</code>，保留其它原有键，文件权限 0600。
        </div>
      </Panel>
    </div>
  );
}
