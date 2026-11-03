// The data-driven Desk tiles: Now (weather), Meetings (ICS), Tickets
// (Jira/Asana/Linear) and the three News feeds. Each renders inside the
// shared Tile frame with its own status LED, copy ⧉ on every link, and a
// refresh ⟳ that bypasses the server cache.

import { useCallback, useEffect, useMemo, useState } from 'react';
import Tile, { statusOf, CopyButton } from './Tile';
import { useDeskFetch } from '../../lib/useDeskFetch';
import { deskError, getMeetings, getNews, getTickets, getWeather } from '../../lib/deskApi';

// ago is "how long since" for a feed timestamp, or "" when the stamp is
// not usable. A feed that sends no date (or one no layout parsed) must
// show nothing at all: the server once formatted a zero time as RFC3339,
// and the tile read "Fortune · 739923d ago". Anything before 2000 is
// such a placeholder, and a stamp in the future is a clock skew, not an
// age, so both answer "".
export const ago = (ts?: string): string => {
  if (!ts) return '';
  const t = new Date(ts).getTime();
  if (isNaN(t) || t < Date.parse('2000-01-01T00:00:00Z')) return '';
  const m = Math.max(0, Math.round((Date.now() - t) / 60000));
  return m < 60 ? `${m}m` : m < 1440 ? `${Math.round(m / 60)}h` : `${Math.round(m / 1440)}d`;
};

function Body({ data, loading, error, off, children }: { data: unknown; loading: boolean; error: string; off: React.ReactNode; children: React.ReactNode }) {
  if (!data && loading) return <div className="text-body text-muted">loading…</div>;
  if (!data && error) return <div className="text-body text-muted" role="status">{error}</div>;
  if (!data) return <>{off}</>;
  return <>{children}</>;
}

// ---- Now (weather) ----
export function NowTile({ syncKey, arranging, onRemove, onSettings, onWeather, onUnauthorized }: TileProps & { onWeather?: (w: { temp: number; text: string; location: string }) => void }) {
  const fetcher = useCallback((r: boolean) => getWeather(r) as Promise<never>, []);
  const { data, loading, error, reload } = useDeskFetch(fetcher, syncKey, onUnauthorized);
  const w = data?.weather as Record<string, unknown> | undefined;
  useEffect(() => {
    if (w) onWeather?.({ temp: Number(w.temp), text: String(w.text ?? ''), location: String(data?.location ?? '') });
  }, [w, data?.location, onWeather]);
  const status = statusOf(data, loading, error);
  return (
    <Tile title="now" status={status} updatedAt={data?.updated_at as string} arranging={arranging} onRemove={onRemove} onSettings={onSettings} onRefresh={reload}>
      <Body data={data} loading={loading} error={error} off={<div className="text-body text-muted">Set your location in Desk sources.</div>}>
        {w ? (
          <div className="flex flex-col gap-2">
            <div className="flex items-baseline gap-2">
              <span className="text-3xl font-semibold text-text">{Math.round(Number(w.temp))}°</span>
              <span className="text-body text-text">{String(w.text)}</span>
            </div>
            <div className="text-tab text-muted">
              {String(data?.location ?? '')}
              {w.feels_like !== undefined ? ` · feels ${Math.round(Number(w.feels_like))}°` : ''}
              {w.wind_kmh !== undefined ? ` · ${Math.round(Number(w.wind_kmh))} km/h` : ''}
              {w.humidity !== undefined ? ` · ${Number(w.humidity)}%` : ''}
            </div>
            {Array.isArray(w.days) && (
              <ul className="mt-1 flex flex-col gap-1">
                {(w.days as Array<Record<string, unknown>>).slice(0, 4).map((d) => (
                  <li key={String(d.date)} className="flex items-center gap-2 text-tab">
                    <span className="w-10 text-muted">{new Date(String(d.date) + 'T12:00:00').toLocaleDateString([], { weekday: 'short' })}</span>
                    <span className="text-text">{String(d.text)}</span>
                    <span className="ml-auto text-muted">{Math.round(Number(d.min))}° / {Math.round(Number(d.max))}°</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : (
          <div className="text-body text-muted">No weather yet.</div>
        )}
      </Body>
    </Tile>
  );
}

// ---- Meetings (ICS) ----
type Meeting = { uid: string; title: string; start: string; end: string; all_day?: boolean; location?: string; url?: string; feed?: string };
function whenLabel(m: Meeting, now = Date.now()): { text: string; now: boolean } {
  const s = new Date(m.start).getTime();
  const e = new Date(m.end).getTime();
  if (m.all_day) return { text: 'all day', now: s <= now && now < e };
  const hm = (t: string) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  if (s <= now && now < e) return { text: `now · ends ${hm(m.end)}`, now: true };
  const sameDay = new Date(s).toDateString() === new Date(now).toDateString();
  const day = sameDay ? '' : new Date(s).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' }) + ' ';
  return { text: `${day}${hm(m.start)}`, now: false };
}

export function MeetingsTile({ syncKey, arranging, onRemove, onSettings, onNext, onUnauthorized }: TileProps & { onNext?: (m: Meeting | null) => void }) {
  const fetcher = useCallback((r: boolean) => getMeetings(r) as Promise<never>, []);
  const { data, loading, error, reload } = useDeskFetch(fetcher, syncKey, onUnauthorized);
  const items = useMemo(() => (Array.isArray(data?.items) ? data?.items : []) as Meeting[], [data]);
  useEffect(() => {
    const now = Date.now();
    onNext?.(items.find((m) => new Date(m.end).getTime() > now) ?? null);
  }, [items, onNext]);
  const status = statusOf(data, loading, error);
  return (
    <Tile title="meetings" status={status} updatedAt={data?.updated_at as string} arranging={arranging} onRemove={onRemove} onSettings={onSettings} onRefresh={reload}>
      <Body data={data} loading={loading} error={error} off={<div className="text-body text-muted">Add a calendar feed (Google, Outlook) in Desk sources. No sign-in needed — a published ICS address.</div>}>
        {items.length === 0 ? (
          <div className="text-body text-muted">Nothing in the next 7 days.</div>
        ) : (
          <ul className="flex flex-col gap-2">
            {items.slice(0, 12).map((m) => {
              const w = whenLabel(m);
              return (
                <li key={m.uid + m.start} className="flex items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className={`truncate text-body ${w.now ? 'font-semibold text-accent' : 'text-text'}`}>{m.title}</span>
                      {m.feed && <span className="rounded bg-base px-1 text-tab text-muted">{m.feed}</span>}
                    </div>
                    <div className="text-tab text-muted">{w.text}{m.location ? ` · ${m.location}` : ''}</div>
                  </div>
                  {m.url && <CopyButton text={m.url} label="copy meeting link" />}
                  {m.url && <a className="shrink-0 rounded bg-accent px-2 py-0.5 text-tab text-on-accent" href={m.url} target="_blank" rel="noreferrer">join</a>}
                </li>
              );
            })}
          </ul>
        )}
      </Body>
    </Tile>
  );
}

// ---- Tickets (Jira / Asana / Linear) ----
type TicketRow = { key: string; summary: string; status: string; priority?: string; updated?: string; url: string };
const PRI: Record<string, string> = { Highest: '⇈', High: '↑', Medium: '→', Urgent: '⇈', Low: '↓', Lowest: '⇊' };

export function TicketsTile({ syncKey, arranging, onRemove, onSettings, onUnauthorized }: TileProps) {
  const fetcher = useCallback((r: boolean) => getTickets(r) as Promise<never>, []);
  const { data, loading, error, reload } = useDeskFetch(fetcher, syncKey, onUnauthorized);
  const items = (Array.isArray(data?.items) ? data?.items : []) as TicketRow[];
  const status = statusOf(data, loading, error);
  const provider = String(data?.provider ?? 'tickets');
  return (
    <Tile title={provider === 'tickets' ? 'tickets' : provider} status={status} updatedAt={data?.updated_at as string} arranging={arranging} onRemove={onRemove} onSettings={onSettings} onRefresh={reload}>
      <Body data={data} loading={loading} error={error} off={<div className="text-body text-muted">Connect Jira, Asana or Linear in Desk sources.</div>}>
        {items.length === 0 ? (
          <div className="text-body text-muted">Nothing assigned to you.</div>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {items.slice(0, 20).map((it) => (
              <li key={it.key} className="flex items-center gap-2">
                <a className="shrink-0 font-mono text-tab text-accent" href={it.url} target="_blank" rel="noreferrer">{it.key}</a>
                <span className="min-w-0 flex-1 truncate text-body text-text" title={it.summary}>{it.summary}</span>
                <span className="shrink-0 rounded bg-base px-1 text-tab text-muted" title={it.status}>{it.status}</span>
                {it.priority && <span className="shrink-0 text-muted" title={it.priority}>{PRI[it.priority] ?? ''}</span>}
                {it.updated && <span className="shrink-0 text-tab text-muted">{ago(it.updated)}</span>}
                <CopyButton text={it.url} label={`copy link to ${it.key}`} />
              </li>
            ))}
          </ul>
        )}
      </Body>
    </Tile>
  );
}

// ---- News (security / tech / custom) ----
type NewsItem = { title: string; url: string; source?: string; published?: string; summary?: string };
const NEWS_TITLES: Record<string, string> = { security: 'hacked today', tech: 'tech news' };

export function NewsTile({ widget, syncKey, arranging, onRemove, onSettings, onUnauthorized }: TileProps) {
  const which = widget.type === 'custom' ? 'custom' : widget.type;
  const fetcher = useCallback((r: boolean) => getNews(which, r) as Promise<never>, [which]);
  const { data, loading, error, reload } = useDeskFetch(fetcher, syncKey, onUnauthorized);
  const items = (Array.isArray(data?.items) ? data?.items : []) as NewsItem[];
  const [all, setAll] = useState(false);
  const status = statusOf(data, loading, error);
  const title = widget.title || NEWS_TITLES[widget.type] || 'news';
  const shown = all ? items : items.slice(0, 6);
  return (
    <Tile title={title} status={status} updatedAt={data?.updated_at as string} arranging={arranging} onRemove={onRemove} onSettings={onSettings} onRefresh={reload}>
      <Body data={data} loading={loading} error={error} off={<div className="text-body text-muted">Set this feed's search in Desk sources.</div>}>
        {items.length === 0 ? (
          <div className="text-body text-muted">No stories yet.</div>
        ) : (
          <>
            <ul className="flex flex-col gap-2">
              {shown.map((it) => {
                // An unreadable date is not an age: keep the source alone
                // rather than print a dangling "Fortune ·  ago".
                const age = ago(it.published);
                return (
                  <li key={it.url}>
                    <div className="flex items-start gap-1">
                      <a className="min-w-0 flex-1 text-body text-accent hover:underline" href={it.url} target="_blank" rel="noreferrer">{it.title}</a>
                      <CopyButton text={it.url} />
                    </div>
                    <div className="text-tab text-muted">{it.source}{age ? ` · ${age} ago` : ''}</div>
                    {it.summary && <div className="text-tab text-muted">{it.summary}</div>}
                  </li>
                );
              })}
            </ul>
            {items.length > 6 && (
              <button type="button" className="mt-2 rounded bg-base px-2 py-1 text-tab text-text" onClick={() => setAll((a) => !a)}>
                {all ? 'show less' : `show all ${items.length}`}
              </button>
            )}
          </>
        )}
      </Body>
    </Tile>
  );
}

export type TileProps = {
  widget: { id: string; type: string; title?: string; config?: Record<string, unknown> };
  syncKey: number;
  arranging: boolean;
  onRemove?: () => void;
  onSettings?: () => void;
  onConfig?: (cfg: Record<string, unknown>) => void;
  onUnauthorized?: () => void;
};

export { deskError };
