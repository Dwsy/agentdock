import { useEffect, useState } from 'react';
import {
  KeyRound, Link2, Cloud, RefreshCw, Save, RotateCw, CheckCircle2,
  AlertTriangle, Shuffle, Copy,
} from 'lucide-react';
import { api, type RemoteAccess } from '../api';
import { Panel, SectionTitle } from '../ui';

/** 32 字节随机 hex，与桌面安装器生成的 token 形态一致。 */
function randomToken(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

function StatusChip({ ok, text }: { ok: boolean; text: string }) {
  return (
    <span className={`rounded px-2 py-0.5 text-[11px] ${ok ? 'bg-emerald-500/15 text-emerald-500' : 'bg-red-500/10 text-red-500'}`}>
      {text}
    </span>
  );
}

function FieldRow({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border/50 bg-card/40 px-3 py-2.5">
      <div className="min-w-0 flex-1">
        <div className="text-xs font-medium">{label}</div>
        {hint && <div className="mt-0.5 text-[11px] text-muted-foreground">{hint}</div>}
      </div>
      {children}
    </div>
  );
}

const inputCls = 'glass-control w-64 shrink-0 rounded-md px-2 py-1.5 font-mono text-xs outline-none';

export default function RemoteAccessPage() {
  const [state, setState] = useState<RemoteAccess | null>(null);
  const [loading, setLoading] = useState(true);
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);
  const [savingToken, setSavingToken] = useState(false);
  const [pairing, setPairing] = useState(false);
  const [applyingTunnel, setApplyingTunnel] = useState(false);
  const [restarting, setRestarting] = useState(false);

  const [tokenDraft, setTokenDraft] = useState('');
  const [pairEndpoint, setPairEndpoint] = useState('');
  const [pairCode, setPairCode] = useState('');
  const [deviceName, setDeviceName] = useState('');
  const [tunnelMode, setTunnelMode] = useState('none');
  const [serverUrl, setServerUrl] = useState('');
  const [tunnelToken, setTunnelToken] = useState('');

  const load = () => {
    setLoading(true);
    api.remoteAccess()
      .then((s) => {
        setState(s);
        setTunnelMode(s.tunnel?.mode ?? 'none');
        if (s.tunnel?.mode === 'named' && !serverUrl && s.tunnel.public_url) setServerUrl(s.tunnel.public_url);
      })
      .catch((e) => setMsg({ type: 'err', text: `加载远程访问状态失败：${e?.message || e}` }))
      .finally(() => setLoading(false));
  };
  useEffect(() => { load(); }, []);

  const saveToken = async () => {
    if (!tokenDraft.trim()) return;
    if (!window.confirm('轮换访问令牌后，旧的 Bearer 调用方会全部失效，确定继续？')) return;
    setSavingToken(true);
    setMsg(null);
    try {
      const r = await api.remoteTokenUpdate(tokenDraft.trim());
      setMsg({ type: 'ok', text: r.restart_hint || '访问令牌已保存。' });
      setTokenDraft('');
    } catch (e: any) {
      setMsg({ type: 'err', text: `保存失败：${e?.message || e}` });
    } finally {
      setSavingToken(false);
    }
  };

  const doPair = async () => {
    setPairing(true);
    setMsg(null);
    try {
      const r = await api.nexusPair(pairEndpoint.trim(), pairCode.trim(), deviceName.trim() || undefined);
      setMsg({ type: 'ok', text: `已配对到 ${r.endpoint || pairEndpoint}。${r.restart_hint || ''}` });
      setPairCode('');
      load();
    } catch (e: any) {
      setMsg({ type: 'err', text: `配对失败：${e?.message || e}` });
    } finally {
      setPairing(false);
    }
  };

  const applyTunnel = async () => {
    setApplyingTunnel(true);
    setMsg(null);
    try {
      const r = await api.tunnelConfigure(tunnelMode, serverUrl.trim(), tunnelToken.trim() || undefined);
      setMsg({ type: 'ok', text: r.restart_hint || 'Tunnel 配置已应用。' });
      setTunnelToken('');
      setTimeout(load, 1500);
    } catch (e: any) {
      setMsg({ type: 'err', text: `应用失败：${e?.message || e}` });
    } finally {
      setApplyingTunnel(false);
    }
  };

  const restart = async () => {
    if (!window.confirm('重启 Core 会中断当前正在运行的任务，确定继续？')) return;
    setRestarting(true);
    setMsg(null);
    try {
      const r = await api.configRestart();
      setMsg({ type: 'ok', text: r.message || 'Core 正在重启' });
      setTimeout(load, 2500);
    } catch (e: any) {
      setMsg({ type: 'err', text: `重启失败：${e?.message || e}` });
    } finally {
      setRestarting(false);
    }
  };

  if (loading && !state) {
    return <div className="flex items-center justify-center py-24 text-muted-foreground"><RefreshCw size={18} className="mr-3 animate-spin" /> 加载远程访问状态…</div>;
  }
  if (!state) {
    return (
      <Panel className="p-8 text-center">
        <AlertTriangle size={28} className="mx-auto text-red-500" />
        <div className="mt-3 text-sm">{msg?.text || '远程访问状态不可用'}</div>
        <button onClick={load} className="glass-control mt-4 rounded-lg px-4 py-2 text-sm">重试</button>
      </Panel>
    );
  }

  const nexus = state.nexus;
  const tunnel = state.tunnel;
  const copyText = async (text: string) => { try { await navigator.clipboard.writeText(text); } catch { /* 剪贴板被拒绝时静默 */ } };

  return (
    <div className="space-y-5">
      <Panel className="p-5">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-base font-semibold">远程访问</h2>
              <StatusChip ok={state.configurable} text={state.configurable ? '本机直连' : '不可写入'} />
            </div>
            <p className="mt-1.5 text-xs text-muted-foreground">
              访问令牌、Nexus 配对与 Cloudflare 隧道均写入本机配置；密钥值不会在此回显。
            </p>
            {state.agentdock_home && <p className="mt-1 font-mono text-[11px] text-muted-foreground/70">{state.agentdock_home}</p>}
          </div>
          <div className="flex gap-2">
            <button onClick={load} className="glass-control flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs text-muted-foreground hover:text-foreground">
              <RefreshCw size={13} /> 重新加载
            </button>
            <button onClick={restart} disabled={restarting} className="glass-control flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs text-amber-500 disabled:opacity-40">
              <RotateCw size={13} /> {restarting ? '重启中…' : '重启 Core'}
            </button>
          </div>
        </div>
        {msg && (
          <div className={`mt-4 flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${msg.type === 'ok' ? 'bg-emerald-500/10 text-emerald-500' : 'bg-red-500/10 text-red-500'}`}>
            {msg.type === 'ok' ? <CheckCircle2 size={13} /> : <AlertTriangle size={13} />} {msg.text}
          </div>
        )}
      </Panel>

      <div className="grid gap-5 lg:grid-cols-2">
        <Panel className="p-5">
          <SectionTitle icon={KeyRound} title="访问令牌" hint={state.auth_token_configured ? '已配置' : '未配置'} />
          <div className="space-y-2">
            <FieldRow label="AGENTDOCK_AUTH_TOKEN" hint="公网 Tunnel 下必须设置；此页只显示已配置与否，不回显当前值">
              <div className="flex shrink-0 items-center gap-2">
                <StatusChip ok={state.auth_token_configured} text={state.auth_token_configured ? '已配置' : '未配置'} />
              </div>
            </FieldRow>
            <FieldRow label="轮换令牌" hint="至少 16 个字符；留空不改动">
              <div className="flex shrink-0 items-center gap-2">
                <input
                  type="password"
                  value={tokenDraft}
                  onChange={(e) => setTokenDraft(e.target.value)}
                  placeholder="新令牌"
                  className={`${inputCls} w-44`}
                />
                <button
                  onClick={() => setTokenDraft(randomToken())}
                  className="glass-control flex items-center gap-1 rounded-md px-2 py-1.5 text-[11px] text-muted-foreground hover:text-foreground"
                  title="随机生成 64 位 hex"
                >
                  <Shuffle size={12} /> 随机
                </button>
              </div>
            </FieldRow>
            <button
              onClick={saveToken}
              disabled={!state.configurable || savingToken || !tokenDraft.trim()}
              className="glass-primary flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              <Save size={13} /> {savingToken ? '保存中…' : '保存并轮换'}
            </button>
            <p className="text-[11px] text-muted-foreground">新令牌重启 Core 后生效，旧令牌随之失效。</p>
          </div>
        </Panel>

        <Panel className="p-5">
          <SectionTitle icon={Link2} title="Nexus 配对" hint={nexus.paired ? '已配对' : '未配对'} />
          <div className="space-y-2">
            {nexus.error && (
              <div className="flex items-center gap-2 rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-500">
                <AlertTriangle size={13} /> {nexus.error}
              </div>
            )}
            {nexus.paired ? (
              <>
                <FieldRow label="服务器" hint={nexus.server_edition ? `edition ${nexus.server_edition}${nexus.server_version ? ' · ' + nexus.server_version : ''}` : undefined}>
                  <div className="flex shrink-0 items-center gap-2">
                    <span className="font-mono text-[11px] text-muted-foreground">{nexus.endpoint}</span>
                    <button onClick={() => copyText(nexus.endpoint || '')} className="glass-control rounded p-1" title="复制地址"><Copy size={11} /></button>
                  </div>
                </FieldRow>
                <FieldRow label="node_id" hint={`凭据文件：${state.nexus_device_identity}`}>
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{nexus.node_id}</span>
                </FieldRow>
              </>
            ) : (
              <p className="text-[11px] text-muted-foreground">
                Nexus 是设备接入远端控制台（NexusDock）的通道，支持官方或自建服务；在服务端生成一次性配对码后填入下方。
              </p>
            )}
            <FieldRow label="Nexus 地址" hint="自建服务填自己的 Base URL，例如 https://nexus.example.com">
              <input value={pairEndpoint} onChange={(e) => setPairEndpoint(e.target.value)} placeholder="https://…" className={inputCls} />
            </FieldRow>
            <FieldRow label="一次性配对码" hint="服务端生成，用后即焚">
              <input value={pairCode} onChange={(e) => setPairCode(e.target.value)} placeholder="配对码" className={`${inputCls} w-40`} />
            </FieldRow>
            <FieldRow label="设备名称（可选）" hint="默认使用主机名">
              <input value={deviceName} onChange={(e) => setDeviceName(e.target.value)} placeholder="名称" className={`${inputCls} w-40`} />
            </FieldRow>
            <button
              onClick={doPair}
              disabled={!state.configurable || pairing || !pairEndpoint.trim() || !pairCode.trim()}
              className="glass-primary flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              <Link2 size={13} /> {pairing ? '配对中…' : '配对'}
            </button>
            <p className="text-[11px] text-muted-foreground">配对凭据保存在本机，重启 Core 后建立连接。</p>
          </div>
        </Panel>

        <Panel className="p-5 lg:col-span-2">
          <SectionTitle icon={Cloud} title="Cloudflare 隧道" hint={tunnel?.mode ? `当前：${tunnel.mode}` : '状态不可用'} />
          {tunnel_error_block(state.tunnel_error)}
          {tunnel && (
            <div className="mb-3 grid gap-2 sm:grid-cols-2">
              <FieldRow label="运行状态">
                <StatusChip ok={!!tunnel.running} text={tunnel.running ? '运行中' : '未运行'} />
              </FieldRow>
              <FieldRow label="公网地址" hint={tunnel.public_url || '尚未生成'}>
                {tunnel.public_url ? (
                  <div className="flex shrink-0 items-center gap-2">
                    <a href={tunnel.public_url} target="_blank" rel="noreferrer" className="font-mono text-[11px] text-primary hover:underline">{tunnel.public_url}</a>
                    <button onClick={() => copyText(tunnel.public_url || '')} className="glass-control rounded p-1" title="复制地址"><Copy size={11} /></button>
                  </div>
                ) : (
                  <span className="shrink-0 text-[11px] text-muted-foreground">—</span>
                )}
              </FieldRow>
            </div>
          )}
          <div className="space-y-2">
            <FieldRow label="隧道模式" hint="none 仅本地 · quick 临时地址（重启会变）· named 固定域名">
              <select value={tunnelMode} onChange={(e) => setTunnelMode(e.target.value)} className="glass-control shrink-0 rounded-md px-2 py-1.5 text-xs outline-none">
                <option value="none">none（仅本地）</option>
                <option value="quick">quick（临时隧道）</option>
                <option value="named">named（固定域名）</option>
              </select>
            </FieldRow>
            {tunnelMode === 'named' && (
              <>
                <FieldRow label="HTTPS Origin" hint="Named Tunnel 对外的固定地址">
                  <input value={serverUrl} onChange={(e) => setServerUrl(e.target.value)} placeholder="https://agent.example.com" className={inputCls} />
                </FieldRow>
                <FieldRow label="Tunnel Token" hint="Cloudflare 侧创建 Tunnel 后获得的 Token">
                  <input type="password" value={tunnelToken} onChange={(e) => setTunnelToken(e.target.value)} placeholder="ey…" className={inputCls} />
                </FieldRow>
              </>
            )}
            <button
              onClick={applyTunnel}
              disabled={!state.configurable || applyingTunnel || (tunnelMode === 'named' && (!serverUrl.trim() || !tunnelToken.trim()))}
              className="glass-primary flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              <Cloud size={13} /> {applyingTunnel ? '应用中…' : '应用隧道配置'}
            </button>
            <p className="text-[11px] text-muted-foreground">
              none/quick 即时生效；named 会写入 SERVER_URL 与 OAuth 开关，重启 Core 后生效。
            </p>
          </div>
        </Panel>
      </div>
    </div>
  );
}

function tunnel_error_block(error?: string) {
  if (!error) return null;
  return (
    <div className="mb-3 flex items-center gap-2 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-500">
      <AlertTriangle size={13} /> 隧道状态读取失败：{error}
    </div>
  );
}
