import { useEffect, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';

/**
 * 通用弹窗：挂到 body、遮罩居中、Esc / 点遮罩关闭、打开时锁定页面滚动。
 * 面板自带标题栏和关闭按钮，内容区独立滚动。
 */
export default function Modal({
  open,
  onClose,
  title,
  children,
  wide = false,
}: {
  open: boolean;
  onClose: () => void;
  title?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener('keydown', onKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    panelRef.current?.focus();
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = prevOverflow;
    };
  }, [open, onClose]);

  if (!open) return null;

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-2 sm:p-6">
      {/* 遮罩 */}
      <div className="absolute inset-0 bg-black/55 backdrop-blur-[3px]" onClick={onClose} aria-hidden="true" />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : undefined}
        tabIndex={-1}
        className={`glass-panel animate-float-in relative flex max-h-[92dvh] w-full flex-col overflow-hidden rounded-2xl shadow-2xl outline-none ${wide ? 'max-w-3xl' : 'max-w-2xl'}`}
      >
        <div className="flex shrink-0 items-center gap-3 border-b border-border/60 px-4 py-3">
          <div className="min-w-0 flex-1 truncate text-sm font-semibold">{title}</div>
          <button
            onClick={onClose}
            aria-label="关闭"
            title="关闭 (Esc)"
            className="u-press glass-control flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2"
          >
            <X size={16} />
          </button>
        </div>
        <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-3.5 sm:p-4">
          {children}
        </div>
      </div>
    </div>,
    document.body,
  );
}
