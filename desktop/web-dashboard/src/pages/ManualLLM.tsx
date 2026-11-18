import { useEffect, useState } from 'react';
import {
  Check,
  CheckCircle2,
  Clock,
  Copy,
  Inbox,
  Layers,
  Pause,
  Play,
  Radio,
  RefreshCw,
  Settings2,
  Terminal,
  XCircle,
} from 'lucide-react';
import { api, type ManualLLMQueue } from '../api.ts';
import {
  filterManualEntries,
  isManualEntryActive,
  resolveFullEndpoint,
  statusMeta,
  type QueueFilter,
} from '../lib/manualLLMQueue.ts';
import { Badge, EmptyState, Panel, SectionTitle, StatCard } from '../ui.tsx';

type Props = {
  data: ManualLLMQueue;
  onRefresh: () => Promise<void> | void;
  onOpenConfig: () => void;
};

export default function ManualLLMPage({ data, onRefresh, onOpenConfig }: Props) {
  const [filter, setFilter] = useState<QueueFilter>('all');
  const [autoPoll, setAutoPoll] = useState(true);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [busyID, setBusyID] = useState<string | null>(null);
  const [toggleBusy, setToggleBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    if (!data.enabled || !autoPoll) return;
    const id = setInterval(() => {
      void onRefresh();
    }, 2500);
    return () => clearInterval(id);
  }, [data.enabled, autoPoll, onRefresh]);

  const origin = typeof window !== 'undefined' ? window.location.origin : 'http://127.0.0.1:8765';
  const baseURL = resolveFullEndpoint(origin, '/v1');
  const responsesURL = resolveFullEndpoint(origin, data.responses_path || '/v1/responses');
  const mcpURL = resolveFullEndpoint(origin, data.mcp_endpoint || '/mcp/manual');
  const entries = filterManualEntries(data.requests ?? [], filter);

  const copyText = async (key: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedKey(key);
      setTimeout(() => setCopiedKey((cur) => (cur === key ? null : cur)), 1800);
    } catch {
      setActionError('浏览器剪贴板写入失败，请手动选中复制。');
    }
  };

  const handleCancel = async (requestID: string) => {
    setBusyID(requestID);
    setActionError(null);
    try {
      await api.manualLLMCancel(requestID, 'cancelled from web dashboard');
      await onRefresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : '取消请求失败');
    } finally {
      setBusyID(null);
    }
  };

  const handleToggle = async (enabled: boolean) => {
    setToggleBusy(true);
    setActionError(null);
    try {
      await api.manualLLMToggle(enabled, true);
      await onRefresh();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : '切换手动推理开关失败');
    } finally {
      setToggleBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      {!data.enabled ? (
        <Panel className="border border-amber-500/30 bg-amber-500/10 p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="text-sm font-semibold">手动推理桥接当前未启用</div>
              <p className="mt-1 text-xs text-muted-foreground">
                默认关闭以保持最小暴露面。点击右侧「立即启用」即可热开启（同时同步写入配置并刷新 Nexus 节点工具），无需重启 Core。
              </p>
            </div>
            <div className="flex items-center gap-2">
              <button
                disabled={toggleBusy}
                onClick={() => void handleToggle(true)}
                className="u-press glass-primary flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
              >
                <Play size={14} /> {toggleBusy ? '开启中…' : '立即启用'}
              </button>
              <button
                onClick={onOpenConfig}
                className="u-press glass-control flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium"
              >
                <Settings2 size={14} /> 配置详情
              </button>
            </div>
          </div>
        </Panel>
      ) : (
        <Panel className="flex flex-wrap items-center justify-between gap-3 p-4">
          <div className="text-xs text-muted-foreground">
            手动推理桥接已开启：<span className="font-mono text-foreground">/v1/responses</span> 与 <span className="font-mono text-foreground">/mcp/manual</span> 运行中。
          </div>
          <button
            disabled={toggleBusy}
            onClick={() => void handleToggle(false)}
            className="u-press flex items-center gap-1.5 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1.5 text-xs font-medium text-red-500 hover:bg-red-500/20 disabled:opacity-50"
          >
            <Pause size={13} /> {toggleBusy ? '关闭中…' : '关闭手动推理'}
          </button>
        </Panel>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          icon={Clock}
          label="等待领取"
          value={data.stats?.queued_count ?? 0}
          tone={(data.stats?.queued_count ?? 0) > 0 ? 'amber' : 'default'}
          sub="queued / 等待 llm_wait"
        />
        <StatCard
          icon={Radio}
          label="活跃推理中"
          value={data.stats?.active_count ?? 0}
          tone={(data.stats?.active_count ?? 0) > 0 ? 'sky' : 'default'}
          sub="claimed / streaming SSE"
        />
        <StatCard
          icon={CheckCircle2}
          label="近期已完成"
          value={data.stats?.completed_count ?? 0}
          tone="green"
          sub="保留最近 30 分钟会话"
        />
        <StatCard
          icon={Layers}
          label="活跃会话"
          value={data.stats?.sessions_count ?? 0}
          sub="支持多轮增量去重"
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Panel className="p-5">
          <SectionTitle
            icon={Terminal}
            title="连接端点"
            hint="仅开放流式 SSE /v1/responses 与精简 2 工具 MCP"
          />
          <div className="space-y-3 text-xs">
            <div className="rounded-xl border border-border/50 bg-card/40 p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-muted-foreground">Codex / OpenAI SDK base_url（自动请求 /responses）</span>
                <button
                  onClick={() => copyText('base-url', baseURL)}
                  className="u-press glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px]"
                >
                  {copiedKey === 'base-url' ? <Check size={12} className="text-emerald-500" /> : <Copy size={12} />}
                  {copiedKey === 'base-url' ? '已复制' : '复制'}
                </button>
              </div>
              <div className="mt-1.5 break-all font-mono text-foreground">{baseURL}</div>
              <div className="mt-1 break-all font-mono text-[11px] text-muted-foreground">直连路径：{responsesURL}（仅 stream=true SSE）</div>
            </div>

            <div className="rounded-xl border border-border/50 bg-card/40 p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-muted-foreground">Manual MCP（仅 llm_wait + llm_submit）</span>
                <button
                  onClick={() => copyText('mcp-url', mcpURL)}
                  className="u-press glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px]"
                >
                  {copiedKey === 'mcp-url' ? <Check size={12} className="text-emerald-500" /> : <Copy size={12} />}
                  {copiedKey === 'mcp-url' ? '已复制' : '复制'}
                </button>
              </div>
              <div className="mt-1.5 break-all font-mono text-foreground">{mcpURL}</div>
            </div>
          </div>
        </Panel>

        <Panel className="p-5">
          <SectionTitle
            icon={Copy}
            title="单提示词入口（渐进披露）"
            hint="仅提及 1 个入口工具 llm_wait，零前置规则膨胀"
            action={
              <button
                onClick={() => copyText('idle-prompt', data.idle_prompt)}
                className="u-press glass-primary flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium text-primary-foreground"
              >
                {copiedKey === 'idle-prompt' ? <Check size={13} /> : <Copy size={13} />}
                {copiedKey === 'idle-prompt' ? '已复制提示词' : '复制提示词'}
              </button>
            }
          />
          <pre className="whitespace-pre-wrap break-words rounded-xl border border-border/50 bg-card/40 p-3 font-mono text-xs leading-relaxed text-foreground">
            {data.idle_prompt}
          </pre>
          <p className="mt-2.5 text-[11px] text-muted-foreground">
            将上述提示词粘贴到已连接 <span className="font-mono">/mcp/manual</span> 的对话中即可监听。首轮仅下发新增输入与摘要清单，系统提示词、工具 Schema 与超长工具输出均按需切片读取。
          </p>
          {data.tuning && (
            <div className="mt-2.5 flex flex-wrap gap-1.5 text-[10px] text-muted-foreground">
              <span className="rounded bg-muted/40 px-2 py-0.5 font-mono">预览 {data.tuning.preview_bytes}B</span>
              <span className="rounded bg-muted/40 px-2 py-0.5 font-mono">切片 {data.tuning.slice_bytes}/{data.tuning.max_slice_bytes}B</span>
              <span className="rounded bg-muted/40 px-2 py-0.5 font-mono">轮询 {data.tuning.wait_ms}/{data.tuning.max_wait_ms}ms</span>
              <span className="rounded bg-muted/40 px-2 py-0.5 font-mono">缓存 {data.tuning.cache_max_responses}轮 · {data.tuning.cache_ttl_sec}s · 图{data.tuning.cache_max_image_mb}MB</span>
              <span className="rounded bg-muted/40 px-2 py-0.5 font-mono">队列 {data.tuning.max_pending} · 心跳 {data.tuning.keepalive_ms}ms</span>
              {data.tuning.token_count_mode && (
                <span className="rounded bg-muted/40 px-2 py-0.5 font-mono">Token {data.tuning.token_count_mode}</span>
              )}
            </div>
          )}
        </Panel>
      </div>

      <Panel className="p-5">
        <SectionTitle
          icon={Radio}
          title="推理请求队列"
          hint={`共 ${entries.length} 条`}
          action={
            <div className="flex flex-wrap items-center gap-1.5">
              {(['all', 'active', 'finished'] as const).map((key) => (
                <button
                  key={key}
                  onClick={() => setFilter(key)}
                  className={`u-press rounded-lg px-2.5 py-1 text-xs ${
                    filter === key ? 'glass-chosen text-foreground' : 'glass-control text-muted-foreground'
                  }`}
                >
                  {key === 'all' ? '全部' : key === 'active' ? '待处理/推理中' : '已结束'}
                </button>
              ))}
              {data.enabled && (
                <button
                  onClick={() => setAutoPoll((v) => !v)}
                  className="u-press glass-control flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs text-muted-foreground hover:text-foreground"
                  title="切换 2.5s 快速自动刷新"
                >
                  {autoPoll ? <Pause size={12} /> : <Play size={12} />}
                  {autoPoll ? '自动刷新中' : '已暂停'}
                </button>
              )}
              <button
                onClick={() => void onRefresh()}
                className="u-press glass-control flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs text-muted-foreground hover:text-foreground"
              >
                <RefreshCw size={12} /> 刷新
              </button>
            </div>
          }
        />

        {actionError && (
          <div className="mb-3 rounded-xl border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-500">
            {actionError}
          </div>
        )}

        {entries.length === 0 ? (
          <EmptyState
            icon={Inbox}
            title="暂无手动推理请求"
            hint={data.enabled ? '向 /v1/responses 发送 stream=true 请求后将出现在这里。' : '启用手动推理桥接后，排队中的请求将实时展示。'}
          />
        ) : (
          <div className="space-y-3">
            {entries.map((entry) => {
              const badge = statusMeta(entry.status);
              const active = isManualEntryActive(entry.status);
              const copyKey = `prompt-${entry.request_id}`;
              return (
                <div
                  key={entry.request_id}
                  className="rounded-xl border border-border/50 bg-card/40 p-4 transition-colors hover:bg-card/60"
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge className={badge.className}>{badge.label}</Badge>
                    <span className="font-mono text-xs font-semibold">{entry.request_id}</span>
                    <span className="text-xs text-muted-foreground">会话 {entry.session_id} · Turn {entry.turn}</span>
                    <Badge className="bg-muted text-muted-foreground">{entry.model}</Badge>
                    <span className="text-xs text-muted-foreground">
                      新增 {entry.new_message_count} / 总消息 {entry.message_count} · 工具 {entry.tool_count}
                    </span>

                    <div className="ml-auto flex items-center gap-1.5">
                      {active && (
                        <>
                          <button
                            onClick={() => copyText(copyKey, entry.copy_prompt)}
                            className="u-press glass-control flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs"
                          >
                            {copiedKey === copyKey ? <Check size={12} className="text-emerald-500" /> : <Copy size={12} />}
                            {copiedKey === copyKey ? '已复制' : '复制定向提示词'}
                          </button>
                          <button
                            disabled={busyID === entry.request_id}
                            onClick={() => void handleCancel(entry.request_id)}
                            className="u-press flex items-center gap-1 rounded-lg border border-red-500/30 bg-red-500/10 px-2.5 py-1 text-xs text-red-500 hover:bg-red-500/20 disabled:opacity-50"
                          >
                            <XCircle size={12} /> 取消
                          </button>
                        </>
                      )}
                    </div>
                  </div>

                  {entry.preview && (
                    <pre className="mt-2.5 max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-background/50 p-2.5 font-mono text-xs text-muted-foreground">
                      {entry.preview}
                    </pre>
                  )}

                  {entry.error_message && (
                    <div className="mt-2 text-xs text-red-500">错误：{entry.error_message}</div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Panel>
    </div>
  );
}
