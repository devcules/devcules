import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { forwardRef, useEffect, useImperativeHandle } from 'react';
import CalendarTile from './CalendarTile';

// FullCalendar measures layout jsdom does not have, so stand in a thin
// grid: it reports the first window through datesSet on mount (as the real
// one does), lists the event titles it is given, and exposes getApi() so
// the ResizeObserver refit can be observed. `fc` holds the latest props so
// a test can page (datesSet) or drag-select (select) like a user would.
type FcProps = {
  events: { id: string; title: string; start: string; editable?: boolean; startEditable?: boolean; extendedProps: { m: { id: string } } }[];
  datesSet: (a: { startStr: string; endStr: string }) => void;
  select: (a: unknown) => void;
  eventClick: (a: unknown) => void;
  eventChange: (a: unknown) => Promise<void>;
};
const fc: { props: FcProps | null; updateSize: ReturnType<typeof vi.fn> } = { props: null, updateSize: vi.fn() };

vi.mock('@fullcalendar/react', () => ({
  default: forwardRef<unknown, FcProps>(function FakeCalendar(props, ref) {
    fc.props = props;
    useImperativeHandle(ref, () => ({ getApi: () => ({ updateSize: fc.updateSize }) }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
    useEffect(() => { props.datesSet(WEEK_A); }, []);
    return <ul data-testid="grid">{props.events.map((e) => <li key={e.id} data-start={e.start}>{e.title}</li>)}</ul>;
  }),
}));
vi.mock('@fullcalendar/daygrid', () => ({ default: {} }));
vi.mock('@fullcalendar/timegrid', () => ({ default: {} }));
vi.mock('@fullcalendar/list', () => ({ default: {} }));
vi.mock('@fullcalendar/interaction', () => ({ default: {} }));

const WEEK_A = { startStr: '2026-11-02T00:00:00', endStr: '2026-11-09T00:00:00' };
const WEEK_B = { startStr: '2026-11-09T00:00:00', endStr: '2026-11-16T00:00:00' };
const widget = { id: 'c1', type: 'calendar' } as unknown as Parameters<typeof CalendarTile>[0]['widget'];

const ev = (title: string) => ({ id: title, title, start: '2026-11-03T09:00:00Z', end: '2026-11-03T10:00:00Z', plan: true });
const body = (titles: string[]) => ({ events: titles.map(ev), configured: true, plans: titles.length });

function tile(syncKey = 0) {
  return <CalendarTile widget={widget} syncKey={syncKey} arranging={false} onRemove={vi.fn()} onSettings={vi.fn()} onUnauthorized={vi.fn()} />;
}
const calls = (f: ReturnType<typeof vi.fn>) => f.mock.calls.map((c) => String(c[0])).filter((u) => u.startsWith('/api/calendar/events?'));

describe('CalendarTile', () => {
  beforeEach(() => {
    fc.props = null;
    fc.updateSize = vi.fn();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // DESK-01: clearing the start field used to throw RangeError inside the
  // setState updater and unmount the whole app.
  it('clearing the start date keeps the plan sheet open and throws nothing', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json(body([]))));
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    render(tile());
    await waitFor(() => expect(fc.props).not.toBeNull());

    // onSelect is async; keep act synchronous so the draft flushes now.
    act(() => {
      void fc.props!.select({
        startStr: '2026-11-03T10:00:00', endStr: '2026-11-03T11:00:00', allDay: false,
        view: { calendar: { unselect: () => {} } },
      });
    });
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(screen.getByLabelText('title'), { target: { value: 'standup' } });
    const start = screen.getByLabelText('start') as HTMLInputElement;
    const add = screen.getByRole('button', { name: 'add it' }) as HTMLButtonElement;
    expect(add.disabled).toBe(false);

    fireEvent.change(start, { target: { value: '' } });

    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(screen.queryByText(/this tile crashed/)).toBeNull();
    expect(errors).not.toHaveBeenCalled();
    expect(start.value).toBe('');
    expect(start.getAttribute('aria-invalid')).toBe('true');
    expect(add.disabled).toBe(true); // nothing half-typed reaches the server

    fireEvent.change(start, { target: { value: '2026-11-03T10:30' } });
    expect(start.getAttribute('aria-invalid')).toBe('false');
    expect(add.disabled).toBe(false);
  });

  // GORT-06.1 (UI half): every occurrence of a series shared the plan's id,
  // and FullCalendar collapsed them into one event after a range change.
  it('gives each occurrence of a series its own id and keeps the series id for edits', async () => {
    const occ = (start: string) => ({ id: 'ev_yoga', title: 'yoga', start, end: start, all_day: true, plan: true, occurrence: true, rrule: 'FREQ=WEEKLY;BYDAY=FR' });
    const fridays = (days: string[]) => ({ events: days.map((d) => occ(`2026-11-${d}T00:00:00Z`)), configured: true, plans: 1 });
    const f = vi.fn(async (url: string) => Response.json(
      url.includes('from=' + encodeURIComponent(new Date(WEEK_B.startStr).toISOString())) ? fridays(['13']) : fridays(['06', '13', '20', '27']),
    ));
    vi.stubGlobal('fetch', f);
    render(tile());
    await waitFor(() => expect(screen.getAllByText('yoga')).toHaveLength(4));
    act(() => fc.props!.datesSet(WEEK_B)); // range change, then back
    await waitFor(() => expect(screen.getAllByText('yoga')).toHaveLength(1));
    act(() => fc.props!.datesSet(WEEK_A));
    await waitFor(() => expect(screen.getAllByText('yoga')).toHaveLength(4));

    const ids = fc.props!.events.map((e) => e.id);
    expect(new Set(ids).size).toBe(4);
    expect(ids).toContain('ev_yoga@2026-11-13T00:00:00Z');
    // Edits and deletes still go to the series.
    expect(fc.props!.events.every((e) => e.extendedProps.m.id === 'ev_yoga')).toBe(true);
  });

  // b8-check-2 item 1: a save or drag from an occurrence used to send the
  // occurrence's own start as the series start, so the series restarted
  // there and every earlier instance was deleted.
  describe('editing an occurrence of a series', () => {
    const SERIES = { id: 'ev_sync', title: 'team sync', plan: true, occurrence: true, rrule: 'FREQ=WEEKLY;BYDAY=MO',
      series_start: '2026-11-02T09:00:00Z', series_end: '2026-11-02T09:45:00Z', notes: 'agenda' };
    const occ = (day: string) => ({ ...SERIES, start: `2026-11-${day}T09:00:00Z`, end: `2026-11-${day}T09:45:00Z` });
    const oneOff = { id: 'ev_dent', title: 'dentist', start: '2026-11-04T08:00:00Z', end: '2026-11-04T08:30:00Z', plan: true };
    // The sheet's datetime-local text for an instant, in the test's zone.
    const local = (iso: string) => { const d = new Date(iso); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16); };
    type Sent = { method: string; url: string; body: Record<string, unknown> | null };

    const DATE_MSG = 'to change the date, end this series and create a new one';
    function setup(extra: unknown[] = []) {
      const sent: Sent[] = [];
      vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET';
        if (method !== 'GET') {
          sent.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : null });
          return method === 'DELETE' ? new Response(null, { status: 204 }) : Response.json({ ...SERIES });
        }
        return Response.json({ events: [occ('02'), occ('09'), occ('16'), occ('23'), oneOff, ...extra], configured: true, plans: 2 });
      }));
      render(tile());
      return sent;
    }
    async function openSheet(m: unknown, n = 5) {
      await waitFor(() => expect(fc.props!.events).toHaveLength(n));
      act(() => fc.props!.eventClick({ jsEvent: { preventDefault: () => {} }, event: { extendedProps: { m } } }));
      return screen.findByRole('dialog');
    }

    it('a title-only save from a later occurrence sends only the title', async () => {
      const sent = setup();
      await openSheet(occ('23'));
      fireEvent.change(screen.getByLabelText('title'), { target: { value: 'team sync renamed' } });
      fireEvent.click(screen.getByRole('button', { name: 'save' }));
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0].method).toBe('PATCH');
      expect(sent[0].url).toBe('/api/calendar/events/ev_sync');
      expect(sent[0].body).toEqual({ title: 'team sync renamed' });
    });

    it('location and notes edits send just those fields', async () => {
      const sent = setup();
      await openSheet(occ('16'));
      fireEvent.change(screen.getByLabelText('location'), { target: { value: 'room 4' } });
      fireEvent.change(screen.getByLabelText('notes'), { target: { value: 'new agenda' } });
      fireEvent.click(screen.getByRole('button', { name: 'save' }));
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0].body).toEqual({ location: 'room 4', notes: 'new agenda' });
    });

    it('a time-of-day change shifts the series start and end by the same delta', async () => {
      const sent = setup();
      await openSheet(occ('23'));
      fireEvent.change(screen.getByLabelText('start'), { target: { value: local('2026-11-23T09:30:00Z') } });
      fireEvent.change(screen.getByLabelText('end'), { target: { value: local('2026-11-23T10:30:00Z') } });
      fireEvent.click(screen.getByRole('button', { name: 'save' }));
      await waitFor(() => expect(sent).toHaveLength(1));
      // +30 min on the start, +45 min on the end, applied to 2 Nov.
      expect(sent[0].body).toEqual({ start: '2026-11-02T09:30:00.000Z', end: '2026-11-02T10:30:00.000Z' });
    });

    it('a date change from a later occurrence is refused inline and sends nothing', async () => {
      const sent = setup();
      await openSheet(occ('23'));
      fireEvent.change(screen.getByLabelText('start'), { target: { value: local('2026-11-24T09:00:00Z') } });
      fireEvent.change(screen.getByLabelText('end'), { target: { value: local('2026-11-24T09:45:00Z') } });
      expect(screen.getByRole('alert').textContent).toBe(DATE_MSG);
      const save = screen.getByRole('button', { name: 'save' }) as HTMLButtonElement;
      expect(save.disabled).toBe(true);
      fireEvent.submit(save.closest('form')!); // Enter in a field
      await new Promise((r) => setTimeout(r, 20));
      expect(sent).toHaveLength(0);
      expect(screen.getByRole('dialog')).toBeTruthy();
      // Back on the occurrence's own day: the message goes, save is back.
      fireEvent.change(screen.getByLabelText('start'), { target: { value: local('2026-11-23T09:00:00Z') } });
      fireEvent.change(screen.getByLabelText('end'), { target: { value: local('2026-11-23T09:45:00Z') } });
      expect(screen.queryByRole('alert')).toBeNull();
      expect(save.disabled).toBe(false);
    });

    it("the series' first event may not change its date either; its time still shifts the series", async () => {
      const sent = setup();
      await openSheet(occ('02'));
      // Mon 2 Nov -> Tue 3 Nov: BYDAY=MO never produces a Tuesday, so the
      // moved event would vanish (check-3 item 2). Refused like the rest.
      fireEvent.change(screen.getByLabelText('start'), { target: { value: local('2026-11-03T09:00:00Z') } });
      fireEvent.change(screen.getByLabelText('end'), { target: { value: local('2026-11-03T09:45:00Z') } });
      expect(screen.getByRole('alert').textContent).toBe(DATE_MSG);
      const save = screen.getByRole('button', { name: 'save' }) as HTMLButtonElement;
      expect(save.disabled).toBe(true);
      fireEvent.submit(save.closest('form')!);
      await new Promise((r) => setTimeout(r, 20));
      expect(sent).toHaveLength(0);
      // Same day, +30 min: allowed, and it is the series' time that moves.
      fireEvent.change(screen.getByLabelText('start'), { target: { value: local('2026-11-02T09:30:00Z') } });
      fireEvent.change(screen.getByLabelText('end'), { target: { value: local('2026-11-02T10:15:00Z') } });
      expect(screen.queryByRole('alert')).toBeNull();
      fireEvent.click(save);
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0].body).toEqual({ start: '2026-11-02T09:30:00.000Z', end: '2026-11-02T10:15:00.000Z' });
    });

    it('a rule without BYDAY refuses a first-event date change too', async () => {
      const sent = setup();
      await openSheet({ ...occ('02'), rrule: 'FREQ=WEEKLY' });
      fireEvent.change(screen.getByLabelText('start'), { target: { value: local('2026-11-04T09:00:00Z') } });
      fireEvent.change(screen.getByLabelText('end'), { target: { value: local('2026-11-04T09:45:00Z') } });
      expect(screen.getByRole('alert').textContent).toBe(DATE_MSG);
      expect((screen.getByRole('button', { name: 'save' }) as HTMLButtonElement).disabled).toBe(true);
      expect(sent).toHaveLength(0);
    });

    // check-3 item 1, "garden": all-day weekly from Thu 5 Nov, stored at
    // UTC midnight (01:00 CET). From its 19 Nov occurrence a "same-day"
    // time edit to 00:30 moved every instance to Wednesday. The time
    // fields are gone on an all-day occurrence, and a save sends only text.
    const GARDEN = { id: 'ev_garden', title: 'garden', plan: true, occurrence: true, all_day: true, rrule: 'FREQ=WEEKLY',
      series_start: '2026-11-05T00:00:00Z', series_end: '2026-11-06T00:00:00Z' };
    const garden = (day: string, next: string) => ({ ...GARDEN, start: `2026-11-${day}T01:00:00+01:00`, end: `2026-11-${next}T01:00:00+01:00` });
    it('an all-day occurrence (garden) has no time fields and its save sends only the title', async () => {
      const sent = setup([garden('19', '20')]);
      await openSheet(garden('19', '20'), 6);
      expect(screen.queryByLabelText('start')).toBeNull();
      expect(screen.queryByLabelText('end')).toBeNull();
      expect(screen.queryByRole('alert')).toBeNull();
      fireEvent.change(screen.getByLabelText('title'), { target: { value: 'garden club' } });
      fireEvent.change(screen.getByLabelText('link'), { target: { value: 'https://garden.example' } });
      fireEvent.click(screen.getByRole('button', { name: 'save' }));
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0]).toMatchObject({ method: 'PATCH', url: '/api/calendar/events/ev_garden' });
      expect(sent[0].body).toEqual({ title: 'garden club', url: 'https://garden.example' });
    });

    // check-3 item 1, "yoga": quick-add "yoga all day every friday", stored
    // at local midnight (6 Nov 00:00 CET = 5 Nov 23:00Z). Its first event
    // was moved to 7 Nov and nothing happened. Now the first all-day event
    // has no time fields either, and an Enter save sends only the text.
    const YOGA = { id: 'ev_yoga', title: 'yoga', plan: true, occurrence: true, all_day: true, rrule: 'FREQ=WEEKLY;BYDAY=FR',
      series_start: '2026-11-05T23:00:00Z', series_end: '2026-11-06T23:00:00Z' };
    const yoga = { ...YOGA, start: '2026-11-05T23:00:00Z', end: '2026-11-06T23:00:00Z' };
    it("an all-day series' first event (yoga) cannot change its date; location and notes still save", async () => {
      const sent = setup([yoga]);
      await openSheet(yoga, 6);
      expect(screen.queryByLabelText('start')).toBeNull();
      expect(screen.queryByLabelText('end')).toBeNull();
      fireEvent.change(screen.getByLabelText('location'), { target: { value: 'studio 2' } });
      fireEvent.change(screen.getByLabelText('notes'), { target: { value: 'mat' } });
      fireEvent.submit(screen.getByRole('button', { name: 'save' }).closest('form')!); // Enter
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0].body).toEqual({ location: 'studio 2', notes: 'mat' });
    });

    it('occurrences cannot be dragged or resized; one-off plans still can', async () => {
      const sent = setup();
      await waitFor(() => expect(fc.props!.events).toHaveLength(5));
      for (const e of fc.props!.events) {
        const once = e.extendedProps.m.id === 'ev_dent';
        expect(e.editable).toBe(once);
        expect(e.startEditable).toBe(once);
      }
      // A drop that reaches the handler anyway is reverted, unsent.
      const revert = vi.fn();
      await act(async () => {
        await fc.props!.eventChange({ revert, event: { extendedProps: { m: occ('16') }, startStr: '2026-11-17T09:00:00Z', endStr: '2026-11-17T09:45:00Z', allDay: false } });
      });
      expect(revert).toHaveBeenCalled();
      expect(sent).toHaveLength(0);
      // A one-off still moves.
      await act(async () => {
        await fc.props!.eventChange({ revert: vi.fn(), event: { extendedProps: { m: oneOff }, startStr: '2026-11-05T08:00:00Z', endStr: '2026-11-05T08:30:00Z', allDay: false } });
      });
      expect(sent).toHaveLength(1);
      expect(sent[0].url).toBe('/api/calendar/events/ev_dent');
      expect(sent[0].body).toMatchObject({ start: '2026-11-05T08:00:00.000Z' });
    });

    it('deleting from an occurrence asks before removing the whole series', async () => {
      const sent = setup();
      await openSheet(occ('23'));
      fireEvent.click(screen.getByRole('button', { name: 'delete' }));
      expect(screen.getByRole('alert').textContent).toContain('delete the whole series?');
      expect(sent).toHaveLength(0);
      fireEvent.click(screen.getByRole('button', { name: 'keep it' }));
      expect(screen.queryByRole('alert')).toBeNull();
      fireEvent.click(screen.getByRole('button', { name: 'delete' }));
      fireEvent.click(screen.getByRole('button', { name: 'delete all' }));
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0]).toMatchObject({ method: 'DELETE', url: '/api/calendar/events/ev_sync' });
    });

    it('a one-off plan deletes at once', async () => {
      const sent = setup();
      await openSheet(oneOff);
      fireEvent.click(screen.getByRole('button', { name: 'delete' }));
      await waitFor(() => expect(sent).toHaveLength(1));
      expect(sent[0]).toMatchObject({ method: 'DELETE', url: '/api/calendar/events/ev_dent' });
    });
  });

  // DESK-12: ⟳ sync / `r` bump syncKey; the calendar ignored it.
  it('a sync bump refetches the visible window once, with refresh', async () => {
    const f = vi.fn(async () => Response.json(body([])));
    vi.stubGlobal('fetch', f);
    const { rerender } = render(tile(0));
    await waitFor(() => expect(calls(f)).toHaveLength(1));
    expect(calls(f)[0]).not.toContain('refresh=1');

    rerender(tile(0)); // a plain re-render is not a sync
    rerender(tile(1));
    await waitFor(() => expect(calls(f)).toHaveLength(2));
    expect(calls(f)[1]).toContain('refresh=1');
    expect(calls(f)[1]).toContain('from=' + encodeURIComponent(new Date(WEEK_A.startStr).toISOString()));

    await new Promise((r) => setTimeout(r, 20));
    expect(calls(f)).toHaveLength(2);
  });

  // DESK-21: paging fast, the older week's slow answer must not win.
  it('drops an out-of-order response and keeps the latest week', async () => {
    const pending: Record<string, (r: Response) => void> = {};
    const f = vi.fn((url: string) => new Promise<Response>((res) => {
      pending[url.includes('from=' + encodeURIComponent(new Date(WEEK_B.startStr).toISOString())) ? 'B' : 'A'] = res;
    }));
    vi.stubGlobal('fetch', f);
    render(tile());
    await waitFor(() => expect(pending.A).toBeDefined());

    act(() => fc.props!.datesSet(WEEK_B));
    await waitFor(() => expect(pending.B).toBeDefined());

    await act(async () => { pending.B(Response.json(body(['week B event']))); });
    await screen.findByText('week B event');
    await act(async () => { pending.A(Response.json(body(['week A event']))); });

    expect(screen.getByText('week B event')).toBeTruthy();
    expect(screen.queryByText('week A event')).toBeNull();
  });

  // DESK-10: the grid refits when its box changes without a window resize.
  it('refits the grid when the tile box changes size, once per real change', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json(body([]))));
    let cb: ResizeObserverCallback | null = null;
    const disconnect = vi.fn();
    vi.stubGlobal('ResizeObserver', class {
      constructor(c: ResizeObserverCallback) { cb = c; }
      observe() {}
      unobserve() {}
      disconnect = disconnect;
    });
    vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => { fn(0); return 1; });
    vi.stubGlobal('cancelAnimationFrame', () => {});
    const size = (width: number, height: number) =>
      cb!([{ contentRect: { width, height } } as ResizeObserverEntry], {} as ResizeObserver);

    const { unmount } = render(tile());
    await waitFor(() => expect(cb).not.toBeNull());
    size(1148, 600);
    size(1420, 600); // presentation mode widened the tile
    size(1420, 600); // same box again: no extra refit
    expect(fc.updateSize).toHaveBeenCalledTimes(2);

    unmount();
    expect(disconnect).toHaveBeenCalled();
  });

  // DESK-13: the plan sheet is a real modal now: Escape closes it, and it
  // sits on <body>, so the tile's transform no longer clips its backdrop.
  it('the plan sheet is an aria-modal portal that Escape closes', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json(body([]))));
    const { container } = render(tile());
    await waitFor(() => expect(fc.props).not.toBeNull());
    act(() => {
      void fc.props!.select({
        startStr: '2026-11-03T10:00:00', endStr: '2026-11-03T11:00:00', allDay: false,
        view: { calendar: { unselect: () => {} } },
      });
    });
    const dialog = await screen.findByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(container.contains(dialog)).toBe(false);
    expect(document.activeElement).toBe(screen.getByLabelText('title'));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
