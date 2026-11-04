// Desk — the instrument panel: a glance strip up top (clock, next meeting,
// weather) and a movable grid of widget tiles below. Arrange mode turns on
// drag grips and resize corners; layouts persist per breakpoint to
// /api/desk/prefs. The save is a whole-document PUT (last writer wins),
// so nothing is written until the server's own copy has been read.

import { useCallback, useEffect, useRef, useState } from 'react';
import { Responsive, useContainerWidth, verticalCompactor, type Layout, type ResponsiveLayouts } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import {
  addWidget, BREAKPOINTS, COLS, MARGIN, normalizePrefs, newWidgetId,
  removeWidget, ROW_HEIGHT, sameLayout, setLayouts, toDocument, updateWidget, WIDGET_TYPES, type LayoutItem, type PrefsDoc, type Widget,
} from '../../lib/deskPrefs';
import { deskError, deskLoaded, deskLoadMark, getPrefs, saveDesk, type Prefs } from '../../lib/deskApi';
import Tile from './Tile';
import { MeetingsTile, NewsTile, NowTile, TicketsTile } from './tiles';
import CalendarTile from './CalendarTile';
import { DESK_FLUSH, LinksTile, NextUpTile, ScratchTile, TimerTile, TodosTile } from './localTiles';
import DeskSources from './DeskSources';
import { listenDesk } from '../../shell/CommandPalette';
import { FeedTile, PulseTile, ShipTile, VibeTile } from './showcaseTiles';
import { toast } from '../../lib/toast';
import Modal from '../../shell/Modal';
import ErrorBoundary, { TileCrashed } from '../../shell/ErrorBoundary';

function Clock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(id);
  }, []);
  return (
    <div className="flex items-baseline gap-3">
      <span className="v-display v-grad-text whitespace-nowrap text-2xl font-bold tabular-nums sm:text-3xl">{now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
      <span className="text-body text-muted">{now.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' })}</span>
    </div>
  );
}

export default function DeskPage({ onSessionExpired, onOpenApprovals }: { onSessionExpired: () => void; onOpenApprovals?: () => void }) {
  const [prefs, setPrefsState] = useState<PrefsDoc | null>(null);
  // 'failed' when the server's document could not be read: the desk then
  // shows a retry line and writes nothing, since a PUT of the starter set
  // would replace the user's real desk.
  const [load, setLoad] = useState<'loading' | 'ready' | 'failed'>('loading');
  const [attempt, setAttempt] = useState(0);
  const [arranging, setArranging] = useState(false);
  const [syncKey, setSyncKey] = useState(0);
  const [drawer, setDrawer] = useState<null | 'add' | 'sources' | { widget: string }>(null);
  const [next, setNext] = useState<{ title: string; start: string } | null>(null);
  const [weather, setWeather] = useState<{ temp: number; text: string; location: string } | null>(null);
  const saveTimer = useRef<number | null>(null);
  // prefsRef is the document as of the last change, read synchronously so
  // a change (and its save) also works from cleanup, after unmount.
  const prefsRef = useRef<PrefsDoc | null>(null);
  // Set only once a real server document has been read.
  const loadedFromServer = useRef(false);
  // A palette "add a widget" that arrived before the desk loaded.
  const wantAdd = useRef(false);
  const pendingSave = useRef<PrefsDoc | null>(null);
  const alive = useRef(true);
  // True while the page is hidden (pagehide or a hidden tab): a save then
  // goes out at once, with keepalive.
  const hiding = useRef(false);
  // A flush is queued for the end of the current task (after unmount).
  const flushQueued = useRef(false);
  // Set by a user drag or resize; only then is a layout change saved.
  const userEdit = useRef(false);

  const { width, containerRef, mounted } = useContainerWidth();
  // Mirror the current breakpoint from the container width (the same pick
  // the grid makes), so props that shape it can react.
  const bp = width >= BREAKPOINTS.lg ? 'lg' : width >= BREAKPOINTS.md ? 'md'
    : width >= BREAKPOINTS.sm ? 'sm' : width >= BREAKPOINTS.xs ? 'xs' : 'xxs';

  // Boot: read the server document and normalize. On a fresh desk the
  // server answers an empty document, which counts as loaded, and
  // normalizePrefs answers the default starter set. An unreachable server
  // (or an error answer) is NOT an empty desk: show the retry line.
  // A save from an earlier visit that the server has not confirmed: one
  // still in flight wins (it lands after this GET); one that failed is
  // shown and sent again only if the server still holds the document it
  // was built on, else it is dropped with a toast (deskLoaded).
  useEffect(() => {
    let live = true;
    const mark = deskLoadMark();
    void getPrefs().then((r) => {
      if (!live) return;
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable' || deskError(r) !== null) { wantAdd.current = false; setLoad('failed'); return; }
      loadedFromServer.current = true;
      prefsRef.current = normalizePrefs(deskLoaded(r, mark, onSessionExpired));
      setPrefsState(prefsRef.current);
      setLoad('ready');
      if (wantAdd.current) { wantAdd.current = false; setDrawer('add'); }
    });
    return () => { live = false; };
  }, [onSessionExpired, attempt]);

  // flushSave hands the pending document to the ordered desk save (one
  // in flight at a time, newest wins, kept past this component's life).
  // keepalive only when the page is being hidden or torn down.
  const flushSave = useCallback((keepalive = false) => {
    if (saveTimer.current !== null) { window.clearTimeout(saveTimer.current); saveTimer.current = null; }
    const doc = pendingSave.current;
    pendingSave.current = null;
    if (!doc || !loadedFromServer.current) return;
    saveDesk(toDocument(doc) as unknown as Prefs, keepalive, onSessionExpired);
  }, [onSessionExpired]);

  // After unmount, the tiles' own cleanups may still add changes (a note
  // in its debounce): collect them all and send ONE save, as a normal PUT
  // since the page stays alive, once the unmount's cleanups have run.
  const flushLater = useCallback(() => {
    if (flushQueued.current) return;
    flushQueued.current = true;
    queueMicrotask(() => { flushQueued.current = false; flushSave(false); });
  }, [flushSave]);

  // Debounced persist: every prefs change saves 400ms after the last one,
  // so a drag across the grid writes the final layout once.
  const persist = useCallback((doc: PrefsDoc) => {
    if (!loadedFromServer.current) return;
    pendingSave.current = doc;
    if (!alive.current) { flushLater(); return; }
    if (hiding.current) { flushSave(true); return; }
    if (saveTimer.current !== null) window.clearTimeout(saveTimer.current);
    saveTimer.current = window.setTimeout(() => flushSave(), 400);
  }, [flushSave, flushLater]);

  // Leaving the Desk or the page flushes a pending save instead of
  // dropping it. The tiles flush their own debounces first (DESK_FLUSH,
  // dispatched synchronously), so desk and scratchpad go as one save.
  useEffect(() => {
    alive.current = true;
    const onHide = () => {
      window.dispatchEvent(new Event(DESK_FLUSH));
      hiding.current = true;
      flushSave(true);
    };
    const onShow = () => { hiding.current = false; };
    const onVis = () => { if (document.visibilityState === 'hidden') onHide(); else onShow(); };
    window.addEventListener('pagehide', onHide);
    window.addEventListener('pageshow', onShow);
    document.addEventListener('visibilitychange', onVis);
    return () => {
      window.removeEventListener('pagehide', onHide);
      window.removeEventListener('pageshow', onShow);
      document.removeEventListener('visibilitychange', onVis);
      alive.current = false;
      window.dispatchEvent(new Event(DESK_FLUSH));
      flushLater();
    };
  }, [flushSave, flushLater]);

  const update = useCallback((fn: (p: PrefsDoc) => PrefsDoc) => {
    const prev = prefsRef.current;
    if (!prev) return;
    const next = fn(prev);
    if (next === prev) return;
    prefsRef.current = next;
    setPrefsState(next);
    persist(next);
  }, [persist]);

  // The grid fires onLayoutChange when the USER drags or resizes a tile,
  // but also echoes it on mount and on every breakpoint switch (resize,
  // presentation mode). Writing the echoes back changes the layouts prop,
  // and rgl's refresh-from-props effect then freezes that layout for every
  // breakpoint, killing responsiveness; it also PUT (and audited) the desk
  // on every resize. So only a change that follows a drag or resize stop
  // is kept, and only for the breakpoint it was made at. rgl calls
  // onDragStop/onResizeStop and then, synchronously, onLayoutChange.
  const onUserEdit = useCallback(() => {
    userEdit.current = true;
    queueMicrotask(() => { userEdit.current = false; });
  }, []);
  const onLayoutChange = useCallback((current: Layout, all: ResponsiveLayouts<string>) => {
    if (!userEdit.current) return;
    userEdit.current = false;
    const at = Object.keys(all).find((k) => (all as Record<string, unknown>)[k] === current);
    if (!at) return;
    update((p) => {
      if (sameLayout(p.layouts[at], current as readonly LayoutItem[])) return p;
      return setLayouts(p, { ...p.layouts, [at]: current as unknown as LayoutItem[] });
    });
  }, [update]);

  // NOTE: no early return when !prefs — the loading line renders INSIDE the
  // ref'd scroll container below. useContainerWidth's observer attaches in a
  // one-shot effect, so that container must exist on the very first render;
  // if it mounts late, the effect sees a null node, never observes, and the
  // grid stays frozen at the hook's initial 1280px no matter the screen.

  const renderWidget = (w: Widget) => {
    const common = {
      widget: w, syncKey, arranging,
      onRemove: () => { update((p) => removeWidget(p, w.id)); toast(`${WIDGET_TYPES[w.type]?.title ?? 'Widget'} removed`); },
      onSettings: () => setDrawer(WIDGET_TYPES[w.type]?.configurable ? { widget: w.id } : 'sources'),
      onConfig: (cfg: Record<string, unknown>) => update((p) => updateWidget(p, w.id, { config: cfg })),
      onUnauthorized: onSessionExpired,
    };
    switch (w.type) {
      case 'vibe': return <VibeTile key={w.id} {...common} onOpenApprovals={onOpenApprovals} />;
      case 'feed': return <FeedTile key={w.id} {...common} />;
      case 'pulse': return <PulseTile key={w.id} {...common} />;
      case 'ship': return <ShipTile key={w.id} {...common} onSettings={undefined} />;
      case 'now': return <NowTile key={w.id} {...common} onWeather={setWeather} />;
      case 'meetings': return <MeetingsTile key={w.id} {...common} onNext={setNext as never} />;
      case 'calendar': return <CalendarTile key={w.id} {...common} />;
      case 'tickets': return <TicketsTile key={w.id} {...common} />;
      case 'security': case 'tech': case 'custom': return <NewsTile key={w.id} {...common} />;
      case 'brief': return <NextUpTile key={w.id} {...common} onOpenApprovals={onOpenApprovals} />;
      case 'todos': return <TodosTile key={w.id} {...common} />;
      case 'timer': return <TimerTile key={w.id} {...common} />;
      case 'links': return <LinksTile key={w.id} {...common} />;
      case 'scratch': return <ScratchTile key={w.id} {...common} />;
      default: return <Tile key={w.id} title={w.type} status="off" arranging={arranging} onRemove={common.onRemove}><div className="text-body text-muted">Unknown widget.</div></Tile>;
    }
  };

  // The command palette drives the desk (listenDesk), including an action
  // it ran from another screen just before this mount (held). "add" needs
  // the loaded desk, so a held one waits for this mount's load (wantAdd).
  useEffect(() => listenDesk((a, held) => {
    if (a === 'sync') setSyncKey((k) => k + 1);
    else if (a === 'add') { if (prefsRef.current) setDrawer('add'); else if (held) wantAdd.current = true; }
    else if (a === 'sources') setDrawer('sources');
    else if (a === 'arrange') setArranging((x) => !x);
  }), []);

  const absent = (type: string) => !(prefs?.widgets.some((w) => w.type === type) ?? false);

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      {/* Console strip: the glance. Clock + date, next meeting, weather,
          then sync / arrange / add / sources. */}
      <div className="v-strip flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-muted bg-surface px-4 py-3">
        <Clock />
        {next && (
          <div className="v-chip">
            next: <span className="text-text">{next.title}</span> · {new Date(next.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </div>
        )}
        {weather && (
          <div className="v-chip">
            {Math.round(weather.temp)}° <span className="text-text">{weather.text}</span>{weather.location ? ` · ${weather.location}` : ''}
          </div>
        )}
        <div className="ml-auto flex items-center gap-1">
          <button type="button" className="v-btn" onClick={() => setSyncKey((k) => k + 1)} title="refetch every widget">⟳ sync</button>
          <button type="button" aria-pressed={arranging} className="v-btn disabled:opacity-50" disabled={!prefs} onClick={() => setArranging((a) => !a)} title="drag and resize tiles">
            {arranging ? '✓ done' : '✥ arrange'}
          </button>
          <button type="button" className="v-btn disabled:opacity-50" disabled={!prefs} onClick={() => setDrawer('add')} title="add a widget">＋ add</button>
          <button type="button" className="v-btn" onClick={() => setDrawer('sources')} title="calendar, news, tickets, weather">⚙ sources</button>
        </div>
      </div>

      <div ref={containerRef as React.RefObject<HTMLDivElement>} className="d-desk-scroll min-h-0 flex-1 overflow-y-auto">
        {!prefs && load !== 'failed' && (
          <div className="flex h-full items-center justify-center p-6 text-body text-muted">Loading your desk…</div>
        )}
        {!prefs && load === 'failed' && (
          <div role="alert" className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
            <p className="text-body text-text">Couldn&rsquo;t load your desk. Nothing was changed.</p>
            <button type="button" className="min-h-11 rounded bg-accent px-4 text-body text-on-accent"
              onClick={() => { setLoad('loading'); setAttempt((n) => n + 1); }}>retry</button>
          </div>
        )}
        {mounted && prefs && (
          <Responsive
            width={width}
            breakpoints={BREAKPOINTS as never}
            cols={COLS as never}
            rowHeight={ROW_HEIGHT}
            margin={MARGIN}
            layouts={prefs.layouts as never}
            dragConfig={{ enabled: arranging, handle: '.d-tile-grip' }}
            resizeConfig={{ enabled: arranging, handles: ['se'] }}
            /* generateLayouts already packs every row flush left to
               right, so the work left is vertical: a short tile beside a
               tall one leaves a hole under it, and the vertical compactor
               pulls whatever sits below up into it. Narrow breakpoints
               can want a tile wider than the grid allows; there, overlap
               is allowed rather than overflow. */
            compactor={{ ...verticalCompactor, allowOverlap: COLS[bp] < 4 }}
            onLayoutChange={onLayoutChange}
            onDragStop={onUserEdit}
            onResizeStop={onUserEdit}
          >
            {prefs.widgets.map((w) => (
              <div key={w.id} className="min-h-0">
                {/* A throw in the widget's own render (above its Tile)
                    shows the tile notice here, not the full-page one. */}
                <ErrorBoundary fallback={(_e, reset) => (
                  <Tile title={w.title || WIDGET_TYPES[w.type]?.title || w.type} status="error" arranging={arranging}
                    onRemove={() => { update((p) => removeWidget(p, w.id)); toast(`${WIDGET_TYPES[w.type]?.title ?? 'Widget'} removed`); }}>
                    <TileCrashed reset={reset} />
                  </Tile>
                )}>
                  {renderWidget(w)}
                </ErrorBoundary>
              </div>
            ))}
          </Responsive>
        )}
        {prefs && prefs.widgets.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-3 p-8">
            <p className="text-body text-muted">An empty desk.</p>
            <button type="button" className="min-h-11 rounded bg-accent px-4 text-body text-on-accent" onClick={() => setDrawer('add')}>Add a widget</button>
          </div>
        )}
      </div>

      {drawer === 'sources' && <DeskSources onClose={() => setDrawer(null)} onSaved={() => setSyncKey((k) => k + 1)} onUnauthorized={onSessionExpired} />}
      {/* Nothing can be added until the desk has loaded: the add would
          be dropped (and must never be saved over an unread desk). */}
      {drawer === 'add' && prefs && (
        <Modal label="Add a widget" onClose={() => setDrawer(null)} className="fixed inset-0 z-40 flex items-end justify-center bg-black/40 sm:items-center">
          <div className="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded-t-lg border border-muted bg-surface p-4 sm:rounded-lg">
            <h2 className="mb-3 text-lead font-semibold text-text">Add a widget</h2>
            <ul className="flex flex-col gap-2">
              {Object.entries(WIDGET_TYPES).map(([type, t]) => {
                const present = !t.multiple && !absent(type);
                return (
                  <li key={type}>
                    <button
                      type="button"
                      disabled={present}
                      onClick={() => {
                        const needsConfig = type === 'custom';
                        const id = newWidgetId(type);
                        const cfg = needsConfig ? { query: '' } : {};
                        update((p) => addWidget(p, type, cfg, undefined, id));
                        setDrawer(needsConfig ? { widget: id } : null);
                        toast(`${t.title} added`);
                      }}
                      className={`w-full rounded-lg border border-muted p-3 text-left ${present ? 'opacity-40' : 'hover:border-accent'}`}
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-body font-semibold text-text">{t.title}</span>
                        {present && <span className="text-tab text-muted">on your desk</span>}
                        {t.needs === 'tavily' && <span className="text-tab text-muted">search</span>}
                        {t.needs === 'ticket' && <span className="text-tab text-muted">tickets</span>}
                      </div>
                      <div className="text-tab text-muted">{t.desc}</div>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        </Modal>
      )}
      {typeof drawer === 'object' && drawer !== null && 'widget' in drawer && (
        <WidgetConfigSheet
          widget={prefs?.widgets.find((w) => w.id === (drawer as { widget: string }).widget)}
          onClose={() => setDrawer(null)}
          onSave={(title, config) => { update((p) => updateWidget(p, (drawer as { widget: string }).widget, { title, config })); setDrawer(null); }}
        />
      )}
    </div>
  );
}

// Per-widget configure: title plus the one configurable field today
// (a custom feed's query).
function WidgetConfigSheet({ widget, onClose, onSave }: { widget?: Widget; onClose: () => void; onSave: (title: string, config: Record<string, unknown>) => void }) {
  const [title, setTitle] = useState(widget?.title ?? '');
  const [query, setQuery] = useState(String(widget?.config?.query ?? ''));
  if (!widget) return null;
  return (
    <Modal label={`configure ${widget.type}`} onClose={onClose} className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4">
      <form className="w-full max-w-sm rounded-lg border border-muted bg-surface p-4" onSubmit={(e) => { e.preventDefault(); onSave(title, { ...(widget.config || {}), query }); }}>
        <h2 className="mb-3 text-lead font-semibold text-text">{WIDGET_TYPES[widget.type]?.title ?? 'Widget'}</h2>
        <label className="mb-2 flex flex-col gap-1 text-body text-text">
          Name
          <input className="min-h-11 rounded bg-base px-3 text-body text-text" value={title} onChange={(e) => setTitle(e.target.value)} placeholder={WIDGET_TYPES[widget.type]?.title} />
        </label>
        {widget.type === 'custom' && (
          <label className="mb-2 flex flex-col gap-1 text-body text-text">
            Search
            <input className="min-h-11 rounded bg-base px-3 text-body text-text" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="rust, local sports, …" />
          </label>
        )}
        <div className="mt-3 flex gap-2">
          <button type="submit" className="min-h-11 rounded bg-accent px-4 text-body text-on-accent">save</button>
          <button type="button" className="min-h-11 rounded px-4 text-body text-text" onClick={onClose}>cancel</button>
        </div>
      </form>
    </Modal>
  );
}
