import type { ReactNode } from 'react';
import { copyText } from '../../lib/toast';

export const STATUS_TEXT = { live: 'live', stale: 'stale', error: 'error', off: 'not set up', loading: 'loading' } as const;
export type TileStatus = keyof typeof STATUS_TEXT;

export function statusOf(data: { configured?: boolean; stale?: boolean } | null, loading: boolean, error: string): TileStatus {
  if (loading && !data) return 'loading';
  if (error && !data) return 'error';
  if (data && data.configured === false) return 'off';
  if (data?.stale) return 'stale';
  return 'live';
}

export function fmtTime(ts?: string): string {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

// Tile is the instrument frame every widget sits in: the status LED in the
// header carries live/stale/error/off, the right rail holds "updated hh:mm"
// and the actions (refresh, settings, copy, and — in arrange mode — remove).
export default function Tile({
  title, status = 'live', meta, updatedAt, arranging = false,
  onRefresh, onSettings, onRemove, children, className = '',
}: {
  title: string;
  status?: TileStatus;
  meta?: ReactNode;
  updatedAt?: string;
  arranging?: boolean;
  onRefresh?: () => void;
  onSettings?: () => void;
  onRemove?: () => void;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`i-card flex h-full min-h-0 flex-col overflow-hidden rounded-lg border border-muted bg-surface ${className}`} aria-label={title}>
      <header className="flex shrink-0 items-center gap-2 border-b border-muted px-3 py-2">
        {arranging && (
          <span className="d-tile-grip cursor-grab touch-none text-muted select-none" aria-hidden="true" title="drag to move">⠿</span>
        )}
        <span className={`d-led d-led-${status}`} title={STATUS_TEXT[status]} aria-hidden="true" />
        <span className="truncate text-body font-semibold text-text">{title}</span>
        <span className="meta ml-auto flex items-center gap-1 text-tab text-muted">
          {updatedAt && <span className={status === 'stale' ? 'text-amber-400' : ''}>{status === 'stale' ? 'stale · ' : ''}{fmtTime(updatedAt)}</span>}
          {meta}
          {onRefresh && !arranging && (
            <button type="button" className="d-tap min-h-7 min-w-7 rounded px-1 text-muted hover:text-text" onClick={onRefresh} title="refresh" aria-label={`refresh ${title}`}>⟳</button>
          )}
          {onSettings && (
            <button type="button" className="d-tap min-h-7 min-w-7 rounded px-1 text-muted hover:text-text" onClick={onSettings} title="settings" aria-label={`settings for ${title}`}>⚙</button>
          )}
          {arranging && onRemove && (
            <button type="button" className="d-tap min-h-7 min-w-7 rounded px-1 text-red-400 hover:text-red-300" onClick={onRemove} title="remove from the Desk" aria-label={`remove ${title}`}>✕</button>
          )}
        </span>
      </header>
      <div className="d-tile-body flex min-h-0 flex-1 flex-col overflow-y-auto p-3">{children}</div>
    </section>
  );
}

export function CopyButton({ text, label = 'copy link', onCopied }: { text: string; label?: string; onCopied?: () => void }) {
  return (
    <button
      type="button"
      className="d-tap min-h-6 min-w-6 shrink-0 rounded px-1 text-muted hover:text-text"
      onClick={(e) => { e.stopPropagation(); e.preventDefault(); void copyText(text); onCopied?.(); }}
      title={label}
      aria-label={label}
    >⧉</button>
  );
}
