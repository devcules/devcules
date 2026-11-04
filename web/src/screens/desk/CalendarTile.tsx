// CalendarTile — the desk calendar: your own plans (stored in devcules,
// editable) merged with the subscribed feeds (Outlook, Google… read-only),
// on a month/week/list grid (FullCalendar v6). Click-drag an empty stretch
// to create, drag an event to move, pull its edge to resize; feeds stay
// read-only. A quick-add bar parses "standup tomorrow 10:00 30m". Events
// for the visible window come from /api/calendar/events — the route that
// merges plans and feeds and expands recurring ones — refetched whenever
// the grid navigates to a new range.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import FullCalendar from '@fullcalendar/react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import interactionPlugin from '@fullcalendar/interaction';
import type { DateSelectArg, DatesSetArg, EventChangeArg, EventClickArg } from '@fullcalendar/core';
import Tile, { statusOf, CopyButton } from './Tile';
import Modal from '../../shell/Modal';
import {
  createCalendar, deleteCalendar, deskError, getCalendar, quickAddCalendar, updateCalendar, type CalEvent,
} from '../../lib/deskApi';
import type { TileProps } from './tiles';

type Range = { from: string; to: string };

// The backend parses every timestamp with RFC3339 strictly. FullCalendar's
// startStr/endStr are ISO strings without a zone when no timeZone is set, so
// marshal through Date and emit toISOString() (full RFC3339) everywhere.
const toRfc = (s: string) => new Date(s).toISOString();

// A datetime-local field reports '' while it is cleared or half-typed.
// new Date('').toISOString() throws a RangeError, so parse here and hand
// back null for anything that is not a real instant.
const fromLocalInput = (v: string): string | null => {
  if (!v) return null;
  const d = new Date(v);
  return isNaN(d.getTime()) ? null : d.toISOString();
};

// eventKey is the FullCalendar id of one item: a plan's id, plus the
// instance start for an occurrence of a series; a feed item is keyed by
// its feed, UID and start (an ICS series repeats its UID too).
const eventKey = (m: CalEvent): string =>
  m.plan ? (m.occurrence ? `${m.id}@${m.start}` : m.id) : `${m.feed}:${m.id}@${m.start}`;

// An occurrence of a series is edited through the series, which must
// never restart at the occurrence it was opened from. No occurrence may
// change its date, the series' first event included: a moved first event
// can land on a day the rule never produces (BYDAY), which drops it. A
// time-of-day change shifts the series' start and end (series_start,
// series_end) by the same delta. An all-day occurrence has no time to
// change: its sheet hides the time fields and its saves never send them.
const SERIES_DATE_MSG = 'to change the date, end this series and create a new one';
const sameDay = (a: string, b: string) => new Date(a).toDateString() === new Date(b).toDateString();
const occurrenceRefusal = (o: CalEvent, f: CalEvent): string | null => {
  if (!o.occurrence || o.all_day) return null;
  const moved = Date.parse(f.start) !== Date.parse(o.start) || Date.parse(f.end) !== Date.parse(o.end);
  if (!moved) return null;
  if (!o.series_start || !o.series_end) return SERIES_DATE_MSG;
  return sameDay(f.start, o.start) && sameDay(f.end, o.end) ? null : SERIES_DATE_MSG;
};
// seriesPatch is the PATCH for a save from an occurrence: only the text
// fields that changed, plus (timed series only) the series' times shifted
// by the edit's delta when the time moved. The PATCH merge keeps every
// field left out.
const seriesPatch = (o: CalEvent, f: CalEvent): Partial<CalEvent> => {
  const p: Partial<CalEvent> = {};
  for (const k of ['title', 'location', 'url', 'notes'] as const) {
    if ((f[k] ?? '') !== (o[k] ?? '')) p[k] = f[k] ?? '';
  }
  if (o.all_day) return p;
  const ds = Date.parse(f.start) - Date.parse(o.start);
  const de = Date.parse(f.end) - Date.parse(o.end);
  if ((ds || de) && o.series_start && o.series_end) {
    p.start = new Date(Date.parse(o.series_start) + ds).toISOString();
    p.end = new Date(Date.parse(o.series_end) + de).toISOString();
  }
  return p;
};

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
  const boxRef = useRef<HTMLDivElement>(null);
  // The latest range, for the Sync effect (which must not re-run on paging).
  const rangeRef = useRef<Range | null>(null);
  // Each load takes a ticket; only the newest ticket may write state, so a
  // slow answer for a week paged away from cannot overwrite the current one.
  const seqRef = useRef(0);
  // Week/month grids need width; land phones on the week list on first mount.
  const [initialView] = useState(() =>
    (typeof window !== 'undefined' && window.innerWidth < 768 ? 'listWeek' : 'timeGridWeek'));

  // Load the visible window: plans + feeds merged by the route. Called on
  // mount, whenever the grid navigates, when Sync bumps, and after a write.
  const load = useCallback(async (r: Range, refresh: boolean) => {
    const seq = ++seqRef.current;
    setLoading(true);
    const res = await getCalendar(r.from, r.to, refresh);
    if (res === 'unauthorized') { onUnauthorized?.(); return; }
    if (seq !== seqRef.current) return; // superseded by a newer load
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
    rangeRef.current = r;
    setRange(r);
    void load(r, false);
  }, [load]);

  // Desk "⟳ sync" and the `r` shortcut bump syncKey. FullCalendar does not
  // fire datesSet on a plain re-render, so refetch the visible window here.
  // Skip the mount run: datesSet already loads the first window.
  const seenSync = useRef(syncKey);
  useEffect(() => {
    if (seenSync.current === syncKey) return;
    seenSync.current = syncKey;
    if (rangeRef.current) void load(rangeRef.current, true);
  }, [syncKey, load]);

  // FullCalendar re-measures only on window resize. The tile also changes
  // size without one (presentation mode, sidebar drag, grid resize), so
  // watch the box and refit, at most once per frame and only when the box
  // really changed (updateSize cannot change the box: it is sized by the
  // grid cell, so this cannot loop).
  useEffect(() => {
    const el = boxRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    let frame = 0;
    let last = '';
    const ro = new ResizeObserver((entries) => {
      const box = entries[entries.length - 1]?.contentRect;
      const key = box ? `${Math.round(box.width)}x${Math.round(box.height)}` : '';
      if (key === last) return;
      last = key;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => calRef.current?.getApi().updateSize());
    });
    ro.observe(el);
    return () => { ro.disconnect(); cancelAnimationFrame(frame); };
  }, []);

  // Tile's ⟳ button calls reload() for a manual refresh.
  const items = useMemo(() => (Array.isArray(data?.events) ? data.events : []), [data]);
  // Each occurrence of a series needs its own FullCalendar id: occurrences
  // sharing one id collapse into a single event when the range changes.
  // Edits and deletes still target the series through extendedProps.m.id.
  const events = useMemo(() => items.map((m) => ({
    id: eventKey(m),
    title: m.title,
    start: m.start,
    end: m.end,
    allDay: !!m.all_day,
    // Feeds are read-only and arranging freezes editing. An occurrence of a
    // series is not dragged or resized: its sheet edits the series.
    editable: !arranging && !!m.plan && !m.occurrence,
    startEditable: !arranging && !!m.plan && !m.occurrence,
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
    if (!m.plan || m.occurrence) { arg.revert(); return; }
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
    // A save from an occurrence sends only what changed, onto the series.
    const occ = !draft && open?.occurrence ? open : null;
    if (occ && occurrenceRefusal(occ, e)) return; // the sheet says why
    setDraft(null);
    const patch = occ ? seriesPatch(occ, e) : null;
    if (patch && Object.keys(patch).length === 0) { setOpen(null); return; }
    const res = e.id ? await updateCalendar(e.id, patch ?? e) : await createCalendar(e);
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
            className="min-h-11 min-w-0 flex-1 rounded bg-base px-2 text-tab text-text"
          />
          <button type="submit" disabled={busy || !quick.trim()} className="min-h-11 rounded bg-accent px-3 text-tab text-on-accent disabled:opacity-60">
            add
          </button>
        </form>
        <div ref={boxRef} className="d-calendar relative min-h-0 flex-1">
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
  // The two datetime fields keep their own text, so a cleared or half-typed
  // value stays on screen while the user edits it. f.start/f.end only move
  // when the text is a real instant (see fromLocalInput); saving waits for
  // both fields to be valid.
  const [startTxt, setStartTxt] = useState(() => toLocal(event.start));
  const [endTxt, setEndTxt] = useState(() => toLocal(event.end));
  const timesOk = fromLocalInput(startTxt) !== null && fromLocalInput(endTxt) !== null;
  const setTime = (k: 'start' | 'end', setTxt: (v: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.target.value;
    setTxt(v);
    const iso = fromLocalInput(v);
    if (iso) setF((p) => ({ ...p, [k]: iso }));
  };
  const set = (k: keyof CalEvent) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setF((p) => ({ ...p, [k]: e.target.value }));
  const refusal = occurrenceRefusal(event, f);
  const [confirmDel, setConfirmDel] = useState(false);
  const when = event.all_day
    ? 'all day'
    : `${new Date(event.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} — ${new Date(event.end).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
  return (
    <Modal label={event.title || 'new plan'} onClose={onClose} className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <form className="w-full max-w-sm rounded-lg border border-muted bg-surface p-4" onSubmit={(e) => { e.preventDefault(); if (f.title.trim() && timesOk) onSave(f); }}>
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
            {event.occurrence && event.all_day ? (
              <div className="text-tab text-muted">all day · the series sets the days</div>
            ) : (
            <div className="grid grid-cols-2 gap-2">
              <label className="flex flex-col gap-1 text-tab text-muted">start
                <input type="datetime-local" className="min-h-10 rounded bg-base px-2 text-body text-text" value={startTxt}
                  aria-invalid={fromLocalInput(startTxt) === null} onChange={setTime('start', setStartTxt)} />
              </label>
              <label className="flex flex-col gap-1 text-tab text-muted">end
                <input type="datetime-local" className="min-h-10 rounded bg-base px-2 text-body text-text" value={endTxt}
                  aria-invalid={fromLocalInput(endTxt) === null} onChange={setTime('end', setEndTxt)} />
              </label>
            </div>
            )}
            {refusal && <div role="alert" className="text-tab text-red-400">{refusal}</div>}
            <input className="min-h-10 rounded bg-base px-3 text-body text-text" placeholder="location" aria-label="location" value={f.location ?? ''} onChange={set('location')} />
            <input className="min-h-10 rounded bg-base px-3 text-body text-text" placeholder="link" aria-label="link" value={f.url ?? ''} onChange={set('url')} />
            <textarea className="min-h-20 rounded bg-base px-3 py-2 text-body text-text" placeholder="notes" aria-label="notes" value={f.notes ?? ''} onChange={set('notes')} />
            <div className="flex gap-2">
              {confirmDel ? (
                <span role="alert" className="flex-1 self-center text-tab text-red-400">delete the whole series?</span>
              ) : (
                <button type="submit" disabled={!f.title.trim() || !timesOk || !!refusal} className="min-h-11 flex-1 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">
                  {creating ? 'add it' : 'save'}
                </button>
              )}
              {!creating && (
                <button type="button" className="min-h-11 rounded bg-base px-3 text-body text-red-400"
                  onClick={() => (event.occurrence && !confirmDel ? setConfirmDel(true) : onDelete(f.id))}>
                  {confirmDel ? 'delete all' : 'delete'}
                </button>
              )}
              {confirmDel && (
                <button type="button" className="min-h-11 rounded bg-base px-3 text-body text-text" onClick={() => setConfirmDel(false)}>keep it</button>
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
    </Modal>
  );
}
