// The Desk tiles that don't proxy a remote service: Next up (Devcules'
// own approvals + calendar), and the four local-state widgets — todos,
// timer, quick links and the scratchpad — which persist to the widget
// config (todos/links/scratch) or the small deskTimer store (timer).

import { useEffect, useRef, useState } from 'react';
import Tile, { CopyButton } from './Tile';
import { listApprovals, type Approval } from '../../api';
import { getMeetings } from '../../lib/deskApi';
import { copyText } from '../../lib/toast';
import { fmtClock, isFinished, pauseTimer, PRESETS_MIN, readTimer, remainingMs, resumeTimer, startTimer, writeTimer, type Timer } from '../../lib/deskTimer';
import type { TileProps } from './tiles';

// ---- Next up (brief): pending approvals + the next meeting — the
// things that actually need you, ranked. ----
export function NextUpTile({ syncKey, arranging, onRemove, onSettings, onUnauthorized, onOpenApprovals }: TileProps & { onOpenApprovals?: () => void }) {
  const [pending, setPending] = useState<Approval[]>([]);
  const [next, setNext] = useState<{ title: string; start: string; url?: string } | null>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let alive = true;
    void Promise.all([listApprovals('pending', 20), getMeetings(false)]).then(([a, m]) => {
      if (!alive) return;
      if (a === 'unauthorized' || m === 'unauthorized') { onUnauthorized?.(); setLoading(false); return; }
      if (typeof a === 'object' && a !== null && Array.isArray((a as { approvals?: Approval[] }).approvals)) {
        setPending((a as { approvals: Approval[] }).approvals);
      }
      if (typeof m === 'object' && m !== null && Array.isArray((m as { items?: unknown[] }).items)) {
        const items = (m as { items: Array<{ title: string; start: string; end: string; url?: string }> }).items;
        const now = Date.now();
        setNext(items.find((x) => new Date(x.end).getTime() > now) ?? null);
      }
      setLoading(false);
    });
    return () => { alive = false; };
  }, [syncKey, onUnauthorized]);
  const empty = !loading && pending.length === 0 && !next;
  return (
    <Tile title="next up" status={loading && pending.length === 0 && !next ? 'loading' : 'live'} arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      {empty ? (
        <div className="text-body text-muted">Nothing needs you. Enjoy the quiet.</div>
      ) : (
        <div className="flex flex-col gap-3">
          {pending.length > 0 && (
            <div>
              <div className="mb-1 text-tab font-semibold text-red-400">{pending.length} approval{pending.length === 1 ? '' : 's'} waiting</div>
              <ul className="flex flex-col gap-1">
                {pending.slice(0, 6).map((p) => (
                  <li key={p.id}>
                    <button type="button" className="w-full truncate rounded px-1 py-0.5 text-left text-body text-text hover:bg-base" onClick={onOpenApprovals} title={`${p.agent} · ${p.command}`}>
                      {p.tool}: {p.command}
                    </button>
                  </li>
                ))}
              </ul>
              {onOpenApprovals && (
                <button type="button" className="mt-1 rounded bg-accent px-2 py-1 text-tab text-on-accent" onClick={onOpenApprovals}>
                  open approvals
                </button>
              )}
            </div>
          )}
          {next && (
            <div>
              <div className="mb-1 text-tab font-semibold text-muted">next on your calendar</div>
              <div className="flex items-center gap-2">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-body text-text">{next.title}</div>
                  <div className="text-tab text-muted">{new Date(next.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</div>
                </div>
                {next.url && <CopyButton text={next.url} label="copy meeting link" />}
              </div>
            </div>
          )}
        </div>
      )}
    </Tile>
  );
}

// ---- Todos (config-persisted simple list) ----
type Todo = { id: string; text: string; done?: boolean };
export function TodosTile({ widget, arranging, onRemove, onSettings, onConfig }: TileProps) {
  const todos = (Array.isArray(widget.config?.todos) ? widget.config.todos : []) as Todo[];
  const [text, setText] = useState('');
  const save = (next: Todo[]) => onConfig?.({ todos: next });
  const add = (e: React.FormEvent) => {
    e.preventDefault();
    const t = text.trim();
    if (!t) return;
    setText('');
    save([...todos, { id: `t-${Date.now().toString(36)}`, text: t }]);
  };
  return (
    <Tile title={widget.title || 'todos'} status="live" arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      <ul className="mb-2 flex flex-col gap-1">
        {todos.map((t) => (
          <li key={t.id} className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={!!t.done}
              aria-label={t.text}
              onChange={() => save(todos.map((x) => (x.id === t.id ? { ...x, done: !x.done } : x)))}
            />
            <span className={`min-w-0 flex-1 truncate text-body ${t.done ? 'text-muted line-through' : 'text-text'}`}>{t.text}</span>
            {!arranging && (
              <button type="button" className="d-tap min-h-6 min-w-6 rounded px-1 text-muted hover:text-red-400" aria-label={`remove ${t.text}`} onClick={() => save(todos.filter((x) => x.id !== t.id))}>✕</button>
            )}
          </li>
        ))}
      </ul>
      {todos.length === 0 && <div className="mb-2 text-body text-muted">Nothing here. Add the one thing you keep forgetting.</div>}
      <form onSubmit={add} className="flex gap-1">
        <input value={text} onChange={(e) => setText(e.target.value)} placeholder="add a todo" aria-label="new todo" className="min-h-11 min-w-0 flex-1 rounded bg-base px-2 text-body text-text" />
        <button type="submit" className="min-h-11 rounded bg-accent px-3 text-body text-on-accent">add</button>
      </form>
    </Tile>
  );
}

// ---- Timer ----
export function beep(): void {
  try {
    const AC = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AC) return;
    const ctx = new AC();
    const at = ctx.currentTime;
    for (const [start, freq] of [[0, 880], [0.18, 1175]] as const) {
      const o = ctx.createOscillator();
      const g = ctx.createGain();
      o.frequency.value = freq;
      o.connect(g);
      g.connect(ctx.destination);
      g.gain.setValueAtTime(at + start, 0.0001);
      g.gain.exponentialRampToValueAtTime(0.2, at + start + 0.02);
      g.gain.exponentialRampToValueAtTime(0.0001, at + start + 0.18);
      o.start(at + start);
      o.stop(at + start + 0.2);
    }
  } catch {
    /* no audio */
  }
}

export function TimerTile({ arranging, onRemove, onSettings }: TileProps) {
  const [t, setT] = useState<Timer | null>(() => readTimer());
  const [, tick] = useState(0);
  const done = useRef(false);
  useEffect(() => {
    const id = window.setInterval(() => tick((n) => n + 1), 1000);
    return () => window.clearInterval(id);
  }, []);
  useEffect(() => { writeTimer(t); }, [t]);
  const finished = isFinished(t);
  useEffect(() => {
    if (finished && !done.current) { done.current = true; beep(); }
    if (!finished) done.current = false;
  }, [finished]);
  const start = (min: number) => { done.current = false; setT(startTimer(min)); };
  return (
    <Tile title="timer" status="live" arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      <div className="flex flex-col items-center gap-3 py-1">
        <div className={`font-mono text-4xl ${finished ? 'text-accent' : 'text-text'}`}>{fmtClock(remainingMs(t))}</div>
        {!t && (
          <div className="flex gap-2">
            {PRESETS_MIN.map((m) => (
              <button key={m} type="button" className="rounded bg-accent px-3 py-1 text-body text-on-accent" onClick={() => start(m)}>{m}m</button>
            ))}
          </div>
        )}
        {t && !finished && (
          <div className="flex gap-2">
            {t.running
              ? <button type="button" className="rounded bg-base px-3 py-1 text-body text-text" onClick={() => setT(pauseTimer(t))}>pause</button>
              : <button type="button" className="rounded bg-accent px-3 py-1 text-body text-on-accent" onClick={() => setT(resumeTimer(t))}>resume</button>}
            <button type="button" className="rounded bg-base px-3 py-1 text-body text-muted" onClick={() => setT(null)}>reset</button>
          </div>
        )}
        {finished && t && (
          <div className="flex gap-2">
            <button type="button" className="rounded bg-accent px-3 py-1 text-body text-on-accent" onClick={() => start(Math.round(t.totalMs / 60000))}>again</button>
            <button type="button" className="rounded bg-base px-3 py-1 text-body text-muted" onClick={() => setT(null)}>clear</button>
          </div>
        )}
      </div>
    </Tile>
  );
}

// ---- Quick links (config-persisted) ----
type Link = { title: string; url: string };
export function LinksTile({ widget, arranging, onRemove, onSettings, onConfig }: TileProps) {
  const links = (Array.isArray(widget.config?.links) ? widget.config.links : []) as Link[];
  const [title, setTitle] = useState('');
  const [url, setUrl] = useState('');
  const [err, setErr] = useState('');
  const add = (e: React.FormEvent) => {
    e.preventDefault();
    let u = url.trim();
    const t = title.trim();
    if (!u) return;
    if (!/^https?:\/\//i.test(u)) u = 'https://' + u;
    try { new URL(u); } catch { setErr('that is not a web address'); return; }
    setErr('');
    setTitle('');
    setUrl('');
    onConfig?.({ links: [...links, { title: t || u.replace(/^https?:\/\//, ''), url: u }] });
  };
  return (
    <Tile title={widget.title || 'quick links'} status="live" arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      <ul className="mb-2 flex flex-col gap-1">
        {links.map((l) => (
          <li key={l.url} className="flex items-center gap-1">
            <a href={l.url} target="_blank" rel="noreferrer" className="min-w-0 flex-1 truncate text-body text-accent hover:underline">{l.title}</a>
            <CopyButton text={l.url} label={`copy ${l.title}`} />
            {!arranging && (
              <button type="button" className="d-tap min-h-6 min-w-6 rounded px-1 text-muted hover:text-red-400" aria-label={`remove ${l.title}`} onClick={() => onConfig?.({ links: links.filter((x) => x.url !== l.url) })}>✕</button>
            )}
          </li>
        ))}
      </ul>
      {links.length === 0 && <div className="mb-2 text-body text-muted">No links yet. Add the pages you keep going back to.</div>}
      <form onSubmit={add} className="flex flex-col gap-1">
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="title" aria-label="link title" className="min-h-8 rounded bg-base px-2 text-body text-text" />
        <div className="flex gap-1">
          <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" aria-label="link url" className="min-h-8 min-w-0 flex-1 rounded bg-base px-2 text-body text-text" />
          <button type="submit" className="min-h-11 rounded bg-accent px-3 text-body text-on-accent">add</button>
        </div>
      </form>
      {err && <div className="mt-1 text-tab text-red-400">{err}</div>}
    </Tile>
  );
}

// ---- Scratchpad (config-persisted note) ----
export function ScratchTile({ widget, arranging, onRemove, onConfig, debounceMs = 800 }: TileProps & { debounceMs?: number }) {
  const [text, setText] = useState(String(widget.config?.text ?? ''));
  const [saved, setSaved] = useState(true);
  const timer = useRef<number | null>(null);
  const last = useRef(String(widget.config?.text ?? ''));
  useEffect(() => () => { if (timer.current !== null) window.clearTimeout(timer.current); }, []);
  const change = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const v = e.target.value;
    setText(v);
    setSaved(false);
    if (timer.current !== null) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      if (v !== last.current) { last.current = v; onConfig?.({ text: v }); }
      setSaved(true);
    }, debounceMs);
  };
  return (
    <Tile title={widget.title || 'scratchpad'} status="live" meta={saved ? `${text.length} chars` : 'saving…'} arranging={arranging} onRemove={onRemove}>
      <textarea
        className="h-full min-h-24 w-full resize-none rounded bg-base p-2 text-body text-text"
        value={text}
        onChange={change}
        placeholder="Type anything. It stays here."
        aria-label="scratchpad"
        spellCheck={false}
      />
    </Tile>
  );
}

// Re-exported for the toast on copy lives in toast.ts already; copyText is
// used by CopyButton inside tiles.tsx. This import keeps the module graph
// explicit about the local tiles also using it.
export { copyText };
