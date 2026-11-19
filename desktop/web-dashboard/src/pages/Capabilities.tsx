import { useState } from 'react';
import { Boxes, Plug, Wifi, FileText, ShieldCheck, Search } from 'lucide-react';
import type { Catalog, Skills, Plugins, MCPs, Status } from '../api';
import { Panel, SectionTitle, Badge, fmt } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';

type Props = { catalog: Catalog; skills: Skills; plugins: Plugins; mcp: MCPs; status: Status };

export default function Capabilities({ catalog, skills, plugins, mcp, status }: Props) {
  const [tab, setTab] = usePersistedState<'tools' | 'rules' | 'skills' | 'common'>('capabilities-tab', 'tools');
  const [q, setQ] = useState('');

  const tools = catalog.tools.filter((t) => !q || (t.name + t.description).toLowerCase().includes(q.toLowerCase()));
  const commonSkills = catalog.common_skills.items.filter((s) => !q || (s.name + s.description).toLowerCase().includes(q.toLowerCase()));
  const filteredSkills = skills.skills.filter((s) => !q || (s.name + s.description).toLowerCase().includes(q.toLowerCase()));

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {[
          [Boxes, 'Skills', skills.count],
          [Plug, 'Plugins', plugins.count],
          [Wifi, 'MCP 服务', mcp.count],
          [ShieldCheck, '工具', catalog.tool_count],
        ].map(([Icon, label, val]: any) => (
          <Panel key={label} className="p-5">
            <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">{label}</span><Icon size={16} className="text-muted-foreground" /></div>
            <div className="tabular mt-3 text-3xl font-semibold">{fmt.format(val as number)}</div>
          </Panel>
        ))}
      </div>

      <Panel className="p-5">
        <SectionTitle
          icon={Boxes}
          title="能力目录"
          hint={`工作区 ${catalog.workspace_root || catalog.workdir || '-'}`}
          action={
            <div className="glass-control flex items-center gap-2 rounded-lg px-3 py-1.5">
              <Search size={14} className="text-muted-foreground" />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索…" className="w-44 bg-transparent text-xs outline-none placeholder:text-muted-foreground" />
            </div>
          }
        />
        <div className="mb-4 flex flex-wrap gap-2">
          {[
            ['tools', `工具 ${catalog.tool_count}`],
            ['rules', `规则 ${catalog.rules.length}`],
            ['skills', `已装 Skill ${skills.count}`],
            ['common', `共享 Skill ${catalog.common_skills.total}`],
          ].map(([k, label]) => (
            <button key={k} onClick={() => setTab(k as any)} className={`glass-control rounded-md px-3 py-1.5 text-xs ${tab === k ? 'text-foreground' : 'text-muted-foreground'}`}>{label}</button>
          ))}
        </div>

        <div className="scrollbar-thin max-h-[600px] space-y-2 overflow-y-auto pr-1">
          {tab === 'tools' && tools.map((t) => (
            <div key={t.name} className="rounded-xl border border-border/60 bg-card/40 p-3.5">
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm font-medium">{t.name}</span>
                {t.read_only_hint && <Badge className="bg-emerald-500/10 text-emerald-500">只读</Badge>}
              </div>
              <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{t.description}</p>
            </div>
          ))}

          {tab === 'rules' && catalog.rules.map((r, i) => (
            <div key={i} className="flex items-center gap-3 rounded-xl border border-border/60 bg-card/40 p-3.5">
              <FileText size={15} className="text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate font-mono text-xs" title={r.path}>{r.path}</span>
              <Badge className="bg-muted/20 text-muted-foreground">{r.scope}</Badge>
              <Badge className="bg-sky-500/10 text-sky-500">{r.status}</Badge>
            </div>
          ))}

          {tab === 'skills' && filteredSkills.map((s) => (
            <div key={s.name} className="rounded-xl border border-border/60 bg-card/40 p-3.5">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">{s.name}</span>
                <Badge className="bg-violet-500/10 text-violet-500">{s.source_type}</Badge>
                {s.file_count != null && <span className="text-[11px] text-muted-foreground">{s.file_count} 文件</span>}
              </div>
              <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{s.description}</p>
            </div>
          ))}

          {tab === 'common' && commonSkills.map((s) => (
            <div key={s.name} className="rounded-xl border border-border/60 bg-card/40 p-3.5">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">{s.name}</span>
                <Badge className="bg-muted/20 text-muted-foreground">{s.source_type}</Badge>
              </div>
              <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{s.description}</p>
            </div>
          ))}
        </div>
      </Panel>
    </div>
  );
}
