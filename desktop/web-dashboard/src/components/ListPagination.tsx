import type { ReactNode } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { fmt } from '../ui';

type Props = {
  label: string;
  page: number;
  pageCount?: number;
  canNext: boolean;
  disabled?: boolean;
  onPrevious: () => void;
  onNext: () => void;
  children?: ReactNode;
};

const buttonClass = 'glass-control inline-flex min-h-9 shrink-0 items-center gap-1 rounded-lg px-2 text-xs font-medium hover:bg-accent disabled:opacity-40';

export default function ListPagination({ label, page, pageCount, canNext, disabled, onPrevious, onNext, children }: Props) {
  return (
    <nav aria-label={label} className="flex flex-wrap items-center gap-2">
      <button type="button" className={buttonClass} disabled={disabled || page <= 1} onClick={onPrevious}>
        <ChevronLeft size={14} aria-hidden="true" /> 上一页
      </button>
      <span aria-live="polite" className="tabular whitespace-nowrap text-xs text-muted-foreground">
        {pageCount === undefined ? `第 ${fmt.format(page)} 页` : `第 ${fmt.format(page)} / ${fmt.format(pageCount)} 页`}
      </span>
      <button type="button" className={buttonClass} disabled={disabled || !canNext} onClick={onNext}>
        下一页 <ChevronRight size={14} aria-hidden="true" />
      </button>
      {children}
    </nav>
  );
}
