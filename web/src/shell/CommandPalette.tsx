import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { THEMES, setTheme } from '../theme/themes';
import { confetti, isPresenting, togglePresenting } from './present';
import Modal from './Modal';

// CommandPalette: ⌘K / Ctrl+K (or "/") opens a fuzzy launcher for
// every screen, theme and desk action; "?" shows the shortcut sheet;
// "g" then a letter jumps to a screen; "p" toggles presentation mode.
// Shortcuts never fire while typing in a field or a terminal, nor while
// another dialog (any aria-modal) or an open header popup holds the
// keyboard.

export type Command = { id: string; label: string; hint?: string; group: string; run: () => void; keys?: string };

export type DeskAction = 'sync' | 'add' | 'sources' | 'arrange';

// The desk listens on one window event while it is mounted. A palette
// command run from another screen switches to the Desk first, so the
// Desk is not mounted yet when the command fires: the action is held
// here and the Desk takes it when it mounts. It is taken once, and only
// within PENDING_MS, so it can never fire on some later, unrelated visit.
export const PENDING_MS = 2000;
let deskListeners = 0;
let pending: { action: DeskAction; at: number } | null = null;

export function deskEvent(name: DeskAction): void {
  window.dispatchEvent(new CustomEvent('devcules:desk', { detail: { action: name } }));
}

// toDesk shows the Desk and runs the action there, now when the Desk is
// already listening, else as soon as it mounts.
export function toDesk(go: (() => void) | undefined, name: DeskAction): void {
  go?.();
  if (deskListeners > 0) { pending = null; deskEvent(name); return; }
  pending = { action: name, at: Date.now() };
}

// listenDesk is the Desk's side: it subscribes to desk actions and is
// handed a held action, if one is still fresh (held = true). It returns
// the unsubscribe.
export function listenDesk(fn: (a: DeskAction, held: boolean) => void): () => void {
  const on = (e: Event) => {
    const a = (e as CustomEvent<{ action: DeskAction }>).detail?.action;
    if (a) fn(a, false);
  };
  window.addEventListener('devcules:desk', on);
  deskListeners++;
  const held = pending;
  pending = null;
  if (held && Date.now() - held.at <= PENDING_MS) fn(held.action, true);
  return () => {
    window.removeEventListener('devcules:desk', on);
    deskListeners--;
  };
}

// The palette key reads as the platform writes it: ⌘K on Apple, Ctrl+K
// everywhere else.
export function modKeyLabel(): string {
  const nav = typeof navigator === 'undefined' ? undefined : navigator;
  const plat = (nav as (Navigator & { userAgentData?: { platform?: string } }) | undefined)?.userAgentData?.platform
    || nav?.platform || nav?.userAgent || '';
  return /mac|iphone|ipad|ipod/i.test(plat) ? '⌘K' : 'Ctrl+K';
}

// Another dialog (B10's Modal, or anything marked aria-modal) is open,
// or a header popup (the theme picker's listbox, the user menu) is
// expanded: those hold the keyboard too, and close on their own Escape.
function modalOpen(): boolean {
  return document.querySelector('[aria-modal="true"], [aria-haspopup][aria-expanded="true"]') !== null;
}

function typing(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null;
  if (!t) return false;
  if (t.isContentEditable) return true;
  if (t.closest?.('.xterm')) return true;
  const tag = t.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
}

// score is a tiny fuzzy match: every query letter in order, with a
// bonus for word starts and contiguous runs. -1 is no match.
export function score(label: string, q: string): number {
  if (!q) return 0;
  const l = label.toLowerCase();
  const s = q.toLowerCase().trim();
  if (l.includes(s)) return 100 - l.indexOf(s);
  let i = 0;
  let pts = 0;
  let prev = -2;
  for (const ch of s) {
    const at = l.indexOf(ch, i);
    if (at < 0) return -1;
    pts += at === prev + 1 ? 3 : 1;
    if (at === 0 || l[at - 1] === ' ') pts += 2;
    prev = at;
    i = at + 1;
  }
  return pts;
}

export default function CommandPalette({ nav }: { nav: Array<{ id: string; label: string; key?: string; go: () => void }> }) {
  const [open, setOpen] = useState(false);
  const [help, setHelp] = useState(false);
  const [q, setQ] = useState('');
  const [sel, setSel] = useState(0);
  const chord = useRef<number>(0);
  const [modKey] = useState(modKeyLabel);
  const listId = useId();
  const rowId = (i: number) => `${listId}-row-${i}`;

  const commands = useMemo<Command[]>(() => [
    ...nav.map((n) => ({ id: `go-${n.id}`, label: `Go to ${n.label}`, group: 'Navigate', keys: n.key ? `g ${n.key}` : undefined, run: n.go })),
    { id: 'present', label: isPresenting() ? 'Exit presentation mode' : 'Start presentation mode', hint: 'full-screen demo, chrome hidden', group: 'Demo', keys: 'p', run: togglePresenting },
    { id: 'confetti', label: 'Celebrate 🎉', hint: 'because you shipped', group: 'Demo', run: confetti },
    { id: 'desk-sync', label: 'Desk: refresh every tile', group: 'Desk', keys: 'r', run: () => toDesk(nav.find((n) => n.id === 'desk')?.go, 'sync') },
    { id: 'desk-add', label: 'Desk: add a widget', group: 'Desk', run: () => toDesk(nav.find((n) => n.id === 'desk')?.go, 'add') },
    { id: 'desk-arrange', label: 'Desk: arrange tiles', group: 'Desk', run: () => toDesk(nav.find((n) => n.id === 'desk')?.go, 'arrange') },
    { id: 'desk-sources', label: 'Desk: sources (calendar, news, tickets)', group: 'Desk', run: () => toDesk(nav.find((n) => n.id === 'desk')?.go, 'sources') },
    ...THEMES.map((t) => ({ id: `theme-${t.id}`, label: `Theme: ${t.label}`, hint: t.scheme, group: 'Theme', run: () => setTheme(t.id) })),
    { id: 'help', label: 'Keyboard shortcuts', group: 'Help', keys: '?', run: () => setHelp(true) },
  // eslint-disable-next-line react-hooks/exhaustive-deps
  ], [nav, open]);

  const shown = useMemo(() => {
    if (!q.trim()) return commands;
    return commands
      .map((c) => ({ c, s: Math.max(score(c.label, q), score(c.group + ' ' + c.label, q) - 1) }))
      .filter((x) => x.s >= 0)
      .sort((a, b) => b.s - a.s)
      .map((x) => x.c);
  }, [commands, q]);

  // Opening always starts fresh: empty query, first row selected.
  const show = (on: boolean | ((o: boolean) => boolean)) => {
    setQ('');
    setSel(0);
    setOpen(on);
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // A dialog that is not ours owns the keyboard, Escape included (it
      // closes itself; presentation mode stays on behind it).
      if (!open && !help && modalOpen()) { chord.current = 0; return; }
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); setHelp(false); show((o) => !o); return; }
      if (e.key === 'Escape') {
        if (open) { show(false); return; }
        if (help) { setHelp(false); return; }
        if (isPresenting()) { togglePresenting(); return; }
      }
      if (open || help || typing(e) || mod || e.altKey) return;
      // The g-chord comes first: the key after g is a jump, never a
      // single-key shortcut (g p must not toggle presentation).
      if (chord.current && Date.now() - chord.current < 1200) {
        const hit = nav.find((n) => n.key === e.key);
        chord.current = e.key === 'g' && !hit ? Date.now() : 0;
        if (hit) { e.preventDefault(); hit.go(); }
        return;
      }
      chord.current = 0;
      if (e.key === '/') { e.preventDefault(); show(true); return; }
      if (e.key === '?') { e.preventDefault(); setHelp(true); return; }
      if (e.key === 'p') { togglePresenting(); return; }
      if (e.key === 'r') { deskEvent('sync'); return; }
      if (e.key === 'g') { chord.current = Date.now(); return; }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, help, nav]);

  const run = (c?: Command) => { if (!c) return; show(false); c.run(); };

  // The active row stays in view as the arrows walk the list.
  useEffect(() => {
    if (open) document.getElementById(rowId(sel))?.scrollIntoView?.({ block: 'nearest' });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, sel, shown]);

  return (
    <>
      <button
        type="button"
        onClick={() => show(true)}
        className="v-kbd-btn hidden min-h-9 items-center gap-2 rounded-full border border-muted px-3 text-tab text-muted hover:text-text sm:inline-flex"
        aria-label="Open the command palette"
        title={`Command palette (${modKey})`}
      >
        <span aria-hidden="true">⌕</span> search & commands <kbd className="v-kbd">{modKey}</kbd>
      </button>
      {open && (
        <Modal label="Command palette" onClose={() => show(false)} className="fixed inset-0 z-[60] flex items-start justify-center bg-black/50 p-4 pt-[12vh] backdrop-blur-sm">
          <div className="v-palette w-full max-w-xl overflow-hidden rounded-2xl border border-muted bg-surface shadow-2xl">
            <input
              value={q}
              onChange={(e) => { setQ(e.target.value); setSel(0); }}
              autoFocus
              role="combobox"
              aria-expanded="true"
              aria-autocomplete="list"
              aria-controls={listId}
              aria-activedescendant={shown[sel] ? rowId(sel) : undefined}
              onKeyDown={(e) => {
                if (e.key === 'ArrowDown') { e.preventDefault(); setSel((s) => Math.min(shown.length - 1, s + 1)); }
                if (e.key === 'ArrowUp') { e.preventDefault(); setSel((s) => Math.max(0, s - 1)); }
                if (e.key === 'Enter') { e.preventDefault(); run(shown[sel]); }
              }}
              placeholder="Type a command, a screen or a theme…"
              aria-label="Command"
              className="w-full border-b border-muted bg-transparent px-4 py-3.5 text-lead text-text outline-none"
            />
            <ul id={listId} className="max-h-[50vh] overflow-y-auto p-2" role="listbox" aria-label="Commands">
              {shown.length === 0 && <li className="px-3 py-4 text-body text-muted">nothing matches. touch grass, try again 🌱</li>}
              {shown.map((c, i) => (
                <li key={c.id} id={rowId(i)} role="option" aria-selected={i === sel}>
                  <button
                    type="button"
                    tabIndex={-1}
                    onMouseEnter={() => setSel(i)}
                    onClick={() => run(c)}
                    className={`flex w-full items-center gap-3 rounded-xl px-3 py-2 text-left ${i === sel ? 'v-palette-sel bg-base' : ''}`}
                  >
                    <span className="w-20 shrink-0 text-tab uppercase tracking-wider text-muted">{c.group}</span>
                    <span className="min-w-0 flex-1 truncate text-body text-text">{c.label}</span>
                    {c.hint && <span className="hidden truncate text-tab text-muted sm:inline">{c.hint}</span>}
                    {c.keys && <kbd className="v-kbd">{c.keys}</kbd>}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        </Modal>
      )}
      {help && (
        <Modal label="Keyboard shortcuts" onClose={() => setHelp(false)} className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm">
          <div className="v-palette w-full max-w-md rounded-2xl border border-muted bg-surface p-5">
            <div className="mb-3 flex items-center justify-between gap-3">
              <h2 className="text-lead font-semibold text-text">Shortcuts</h2>
              <button type="button" onClick={() => setHelp(false)} aria-label="Close shortcuts" className="v-btn min-h-9 min-w-9 rounded-full text-muted hover:text-text">✕</button>
            </div>
            <ul className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-body">
              {[[`${modKey}  /`, 'command palette'], ['?', 'this sheet'], ['p', 'presentation mode'], ['r', 'refresh the desk'], ['Esc', 'close / leave presentation'], ...nav.filter((n) => n.key).map((n) => [`g ${n.key}`, n.label])].map(([k, v]) => (
                <li key={k} className="contents"><kbd className="v-kbd justify-self-start">{k}</kbd><span className="text-text">{v}</span></li>
              ))}
            </ul>
          </div>
        </Modal>
      )}
    </>
  );
}
