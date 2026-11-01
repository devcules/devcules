// CalendarTile — a month/week/list grid over the subscribed ICS feeds.
// Read-only for feeds (their events belong to their source); Kaithos' plan
// editing has no Devcules backend, so this is the honest beginning of the
// calendar surface: everything on your calendar, switchable views, a card
// with the join link on click. FullCalendar v6 draws the grid.

import { useCallback, useState } from 'react';
import FullCalendar from '@fullcalendar/react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import Tile, { statusOf, CopyButton } from './Tile';
import { useDeskFetch } from '../../lib/useDeskFetch';
import { getMeetings } from '../../lib/deskApi';
import type { TileProps } from './tiles';

type Meeting = { uid: string; title: string; start: string; end: string; all_day?: boolean; location?: string; url?: string; feed?: string };

export default function CalendarTile({ widget, syncKey, arranging, onRemove, onSettings, onUnauthorized }: TileProps) {
  const fetcher = useCallback((r: boolean) => getMeetings(r) as Promise<never>, []);
  const { data, loading, error, reload } = useDeskFetch(fetcher, syncKey, onUnauthorized);
  const items = (Array.isArray(data?.items) ? data?.items : []) as Meeting[];
  const [open, setOpen] = useState<Meeting | null>(null);
  const status = statusOf(data, loading, error);

  const events = items.map((m) => ({
    id: m.uid + m.start,
    title: m.title,
    start: m.start,
    end: m.end,
    allDay: !!m.all_day,
    extendedProps: { m },
  }));

  return (
    <Tile title={widget.title || 'calendar'} status={status} updatedAt={data?.updated_at as string} arranging={arranging} onRemove={onRemove} onSettings={onSettings} onRefresh={reload}>
      {items.length === 0 && data ? (
        <div className="text-body text-muted">
          {data.configured === false
            ? 'Add a calendar feed in Desk sources to fill this in.'
            : 'Nothing in the next 7 days.'}
        </div>
      ) : (
        <div className="d-calendar h-full">
          <FullCalendar
            plugins={[dayGridPlugin, timeGridPlugin, listPlugin]}
            initialView="dayGridMonth"
            headerToolbar={{ left: 'prev,next today', center: 'title', right: 'dayGridMonth,timeGridWeek,listWeek' }}
            events={events}
            height="100%"
            dayMaxEventRows={3}
            eventClick={(info) => { info.jsEvent.preventDefault(); setOpen((info.event.extendedProps as { m: Meeting }).m); }}
          />
        </div>
      )}
      {open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-label={open.title} onClick={() => setOpen(null)}>
          <div className="w-full max-w-sm rounded-lg border border-muted bg-surface p-4" onClick={(e) => e.stopPropagation()}>
            <div className="mb-1 flex items-start justify-between gap-2">
              <strong className="text-body text-text">{open.title}{open.feed ? ` · ${open.feed}` : ''}</strong>
              <button type="button" className="rounded px-1 text-muted hover:text-text" onClick={() => setOpen(null)} aria-label="close">✕</button>
            </div>
            <div className="mb-2 text-tab text-muted">
              {open.all_day ? 'all day' : `${new Date(open.start).toLocaleString([], { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })} — ${new Date(open.end).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`}
              {open.location ? ` · ${open.location}` : ''}
            </div>
            <div className="flex gap-2">
              {open.url && <a className="rounded bg-accent px-3 py-1 text-body text-on-accent" href={open.url} target="_blank" rel="noreferrer">join</a>}
              {open.url && <CopyButton text={open.url} label="copy meeting link" />}
            </div>
          </div>
        </div>
      )}
    </Tile>
  );
}
