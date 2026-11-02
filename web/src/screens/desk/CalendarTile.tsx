// CalendarTile — the desk calendar: your own plans (stored in devcules,
// editable) merged with the subscribed feeds (Outlook, Google… read-only),
// on a month/week/list grid (FullCalendar v6). Click-drag an empty stretch
// to create, drag an event to move, pull its edge to resize; feeds stay
// read-only. A quick-add bar parses "standup tomorrow 10:00 30m". Events
// for the visible window come from /api/calendar/events — the route that
// merges plans and feeds and expands recurring ones — refetched whenever
// the grid navigates to a new range.

import { useCallback, useMemo, useRef, useState } from 'react';
import FullCalendar from '@fullcalendar/react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import interactionPlugin from '@fullcalendar/interaction';
import type { DateSelectArg, DatesSetArg, EventChangeArg, EventClickArg } from '@fullcalendar/core';
import Tile, { statusOf, CopyButton } from './Tile';
import {
  createCalendar, deleteCalendar, deskError, getCalendar, quickAddCalendar, updateCalendar, type CalEvent,
} from '../../lib/deskApi';
import type { TileProps } from './tiles';

type Range = { from: string; to: string };

// The backend parses every timestamp with RFC3339 strictly. FullCalendar's
// startStr/endStr are ISO strings without a zone when no timeZone is set, so
// marshal through Date and emit toISOString() (full RFC3339) everywhere.
const toRfc = (s: string) => new Date(s).toISOString();

export default function CalendarTile({ widget, syncKey, arranging, onRemove, onSettings, onUnauthorized }: TileProps) {
  const [range, setRange] = useState<Range | null>(null);
  const [data, setData] = useState<{ events: CalEvent[]; configured: boolean; stale?: boolean } | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [open, setOpen] = useState<CalEvent | null>(null);
  const [draft, setDraft] = useState<CalEvent | null>(null);
  const [quick, setQuick] = useState('');
  const [busy, setBusy] = useState(false);
  const calRef = useRef<FullCalendar>(null);
  const lastSync = useRef(syncKey);
  // Week/month grids need width; land phones on the week list on first mount.
  const [initialView] = useState(() =>
    (typeof window !== 'undefined' && window.innerWidth < 768 ? 'listWeek' : 'timeGridWeek'));

  // Load the visible window: plans + feeds merged by the route. Called on
  // mount, whenever the grid navigates, when Sync bumps, and after a write.
  const load = useCallback(async (r: Range, refresh: boolean) => {
    setLoading(true);
    const res = await getCalendar(r.from, r.to, refresh);
    if (res === 'unauthorized') { onUnauthorized?.(); return; }
    if (res === 'unreachable') { setError('server unreachable'); setLoading(false); return; }
    const dErr = deskError(res);
    if (dErr) { setError(dErr); setLoading(false); return; }
    setError('');
    setData(res);
    setLoading(false);
  }, [onUnauthorized]);

  const reload = useCallback(() => {
    if (range) void load(range, true);
  }, [range, load]);

  // FullCalendar asks the grid which window to render; we fetch for it.
  // The datesSet arg carries the start/end of what's on screen.
  const onDatesSet = useCallback((arg: DatesSetArg) => {
    const r = { from: toRfc(arg.startStr), to: toRfc(arg.endStr) };
    setRange(r);
    void load(r, lastSync.current !== syncKey);
    lastSync.current = syncKey;
  }, [load, syncKey]);

  // The Sync button bumped: refetch the current window, bypassing caches.
  // Read during render (not in an effect) so a bump while mounted
  // refetches before paint.
  if (lastSync.current !== syncKey) {
    lastSync.current = syncKey;
    if (range) void load(range, true);
  }

  const items = useMemo(() => (Array.isArray(data?.events) ? data.events : []), [data]);
  const events = useMemo(() => items.map((m) => ({
    id: (m.plan ? m.id : m.feed + ':' + m.id),
    title: m.title,
    start: m.start,
    end: m.end,
    allDay: !!m.all_day,
    editable: !arranging && !!m.plan, // feeds are read-only; arranging freezes editing
    backgroundColor: m.plan ? undefined : '',
    classNames: m.plan ? ['d-cal-plan'] : ['d-cal-feed'],
    extendedProps: { m },
  })), [items, arranging]);

  const status = statusOf(data as never, loading, error);

  // Click-drag an empty slot to make a plan. A plan (not a feed event) is
  // created with the title from the sheet that opens.
  const onSelect = useCallback(async (sel: DateSelectArg) => {
    const title = '';
    setDraft({
      id: '', title, start: toRfc(sel.startStr), end: toRfc(sel.endStr), all_day: sel.allDay, plan: true,
    });
    sel.view.calendar.unselect();
  }, []);

  // Drag-move / edge-resize a plan. Feeds are never editable so only plans
  // arrive here; revert the drop on a failed write.
  const onChange = useCallback(async (arg: EventChangeArg) => {
    const m = (arg.event.extendedProps as { m: CalEvent }).m;
    if (!m.plan) { arg.revert(); return; }
    const next: CalEvent = {
      ...m,
      start: toRfc(arg.event.startStr),
      end: arg.event.endStr ? toRfc(arg.event.endStr) : new Date(arg.event.start!.getTime() + 30 * 60000).toISOString(),
      all_day: arg.event.allDay,
    };
    const res = await updateCalendar(m.id, next);
    if (res === 'unauthorized') { onUnauthorized?.(); return; }
    if (res === 'unreachable' || deskError(res)) { arg.revert(); return; }
    reload();
  }, [onUnauthorized, reload]);

  const onClick = useCallback((info: EventClickArg) => {
    info.jsEvent.preventDefault();
    setOpen((info.event.extendedProps as { m: CalEvent }).m);
  }, []);

  const quickAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    const text = quick.trim();
    if (!text) return;
    setBusy(true);
    const res = await quickAddCalendar(text);
    setBusy(false);
    if (res === 'unauthorized') { onUnauthorized?.(); return; }
    if (res === 'unreachable' || deskError(res)) { setError(deskError(res) ?? 'could not add that'); return; }
    setQuick('');
    setError('');
    reload();
  };

  const saveDraft = async (e: CalEvent) => {
    setDraft(null);
    const res = e.id ? await updateCalendar(e.id, e) : await createCalendar(e);
    if (res === 'unauthorized') { onUnauthorized?.(); return; }
    if (res === 'unreachable' || deskError(res)) { setError(deskError(res) ?? 'save failed'); return; }
    reload();
  };

  return (
    <Tile
      title={widget.title || 'calendar'} status={status}
      updatedAt={undefined}
      meta={`${items.filter((x) => !x.feed && new Date(x.start).toDateString() === new Date().toDateString()).length} today`}
      arranging={arranging} onRemove={onRemove} onSettings={onSettings} onRefresh={reload}
    >
      <div className="flex h-full min-h-0 flex-1 flex-col gap-2">
        <form onSubmit={quickAdd} className="flex gap-1">
          <input
            value={quick} onChange={(e) => setQuick(e.target.value)}
            placeholder="add: standup tomorrow 10:00 30m · gym every weekday 7:00"
            aria-label="quick add a calendar event" disabled={busy}
            className="min-h-9 min-w-0 flex-1 rounded bg-base px-2 text-tab text-text"
          />
          <button type="submit" disabled={busy || !quick.trim()} className="min-h-9 rounded bg-accent px-3 text-tab text-on-accent disabled:opacity-60">
            add
          </button>
        </form>
        <div className="d-calendar relative min-h-0 flex-1">
          {error && <div role="alert" className="mb-1 text-tab text-red-400">{error}</div>}
          <FullCalendar
            ref={calRef}
            plugins={[dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin]}
            initialView={initialView}
            headerToolbar={{ left: 'prev,next today', center: 'title', right: 'timeGridWeek,dayGridMonth,listWeek' }}
            selectLongPressDelay={450}
            eventLongPressDelay={450}
            events={events}
            height="100%"
            allDaySlot={true}
            allDayText="all day"
            nowIndicator={true}
            scrollTime={`${String(Math.max(0, new Date().getHours() - 1)).padStart(2, '0')}:00:00`}
            dayMaxEventRows={3}
            selectable={!arranging}
            selectMirror={true}
            editable={!arranging}
            select={onSelect}
            eventChange={onChange}
            eventClick={onClick}
            datesSet={onDatesSet}
          />
          {!loading && items.length === 0 && data?.configured === false && (
            <div className="pointer-events-none absolute inset-x-0 top-1/2 -translate-y-1/2 text-center text-body text-muted">
              Add a calendar feed in Desk sources to see your meetings.
            </div>
          )}
        </div>
      </div>

      {(open || draft) && (
        <EventSheet
          event={draft ?? open!}
          creating={!draft ? false : true}
          onClose={() => { setOpen(null); setDraft(null); }}
          onSave={saveDraft}
          onDelete={async (id) => {
            setOpen(null);
            const res = await deleteCalendar(id);
            if (res === 'unauthorized') { onUnauthorized?.(); return; }
            reload();
          }}
        />
      )}
    </Tile>
  );
}

// EventSheet — a plan's card: edit the fields, save or delete; a feed
// event's card is read-only (it belongs to its source) plus join/copy.
function EventSheet({ event, creating, onClose, onSave, onDelete }: {
  event: CalEvent; creating: boolean;
  onClose: () => void; onSave: (e: CalEvent) => void; onDelete: (id: string) => void;
}) {
  const editable = event.plan === true;
  const [f, setF] = useState<CalEvent>(event);
  const toLocal = (iso: string) => {
    const d = new Date(iso);
    return isNaN(d.getTime()) ? iso.slice(0, 16) : new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  };
  const set = (k: keyof CalEvent) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setF((p) => ({ ...p, [k]: e.target.value }));
  const when = event.all_day
    ? 'all day'
    : `${new Date(event.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} — ${new Date(event.end).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-label={event.title || 'new plan'} onClick={onClose}>
      <form className="w-full max-w-sm rounded-lg border border-muted bg-surface p-4" onClick={(e) => e.stopPropagation()} onSubmit={(e) => { e.preventDefault(); onSave(f); }}>
        <div className="mb-1 flex items-start justify-between gap-2">
          <strong className="text-body text-text">
            {creating ? 'Plan it' : (event.title || 'plan')}
            {event.occurrence && <span className="ml-2 rounded bg-base px-1 text-tab text-muted">repeats — editing the series</span>}
          </strong>
          <button type="button" className="rounded px-1 text-muted hover:text-text" onClick={onClose} aria-label="close">✕</button>
        </div>
        <div className="mb-3 text-tab text-muted">{when}{event.feed ? ` · ${event.feed}` : ''}{event.location ? ` · ${event.location}` : ''}</div>

        {editable ? (
          <div className="flex flex-col gap-2">
            <input autoFocus className="min-h-11 rounded bg-base px-3 text-body text-text" placeholder="title" aria-label="title"
              value={f.title} onChange={set('title')} />
            <div className="grid grid-cols-2 gap-2">
              <label className="flex flex-col gap-1 text-tab text-muted">start
                <input type="datetime-local" className="min-h-10 rounded bg-base px-2 text-body text-text" value={toLocal(f.start)}
                  onChange={(e) => setF((p) => ({ ...p, start: new Date(e.target.value).toISOString() }))} />
              </label>
              <label className="flex flex-col gap-1 text-tab text-muted">end
                <input type="datetime-local" className="min-h-10 rounded bg-base px-2 text-body text-text" value={toLocal(f.end)}
                  onChange={(e) => setF((p) => ({ ...p, end: new Date(e.target.value).toISOString() }))} />
              </label>
            </div>
            <input className="min-h-10 rounded bg-base px-3 text-body text-text" placeholder="location" aria-label="location" value={f.location ?? ''} onChange={set('location')} />
            <input className="min-h-10 rounded bg-base px-3 text-body text-text" placeholder="link" aria-label="link" value={f.url ?? ''} onChange={set('url')} />
            <textarea className="min-h-20 rounded bg-base px-3 py-2 text-body text-text" placeholder="notes" aria-label="notes" value={f.notes ?? ''} onChange={set('notes')} />
            <div className="flex gap-2">
              <button type="submit" disabled={!f.title.trim()} className="min-h-11 flex-1 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">
                {creating ? 'add it' : 'save'}
              </button>
              {!creating && (
                <button type="button" className="min-h-11 rounded bg-base px-3 text-body text-red-400" onClick={() => onDelete(f.id)}>delete</button>
              )}
            </div>
          </div>
        ) : (
          <div className="flex gap-2">
            {event.url && <a className="rounded bg-accent px-3 py-2 text-body text-on-accent" href={event.url} target="_blank" rel="noreferrer">join</a>}
            {event.url && <CopyButton text={event.url} label="copy meeting link" />}
            <span className="ml-auto self-center text-tab text-muted">from {event.feed || 'a feed'} — edit it there</span>
          </div>
        )}
      </form>
    </div>
  );
}
