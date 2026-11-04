// Showcase tiles for demos: Vibe check (the live headline numbers),
// Agent feed (the audit chain as a scrolling timeline) and Ship clock
// (a countdown ring to whatever deadline you set). All three read the
// same APIs the rest of the app uses — nothing here is mocked.

import { useEffect, useMemo, useState } from 'react';
import Tile from './Tile';
import { listApprovals, listAudit, listMachines, listProviders, listTerminals, type AuditEntry } from '../../api';
import type { TileProps } from './tiles';

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null;

// A read that came back 'unreachable' is a failure, never an empty
// list: the tile keeps its last good numbers and its LED says error.
// 'offline' is a real answer (the server is up, no runner is linked),
// so it still counts as nothing live.
const failed = (v: unknown) => v === 'unreachable';

function usePoll(ms: number, syncKey: number): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), ms);
    return () => window.clearInterval(id);
  }, [ms]);
  return tick + syncKey * 100000;
}

const VERB: Record<string, string> = {
  'auth.login': 'signed in',
  'auth.logout': 'signed out',
  'terminal.open': 'spun up a terminal',
  'terminal.close': 'closed a terminal',
  'approval.approve': 'approved a risky step',
  'approval.deny': 'blocked a risky step',
  'provider.key': 'stashed a key in the vault',
};
const say = (a: string) => VERB[a] ?? a.replace(/[._]/g, ' ');

const ago = (iso: string) => {
  const t = new Date(iso).getTime();
  if (isNaN(t)) return '';
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  return s < 60 ? `${s}s` : s < 3600 ? `${Math.round(s / 60)}m` : s < 86400 ? `${Math.round(s / 3600)}h` : `${Math.round(s / 86400)}d`;
};

// ---- Vibe check ----
export function VibeTile({ syncKey, arranging, onRemove, onSettings, onUnauthorized, onOpenApprovals }: TileProps & { onOpenApprovals?: () => void }) {
  const tick = usePoll(8000, syncKey);
  const [s, setS] = useState<{ terms: number; machines: number; online: number; pending: number; providers: number; ready: number; events: AuditEntry[] } | null>(null);
  const [down, setDown] = useState(false);
  useEffect(() => {
    let alive = true;
    void Promise.all([listTerminals(), listMachines(), listApprovals('pending', 50), listProviders(), listAudit()]).then(([t, m, a, p, au]) => {
      if (!alive) return;
      if ([t, m, a, p, au].includes('unauthorized' as never)) { onUnauthorized?.(); return; }
      if ([t, m, a, p, au].some(failed)) { setDown(true); return; }
      setDown(false);
      const terms = isObj(t) && Array.isArray(t.terminals) ? t.terminals.length : 0;
      const ms = isObj(m) && Array.isArray(m.machines) ? m.machines : [];
      const pending = isObj(a) && typeof a.pending === 'number' ? a.pending : 0;
      const ps = Array.isArray(p) ? p : [];
      const events = isObj(au) && Array.isArray(au.entries) ? (au.entries as AuditEntry[]).slice(0, 12) : [];
      setS({ terms, machines: ms.length, online: ms.filter((x) => x.online).length, pending, providers: ps.length, ready: ps.filter((x) => x.ready).length, events });
    });
    return () => { alive = false; };
  }, [tick, onUnauthorized]);

  const mood = down ? (s ? "can't reach the server · last known numbers" : "can't reach the server") : !s ? 'warming up…' : s.pending > 0 ? `${s.pending} thing${s.pending === 1 ? '' : 's'} need your thumbs 👀` : s.terms > 0 ? 'agents are cooking 🔥' : 'calm. suspiciously calm. 🧘';
  return (
    <Tile title="vibe check" status={down ? 'error' : s ? 'live' : 'loading'} arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      <div className="flex h-full flex-col gap-3">
        <div>
          <div className="v-display v-grad-text text-2xl font-bold leading-tight sm:text-3xl">agents lift heavy.</div>
          <div className="v-display text-lead text-text">you stay in control.</div>
          <div className="mt-1 text-body text-muted">{mood}</div>
        </div>
        <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
          <Stat n={s?.terms} label="terminals live" />
          <Stat n={s ? `${s.online}/${s.machines || 0}` : undefined} label="machines online" />
          <button type="button" className="text-left" onClick={onOpenApprovals} disabled={!onOpenApprovals}>
            <Stat n={s?.pending} label="approvals waiting" hot={!!s && s.pending > 0} />
          </button>
          <Stat n={s ? `${s.ready}/${s.providers}` : undefined} label="models ready" />
        </div>
        {s && s.events.length > 0 && (
          <div className="v-marquee mt-auto text-tab text-muted">
            <div>
              {s.events.map((e) => (
                <span key={e.seq} className="mr-8">
                  <span className="text-accent">●</span> {e.actor} {say(e.action)}{e.target ? ` · ${e.target}` : ''} <span className="opacity-70">{ago(e.at)} ago</span>
                </span>
              ))}
            </div>
          </div>
        )}
      </div>
    </Tile>
  );
}

function Stat({ n, label, hot = false }: { n?: number | string; label: string; hot?: boolean }) {
  return (
    <div className="v-stat v-pop">
      <span className={`v-stat-num ${hot ? 'text-[#ff4fd8]' : 'text-text'}`}>{n === undefined ? '–' : n}</span>
      <span className="v-stat-label">{label}</span>
    </div>
  );
}

// ---- Agent feed ----
export function FeedTile({ syncKey, arranging, onRemove, onSettings, onUnauthorized }: TileProps) {
  const tick = usePoll(5000, syncKey);
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const [down, setDown] = useState(false);
  useEffect(() => {
    let alive = true;
    void listAudit().then((r) => {
      if (!alive) return;
      if (r === 'unauthorized') { onUnauthorized?.(); return; }
      if (failed(r)) { setDown(true); return; }
      setDown(false);
      setRows(isObj(r) && Array.isArray(r.entries) ? (r.entries as AuditEntry[]).slice(0, 25) : []);
    });
    return () => { alive = false; };
  }, [tick, onUnauthorized]);
  return (
    <Tile title="agent feed" status={down ? 'error' : rows ? 'live' : 'loading'} arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      {/* The LED is aria-hidden; tell screen readers the rows are stale. */}
      <span className="sr-only" aria-live="polite">{down && rows ? "can't reach the server · showing the last known feed" : ''}</span>
      {down && !rows ? (
        <div role="alert" className="text-body text-muted">can&apos;t reach the server</div>
      ) : !rows ? (
        <div className="text-body text-muted">tuning in…</div>
      ) : rows.length === 0 ? (
        <div className="text-body text-muted">no tea yet. open a terminal and let an agent cook.</div>
      ) : (
        <ul className="flex flex-col gap-2.5">
          {rows.map((e) => (
            <li key={e.seq} className="v-feed-item v-pop">
              <div className="flex items-baseline gap-2">
                <span className="truncate text-body text-text"><span className="font-semibold">{e.actor}</span> {say(e.action)}</span>
                <span className="ml-auto shrink-0 font-mono text-tab text-muted">{ago(e.at)}</span>
              </div>
              {e.target && <div className="truncate font-mono text-tab text-muted">{e.target}</div>}
            </li>
          ))}
        </ul>
      )}
    </Tile>
  );
}

// ---- Ship clock ----
// The hackathon window (Meta Global AI Developer Hackathon): Mon 26 Oct 18:00 CET
// to the Thu 5 Nov 03:00 CET deadline. ISO 8601 with an offset, the only form
// every engine's Date parser is required to read.
const DEFAULT_START = '2026-10-26T18:00:00+01:00';
const DEFAULT_DEADLINE = '2026-11-05T03:00:00+01:00';
export function ShipTile({ widget, arranging, onRemove, onSettings, onConfig }: TileProps) {
  const deadline = String(widget.config?.deadline ?? DEFAULT_DEADLINE);
  const start = String(widget.config?.start ?? DEFAULT_START);
  const [now, setNow] = useState(() => Date.now());
  const [editing, setEditing] = useState(false);
  const [badDate, setBadDate] = useState(false);
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);
  const end = new Date(deadline).getTime();
  const begin = new Date(start).getTime();
  // A deadline that does not parse (a hand-edited config, an old human
  // string) must not tick as NaN:NaN:NaN; it asks for a deadline instead.
  const unset = isNaN(end);
  const left = unset ? 0 : Math.max(0, end - now);
  const pct = useMemo(() => (isNaN(end) || isNaN(begin) || end <= begin ? 0 : Math.min(100, Math.max(0, ((now - begin) / (end - begin)) * 100))), [now, end, begin]);
  const h = Math.floor(left / 3600000);
  const m = Math.floor((left % 3600000) / 60000);
  const sec = Math.floor((left % 60000) / 1000);
  const pad = (n: number) => String(n).padStart(2, '0');
  const line = unset ? 'set a deadline' : left === 0 ? 'shipped. go touch grass 🌱' : h < 3 ? 'final boss energy ⚡' : h < 24 ? 'lock in. no side quests.' : 'plenty of runway. keep cooking.';
  return (
    <Tile title={widget.title || 'ship clock'} status={unset ? 'off' : 'live'} arranging={arranging} onRemove={onRemove} onSettings={onSettings ?? (() => { setBadDate(false); setEditing(true); })}>
      <div className="flex h-full items-center gap-4">
        <div className="v-ring shrink-0" style={{ ['--p' as string]: pct.toFixed(1) }}>
          <span className="v-display text-tab font-semibold text-text">{Math.round(pct)}%</span>
        </div>
        <div className="min-w-0">
          <div className="v-display font-mono text-2xl font-bold tabular-nums text-accent">{unset ? '--:--:--' : `${pad(h)}:${pad(m)}:${pad(sec)}`}</div>
          <div className="text-body text-text" role={unset ? 'status' : undefined}>{line}</div>
          <div className="text-tab text-muted">{unset ? 'pick one in settings' : `until ${new Date(end).toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit' })}`}</div>
          {editing && (
            <form className="mt-2 flex gap-1" onSubmit={(e) => {
              e.preventDefault();
              const v = (e.currentTarget.elements.namedItem('d') as HTMLInputElement).value;
              // Empty closes without saving. A value the browser accepts but Date
              // cannot hold (e.g. year 275760 west of UTC) stays open and says so.
              if (!v) { setEditing(false); return; }
              const t = new Date(v);
              if (isNaN(t.getTime())) { setBadDate(true); return; }
              onConfig?.({ ...(widget.config || {}), deadline: t.toISOString() });
              setEditing(false);
            }}>
              <input name="d" type="datetime-local" aria-invalid={badDate || undefined} onChange={() => setBadDate(false)} className="min-h-8 rounded bg-base px-2 text-tab text-text" />
              <button className="v-btn" type="submit">set</button>
            </form>
          )}
          {editing && badDate && <p className="text-tab text-red-400" role="alert">that date can't be used</p>}
        </div>
      </div>
    </Tile>
  );
}

// ---- Pulse: the last 24 hours of activity as hourly bars ----
export function PulseTile({ syncKey, arranging, onRemove, onSettings, onUnauthorized }: TileProps) {
  const tick = usePoll(30000, syncKey);
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const [at, setAt] = useState(0);
  const [down, setDown] = useState(false);
  useEffect(() => {
    let alive = true;
    // Two pages back is plenty for a day on a personal desk.
    void listAudit().then(async (r) => {
      if (!alive) return;
      if (r === 'unauthorized') { onUnauthorized?.(); return; }
      if (failed(r)) { setDown(true); return; }
      let all = isObj(r) && Array.isArray(r.entries) ? (r.entries as AuditEntry[]) : [];
      const last = all[all.length - 1];
      if (last && all.length >= 50) {
        const more = await listAudit(last.seq);
        if (isObj(more) && Array.isArray(more.entries)) all = all.concat(more.entries as AuditEntry[]);
      }
      if (alive) { setDown(false); setRows(all); setAt(Date.now()); }
    });
    return () => { alive = false; };
  }, [tick, onUnauthorized]);
  const buckets = useMemo(() => {
    const now = at;
    const b = Array.from({ length: 24 }, () => 0);
    for (const e of rows ?? []) {
      const t = new Date(e.at).getTime();
      if (isNaN(t)) continue;
      const h = Math.floor((now - t) / 3600000);
      if (h >= 0 && h < 24) b[23 - h] += 1;
    }
    return b;
  }, [rows, at]);
  const max = Math.max(1, ...buckets);
  const total = buckets.reduce((a, n) => a + n, 0);
  const peak = buckets.indexOf(Math.max(...buckets));
  return (
    <Tile title="pulse · 24h" status={down ? 'error' : rows ? 'live' : 'loading'} arranging={arranging} onRemove={onRemove} onSettings={onSettings}>
      {/* The LED is aria-hidden; tell screen readers the count is stale or missing. */}
      <span className="sr-only" aria-live="polite">{down ? (rows ? "can't reach the server · showing the last known count" : "can't reach the server") : ''}</span>
      <div className="flex h-full min-h-0 flex-col gap-2">
        <div className="flex items-baseline gap-2">
          <span className="v-stat-num text-text">{rows ? total : '–'}</span>
          <span className="v-stat-label">events · 24h</span>
          {rows && total > 0 && <span className="ml-auto text-tab text-muted">peak {new Date(at - (23 - peak) * 3600000).toLocaleTimeString([], { hour: '2-digit' })}</span>}
        </div>
        <div className="v-bars min-h-0 flex-1" role="img" aria-label={`${total} events in the last 24 hours`}>
          {buckets.map((n, i) => (
            <div key={i} className="v-bar" data-now={i === 23} style={{ height: `${Math.max(4, (n / max) * 100)}%`, opacity: n ? 1 : 0.35 }} title={`${n} event${n === 1 ? '' : 's'}, ${23 - i}h ago`} />
          ))}
        </div>
        <div className="flex justify-between text-tab text-muted"><span>24h ago</span><span>now</span></div>
      </div>
    </Tile>
  );
}
