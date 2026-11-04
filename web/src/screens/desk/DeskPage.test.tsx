import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import DeskPage from './DeskPage';
import { DESK_VERSION } from '../../lib/deskPrefs';
import { DESK_SAVE_LOST, resetDeskSave } from '../../lib/deskApi';
import type { ToastEvent } from '../../lib/toast';
import { toDesk } from '../../shell/CommandPalette';

// react-grid-layout measures a container jsdom does not lay out, so stand
// in a thin grid that renders its children and keeps its latest props, so
// a test can fire the grid's callbacks the way rgl does: a breakpoint echo
// is onLayoutChange alone; a user drag is onDragStop and then, in the same
// call, onLayoutChange.
type Item = { i: string; x: number; y: number; w: number; h: number };
type GridProps = {
  children: ReactNode;
  layouts: Record<string, Item[]>;
  onLayoutChange: (l: Item[], all: Record<string, Item[]>) => void;
  onDragStop?: (...a: unknown[]) => void;
  onResizeStop?: (...a: unknown[]) => void;
};
const grid: { props: GridProps | null } = { props: null };
vi.mock('react-grid-layout', () => ({
  Responsive: (p: GridProps) => { grid.props = p; return <div data-testid="grid">{p.children}</div>; },
  useContainerWidth: () => ({ width: 1440, containerRef: { current: null }, mounted: true }),
  verticalCompactor: {},
}));
vi.mock('react-grid-layout/css/styles.css', () => ({}));
vi.mock('react-resizable/css/styles.css', () => ({}));

// The data tiles are not under test: plain stubs, and one that throws in
// its own render (above its Tile) when `crash.now` is set.
const crash = { now: false };
vi.mock('./tiles', () => ({
  NowTile: () => { if (crash.now) throw new Error('now tile broke'); return <div>now tile</div>; },
  MeetingsTile: () => <div>meetings</div>,
  TicketsTile: () => <div>tickets</div>,
  NewsTile: ({ widget }: { widget: { id: string } }) => <div>news {widget.id}</div>,
}));
vi.mock('./CalendarTile', () => ({ default: () => <div>calendar</div> }));
vi.mock('./showcaseTiles', () => ({
  VibeTile: () => <div>vibe</div>, FeedTile: () => <div>feed</div>,
  PulseTile: () => <div>pulse</div>, ShipTile: () => <div>ship</div>,
}));

type Doc = { version: number; widgets: Array<{ id: string; type: string; config?: Record<string, unknown> }>; layouts: Record<string, Item[]>; seen: Record<string, unknown> };
const SERVER_DOC: Doc = {
  version: DESK_VERSION,
  widgets: [{ id: 'w-now', type: 'now' }, { id: 'w-scratch', type: 'scratch', config: { text: '' } }, { id: 'w-custom-1', type: 'custom', config: { query: 'rust' } }],
  layouts: {
    lg: [
      { i: 'w-now', x: 0, y: 0, w: 4, h: 5 },
      { i: 'w-scratch', x: 4, y: 0, w: 4, h: 6 },
      { i: 'w-custom-1', x: 8, y: 0, w: 4, h: 8 },
    ],
  },
  seen: {},
};

type Call = { url: string; method: string; body?: string; keepalive?: boolean };
let calls: Call[] = [];
let prefsGet: () => Promise<Response>;
let prefsPut: (body: string) => Promise<Response>;
const puts = () => calls.filter((c) => c.url === '/api/desk/prefs' && c.method === 'PUT');
const putDoc = (k = 0) => JSON.parse(puts()[k].body ?? '{}') as Doc;

function stubFetch() {
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    calls.push({ url, method, body: init?.body as string | undefined, keepalive: init?.keepalive });
    if (url === '/api/desk/prefs' && method === 'GET') return prefsGet();
    if (url === '/api/desk/prefs' && method === 'PUT') return prefsPut(init?.body as string);
    if (method === 'PUT') return new Response(null, { status: 204 });
    return Response.json({});
  }));
}

// toastLog records the app-wide toasts raised from now on.
function toastLog(): ToastEvent[] {
  const seen: ToastEvent[] = [];
  const on = (e: Event) => seen.push((e as CustomEvent<ToastEvent>).detail);
  window.addEventListener('devcules:toast', on);
  offToasts.push(() => window.removeEventListener('devcules:toast', on));
  return seen;
}
const offToasts: Array<() => void> = [];

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const page = () => <DeskPage onSessionExpired={vi.fn()} />;

describe('DeskPage', () => {
  beforeEach(() => {
    calls = [];
    grid.props = null;
    crash.now = false;
    prefsGet = async () => Response.json(SERVER_DOC);
    prefsPut = async () => new Response(null, { status: 204 });
    resetDeskSave();
    stubFetch();
  });
  afterEach(() => {
    offToasts.splice(0).forEach((off) => off());
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // DESK-02: a failed GET used to render the starter set and let the next
  // layout echo PUT it over the user's real desk.
  it('a failed prefs load shows a retry line and never writes', async () => {
    prefsGet = async () => { throw new TypeError('network down'); };
    render(page());
    expect((await screen.findByRole('alert')).textContent).toMatch(/couldn.t load your desk/i);
    // No grid, so no echo can write; nor can the palette's add.
    expect(grid.props).toBeNull();
    act(() => { window.dispatchEvent(new CustomEvent('devcules:desk', { detail: { action: 'add' } })); });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect((screen.getByRole('button', { name: /add$/ }) as HTMLButtonElement).disabled).toBe(true);
    await sleep(500);
    expect(puts()).toHaveLength(0);

    // Retry reads the real document; still nothing is written by loading.
    prefsGet = async () => Response.json(SERVER_DOC);
    fireEvent.click(screen.getByRole('button', { name: 'retry' }));
    await screen.findByText('now tile');
    await sleep(500);
    expect(puts()).toHaveLength(0);
  });

  it('a 5xx prefs answer is a failed load too, and a never-saved desk counts as loaded', async () => {
    prefsGet = async () => new Response('boom', { status: 503 });
    const { unmount } = render(page());
    await screen.findByRole('alert');
    unmount();

    calls = [];
    prefsGet = async () => Response.json({ version: 0, widgets: [], layouts: {} });
    render(page());
    // The starter set renders (Now is in it), and no error shows.
    await screen.findByText('now tile');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  // DESK-05: breakpoint echoes (resize, presentation) PUT and audited the
  // desk every time; only a user drag or resize stop may write.
  it('a breakpoint echo writes nothing; a drag stop writes once', async () => {
    render(page());
    await screen.findByText('now tile');
    const lg = SERVER_DOC.layouts.lg;
    // An echo: the same layout in another order, then a regenerated md one.
    const reordered = [lg[2], lg[0], lg[1]];
    act(() => { grid.props!.onLayoutChange(reordered, { lg: reordered }); });
    const md = lg.map((l) => ({ ...l, w: 1 }));
    act(() => { grid.props!.onLayoutChange(md, { lg, md }); });
    await sleep(550);
    expect(puts()).toHaveLength(0);

    // A user drag: onDragStop, then onLayoutChange in the same tick.
    const moved = [{ ...lg[0], x: 8 }, { ...lg[1], x: 0 }, { ...lg[2], x: 4 }];
    act(() => {
      grid.props!.onDragStop!(moved);
      grid.props!.onLayoutChange(moved, { lg: moved, md });
    });
    await waitFor(() => expect(puts()).toHaveLength(1));
    const doc = putDoc();
    expect(doc.layouts.lg.find((l) => l.i === 'w-now')).toMatchObject({ x: 8 });
    // Only the breakpoint the user arranged at is written from the grid.
    expect(doc.layouts.md.find((l) => l.i === 'w-now')?.w).not.toBe(1);
    // A later echo after the drag still writes nothing.
    act(() => { grid.props!.onLayoutChange(md, { lg: moved, md }); });
    await sleep(500);
    expect(puts()).toHaveLength(1);
  });

  // DESK-03: leaving the Desk inside the scratchpad's 800ms debounce
  // dropped the note.
  it('unmounting within the debounce still saves the scratchpad note', async () => {
    const { unmount } = render(page());
    const pad = await screen.findByRole('textbox', { name: 'scratchpad' });
    fireEvent.change(pad, { target: { value: 'lost-note-123' } });
    await sleep(100);
    unmount();
    await waitFor(() => expect(puts().length).toBeGreaterThanOrEqual(1));
    const last = JSON.parse(puts().at(-1)!.body!) as Doc;
    expect(last.widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: 'lost-note-123' });
    // An in-app leave keeps the page alive: a normal PUT, no keepalive.
    expect(puts().at(-1)!.keepalive).toBeFalsy();
  });

  // Check round 1, item 1: a leave with both the desk debounce (a drag)
  // and the scratchpad debounce pending sends ONE merged save.
  it('leaving with a desk change and a note pending sends one merged save', async () => {
    const { unmount } = render(page());
    const pad = await screen.findByRole('textbox', { name: 'scratchpad' });
    const lg = SERVER_DOC.layouts.lg;
    const moved = [{ ...lg[0], x: 8 }, { ...lg[1], x: 0 }, { ...lg[2], x: 4 }];
    act(() => {
      grid.props!.onDragStop!(moved);
      grid.props!.onLayoutChange(moved, { lg: moved });
    });
    fireEvent.change(pad, { target: { value: 'both-at-once' } });
    unmount();
    await sleep(1000);
    expect(puts()).toHaveLength(1);
    const doc = putDoc();
    expect(doc.widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: 'both-at-once' });
    expect(doc.layouts.lg.find((l) => l.i === 'w-now')).toMatchObject({ x: 8 });
  });

  // Check round 1, item 1: the leave-time save lands only AFTER the return
  // visit's GET has been answered (a slow uplink). The Desk must show the
  // note, and the next unrelated change must not PUT the old document over
  // it; nor may that next save overtake the leave save.
  it('a leave save that lands after the return load keeps the note', async () => {
    let server: Doc = SERVER_DOC;
    prefsGet = async () => Response.json(server);
    let releaseLeave: () => void = () => {};
    const order: string[] = [];
    let started = 0;
    prefsPut = (body) => {
      const n = started++;
      order.push(`start${n}`);
      const done = () => { server = JSON.parse(body) as Doc; order.push(`done${n}`); return new Response(null, { status: 204 }); };
      if (n === 0) return new Promise<Response>((res) => { releaseLeave = () => res(done()); });
      return Promise.resolve(done());
    };
    const first = render(page());
    const pad = await screen.findByRole('textbox', { name: 'scratchpad' });
    fireEvent.change(pad, { target: { value: 'race-note-1' } });
    first.unmount();
    await waitFor(() => expect(puts()).toHaveLength(1)); // held in flight

    // Come back: the GET answers the old server document first.
    render(page());
    await screen.findByText('now tile');
    expect((screen.getByRole('textbox', { name: 'scratchpad' }) as HTMLTextAreaElement).value).toBe('race-note-1');

    // An unrelated change while the leave save is still in flight waits
    // for it instead of racing it.
    const lg = SERVER_DOC.layouts.lg;
    const moved = [{ ...lg[0], x: 8 }, lg[1], lg[2]];
    act(() => { grid.props!.onDragStop!(moved); grid.props!.onLayoutChange(moved, { lg: moved }); });
    await sleep(550);
    expect(puts()).toHaveLength(1);
    await act(async () => { releaseLeave(); await sleep(0); });
    await waitFor(() => expect(puts()).toHaveLength(2));
    await sleep(50);
    expect(order).toEqual(['start0', 'done0', 'start1', 'done1']);
    expect(server.widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: 'race-note-1' });
    expect(server.layouts.lg.find((l) => l.i === 'w-now')).toMatchObject({ x: 8 });
  });

  // A leave save that failed is not lost either: while the server still
  // holds the document it was built on, the return visit shows it and
  // sends it again.
  it('a failed leave save is shown and re-sent when the server is unchanged', async () => {
    let fail = true;
    prefsPut = async () => (fail ? Promise.reject(new TypeError('offline')) : new Response(null, { status: 204 }));
    const toasts = toastLog();
    const first = render(page());
    fireEvent.change(await screen.findByRole('textbox', { name: 'scratchpad' }), { target: { value: 'kept-offline' } });
    first.unmount();
    await waitFor(() => expect(puts()).toHaveLength(1));
    await sleep(20);
    fail = false;
    render(page());
    await screen.findByText('now tile');
    expect((screen.getByRole('textbox', { name: 'scratchpad' }) as HTMLTextAreaElement).value).toBe('kept-offline');
    await waitFor(() => expect(puts()).toHaveLength(2));
    expect(putDoc(1).widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: 'kept-offline' });
    expect(toasts).toEqual([]);
  });

  // ...but once another tab or device has written in between, the failed
  // copy is stale: re-sending it would revert that newer write. The server
  // document is shown, nothing is written, and a toast says so.
  it('a failed leave save is dropped with a toast when the server changed in between', async () => {
    prefsPut = async () => new Response('bad gateway', { status: 502 });
    const toasts = toastLog();
    const first = render(page());
    fireEvent.change(await screen.findByRole('textbox', { name: 'scratchpad' }), { target: { value: 'stale-note' } });
    first.unmount();
    await waitFor(() => expect(puts()).toHaveLength(1));
    await sleep(20);
    prefsPut = async () => new Response(null, { status: 204 });
    const other: Doc = { ...SERVER_DOC, widgets: SERVER_DOC.widgets.map((w) => (w.id === 'w-scratch' ? { ...w, config: { text: 'other-src' } } : w)) };
    prefsGet = async () => Response.json(other);
    render(page());
    await screen.findByText('now tile');
    expect((screen.getByRole('textbox', { name: 'scratchpad' }) as HTMLTextAreaElement).value).toBe('other-src');
    await sleep(900);
    expect(puts()).toHaveLength(1);
    expect(toasts).toEqual([{ message: DESK_SAVE_LOST, tone: 'bad' }]);
  });

  // A 4xx (too big, too many widgets) is refused every time: drop it and
  // say so at once, instead of keeping it as if offline.
  it('a leave save refused with a 4xx is dropped with a toast, not re-sent', async () => {
    prefsPut = async () => Response.json({ error: 'too many widgets' }, { status: 400 });
    const toasts = toastLog();
    const first = render(page());
    fireEvent.change(await screen.findByRole('textbox', { name: 'scratchpad' }), { target: { value: 'refused' } });
    first.unmount();
    await waitFor(() => expect(toasts).toEqual([{ message: DESK_SAVE_LOST, tone: 'bad' }]));
    expect(puts()).toHaveLength(1);
    prefsPut = async () => new Response(null, { status: 204 });
    render(page());
    await screen.findByText('now tile');
    expect((screen.getByRole('textbox', { name: 'scratchpad' }) as HTMLTextAreaElement).value).toBe('');
    await sleep(900);
    expect(puts()).toHaveLength(1);
    expect(toasts).toHaveLength(1);
  });

  it('pagehide flushes a pending desk save with keepalive', async () => {
    render(page());
    await screen.findByText('now tile');
    const pad = screen.getByRole('textbox', { name: 'scratchpad' });
    fireEvent.change(pad, { target: { value: 'closing the tab' } });
    act(() => { window.dispatchEvent(new Event('pagehide')); });
    expect(puts()).toHaveLength(1);
    expect(puts()[0].keepalive).toBe(true);
    expect(putDoc().widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: 'closing the tab' });
    // The flushed debounce does not write a second time.
    await sleep(900);
    expect(puts()).toHaveLength(1);
  });

  it('a hidden tab flushes with keepalive; a note over the byte budget goes as a plain PUT', async () => {
    render(page());
    await screen.findByText('now tile');
    const pad = screen.getByRole('textbox', { name: 'scratchpad' });
    const vis = vi.spyOn(document, 'visibilityState', 'get');
    fireEvent.change(pad, { target: { value: 'ünïcødé ✓ 日本' } });
    vis.mockReturnValue('hidden');
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    expect(puts()).toHaveLength(1);
    expect(puts()[0].keepalive).toBe(true);
    expect(putDoc().widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: 'ünïcødé ✓ 日本' });

    // 25,016 chars of 日 is about 75 KB of UTF-8: under 60,000 chars, but
    // over the keepalive budget in bytes, so it must not use keepalive.
    vis.mockReturnValue('visible');
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    const big = '日'.repeat(25_016);
    fireEvent.change(pad, { target: { value: big } });
    act(() => { window.dispatchEvent(new Event('pagehide')); });
    await waitFor(() => expect(puts()).toHaveLength(2));
    expect(puts()[1].keepalive).toBeFalsy();
    expect(putDoc(1).widgets.find((w) => w.id === 'w-scratch')?.config).toMatchObject({ text: big });
  });

  // DESK-07: the custom feed's configure sheet pointed at an id addWidget
  // never used, so it never opened.
  it('adding a custom feed opens its configure sheet', async () => {
    render(page());
    await screen.findByText('now tile');
    fireEvent.click(screen.getByRole('button', { name: /add$/ }));
    const add = screen.getByRole('dialog', { name: 'Add a widget' });
    expect(add).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /Custom feed/ }));
    const sheet = await screen.findByRole('dialog', { name: 'configure custom' });
    expect(sheet.getAttribute('aria-modal')).toBe('true');
    expect(screen.queryByRole('dialog', { name: 'Add a widget' })).toBeNull();
    // Saving names the new widget, not some other one.
    fireEvent.change(screen.getByPlaceholderText(/rust, local sports/), { target: { value: 'zig' } });
    fireEvent.click(screen.getByRole('button', { name: 'save' }));
    await waitFor(() => expect(puts().length).toBeGreaterThanOrEqual(1));
    const doc = JSON.parse(puts().at(-1)!.body!) as Doc;
    const added = doc.widgets.filter((w) => w.type === 'custom' && w.id !== 'w-custom-1');
    expect(added).toHaveLength(1);
    expect(added[0].config).toMatchObject({ query: 'zig' });
  });

  // DESK-13: Escape closes the desk dialogs and stops there.
  it('Escape closes Add a widget and does not reach window shortcuts', async () => {
    render(page());
    await screen.findByText('now tile');
    const opener = screen.getByRole('button', { name: /add$/ });
    opener.focus();
    fireEvent.click(opener);
    const dlg = screen.getByRole('dialog', { name: 'Add a widget' });
    expect(dlg.contains(document.activeElement)).toBe(true);
    const onWin = vi.fn();
    window.addEventListener('keydown', onWin);
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    window.removeEventListener('keydown', onWin);
    expect(screen.queryByRole('dialog', { name: 'Add a widget' })).toBeNull();
    expect(onWin).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(opener);
  });

  // DESK-06: the palette's "add a widget" run from another screen switches
  // to the Desk before it mounts; the Desk takes the held action and opens
  // Add once its document has loaded.
  it('a palette add-widget from another screen opens Add after mount', async () => {
    let answer: (r: Response) => void = () => {};
    prefsGet = () => new Promise<Response>((r) => { answer = r; });
    toDesk(undefined, 'add');
    render(page());
    expect(screen.queryByRole('dialog', { name: 'Add a widget' })).toBeNull();
    await act(async () => { answer(Response.json(SERVER_DOC)); });
    expect(await screen.findByRole('dialog', { name: 'Add a widget' })).toBeTruthy();
  });

  it('a held add is dropped when the desk fails to load, even after a retry', async () => {
    prefsGet = async () => { throw new TypeError('network down'); };
    toDesk(undefined, 'add');
    render(page());
    await screen.findByRole('alert');
    prefsGet = async () => Response.json(SERVER_DOC);
    fireEvent.click(screen.getByRole('button', { name: 'retry' }));
    await screen.findByText('now tile');
    expect(screen.queryByRole('dialog', { name: 'Add a widget' })).toBeNull();
  });

  it('palette desk actions reach a mounted desk (sources)', async () => {
    render(page());
    await screen.findByText('now tile');
    act(() => { toDesk(undefined, 'sources'); });
    expect(await screen.findByRole('dialog')).toBeTruthy();
  });

  // B4 follow-up: a throw in a widget's own render (above its Tile) shows
  // the tile notice; the rest of the desk keeps working.
  it('a widget that throws in its own render shows the tile notice only', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    crash.now = true;
    render(page());
    await screen.findByRole('textbox', { name: 'scratchpad' });
    expect(screen.getByRole('alert').textContent).toMatch(/this tile crashed/);
    expect(screen.getByRole('region', { name: 'Now' })).toBeTruthy();
    crash.now = false;
    fireEvent.click(screen.getByRole('button', { name: 'reload' }));
    expect(await screen.findByText('now tile')).toBeTruthy();
  });
});
