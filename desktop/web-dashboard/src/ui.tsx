import type { ReactNode, ElementType } from 'react';

export function Panel({ className = '', children }: { className?: string; children: ReactNode }) {
  return <div className={`glass-panel rounded-2xl ${className}`}>{children}</div>;
}

export function SectionTitle({
  icon: Icon,
  title,
  hint,
  action,
}: {
  icon: ElementType;
  title: string;
  hint?: string;
  action?: ReactNode;
}) {
  return (
    <div className="mb-4 flex items-center gap-2.5">
      <Icon size={16} className="text-muted-foreground" />
      <h2 className="text-sm font-semibold tracking-wide">{title}</h2>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      {action && <div className="ml-auto">{action}</div>}
    </div>
  );
}

const toneMap: Record<string, string> = {
  default: 'text-foreground',
  green: 'text-emerald-500',
  amber: 'text-amber-500',
  red: 'text-red-500',
  violet: 'text-violet-500',
  sky: 'text-sky-500',
};

export function StatCard({
  icon: Icon,
  label,
  value,
  sub,
  tone = 'default',
  dense = false,
}: {
  icon: ElementType;
  label: string;
  value: string | number;
  sub?: string;
  tone?: keyof typeof toneMap;
  dense?: boolean;
}) {
  return (
    <Panel className={dense ? 'p-3 sm:p-4' : 'animate-float-in p-5'}>
      <div className="flex items-start justify-between gap-1">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        <Icon size={dense ? 14 : 16} className="shrink-0 text-muted-foreground/70" />
      </div>
      <div className={`tabular font-semibold leading-none ${dense ? 'mt-2 text-xl sm:text-2xl' : 'mt-3 text-3xl'} ${toneMap[tone]}`}>{value}</div>
      {sub && <div className={`${dense ? 'mt-1.5' : 'mt-2'} text-xs text-muted-foreground`}>{sub}</div>}
    </Panel>
  );
}

export function Badge({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium ${className}`}>
      {children}
    </span>
  );
}

export function EmptyState({ icon: Icon, title, hint }: { icon: ElementType; title: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
      <Icon size={28} className="text-muted-foreground" />
      <div className="text-sm font-medium">{title}</div>
      {hint && <div className="text-xs text-muted-foreground">{hint}</div>}
    </div>
  );
}

/* ---------- formatters ---------- */

export const fmt = new Intl.NumberFormat('zh-CN');

export function compact(n: number): string {
  if (!Number.isFinite(n)) return '-';
  const a = Math.abs(n);
  if (a >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (a >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (a >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return fmt.format(n);
}

export function bytes(n: number): string {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(2) + ' GB';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + ' KB';
  return n + ' B';
}

export function duration(ms: number): string {
  if (ms >= 86400000) return Math.floor(ms / 86400000) + ' 天';
  if (ms >= 3600000) return Math.floor(ms / 3600000) + ' 小时';
  if (ms >= 60000) return Math.floor(ms / 60000) + ' 分钟';
  return Math.floor(ms / 1000) + ' 秒';
}

export function relTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (Number.isNaN(diff)) return '-';
  if (diff < 0) return '刚刚';
  return duration(diff) + '前';
}

export function ms(n: number): string {
  if (n >= 1000) return (n / 1000).toFixed(2) + ' s';
  return n.toFixed(n < 1 ? 2 : 0) + ' ms';
}

export function shortPath(p: string): string {
  if (!p) return '-';
  const parts = p.split('/');
  return parts.slice(-3).join('/');
}
