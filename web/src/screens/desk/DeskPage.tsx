// Desk — the instrument panel: a glance strip up top (clock, next meeting,
// weather) and a movable grid of widget tiles below. Arrange mode turns on
// drag grips and resize corners; layouts persist per breakpoint to
// /api/desk/prefs and are merged with any newer copy another device wrote.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Responsive, useContainerWidth, verticalCompactor, type Layout, type ResponsiveLayouts } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import {
  addWidget, BREAKPOINTS, COLS, MARGIN, normalizePrefs, newWidgetId,
  removeWidget, ROW_HEIGHT, setLayouts, toDocument, updateWidget, WIDGET_TYPES, type PrefsDoc, type Widget,
} from '../../lib/deskPrefs';
import { getPrefs, putPrefs } from '../../lib/deskApi';
import Tile from './Tile';
import { MeetingsTile, NewsTile, NowTile, TicketsTile } from './tiles';
import CalendarTile from './CalendarTile';
import { LinksTile, NextUpTile, ScratchTile, TimerTile, TodosTile } from './localTiles';
import DeskSources from './DeskSources';
import { toast, type ToastEvent } from '../../lib/toast';

function Clock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(id);
  }, []);
  return (
    <div className="flex items-baseline gap-3">
      <span className="text-3xl font-semibold tabular-nums text-text">{now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
      <span className="text-body text-muted">{now.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' })}</span>
    </div>
  );
}

export default function DeskPage({ onSessionExpired, onOpenApprovals }: { onSessionExpired: () => void; onOpenApprovals?: () => void }) {
  const [prefs, setPrefs] = useState<PrefsDoc | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [arranging, setArranging] = useState(false);
  const [syncKey, setSyncKey] = useState(0);
  const [drawer, setDrawer] = useState<null | 'add' | 'sources' | { widget: string }>(null);
  const [next, setNext] = useState<{ title: string; start: string } | null>(null);
  const [weather, setWeather] = useState<{ temp: number; text: string; location: string } | null>(null);
  const saveTimer = useRef<number | null>(null);

  const { width, containerRef, mounted } = useContainerWidth();

  // Boot: read the server document and normalize. On a fresh desk the
  // server has nothing, so normalizePrefs answers the default starter set.
  useEffect(() => {
    void getPrefs().then((r) => {
      if (r === 'unauthorized') { onSessionExpired(); return; }
      setPrefs(r === 'unreachable' ? normalizePrefs(null) : normalizePrefs(r));
      setLoaded(true);
    });
  }, [onSessionExpired]);

  // Debounced persist: every prefs change saves 400ms after the last one,
  // so a drag across the grid writes the final layout once.
  const persist = useCallback((doc: PrefsDoc) => {
    if (saveTimer.current !== null) window.clearTimeout(saveTimer.current);
    saveTimer.current = window.setTimeout(() => {
      void putPrefs(toDocument(doc) as never).then((r) => {
        if (r === 'unauthorized') onSessionExpired();
      });
    }, 400);
  }, [onSessionExpired]);
  useEffect(() => () => { if (saveTimer.current !== null) window.clearTimeout(saveTimer.current); }, []);

  const update = useCallback((fn: (p: PrefsDoc) => PrefsDoc) => {
    setPrefs((prev) => {
      if (!prev) return prev;
      const next = fn(prev);
      persist(next);
      return next;
    });
  }, [persist]);

  const onLayoutChange = useCallback((_: Layout, all: ResponsiveLayouts<string>) => {
    update((p) => setLayouts(p, all as never));
  }, [update]);

  const grids = useMemo(() => {
    if (!prefs) return null;
    return WIDGET_TYPES && prefs;
  }, [prefs]);

  if (!prefs || !grids) {
    return <div className="flex flex-1 items-center justify-center p-6 text-body text-muted">{loaded ? 'Waking your desk…' : 'Loading your desk…'}</div>;
  }

  const renderWidget = (w: Widget) => {
    const common = {
      widget: w, syncKey, arranging,
      onRemove: () => { update((p) => removeWidget(p, w.id)); toast(`${WIDGET_TYPES[w.type]?.title ?? 'Widget'} removed`); },
      onSettings: () => setDrawer(WIDGET_TYPES[w.type]?.configurable ? { widget: w.id } : 'sources'),
      onConfig: (cfg: Record<string, unknown>) => update((p) => updateWidget(p, w.id, { config: cfg })),
      onUnauthorized: onSessionExpired,
    };
    switch (w.type) {
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

  const absent = (type: string) => !prefs.widgets.some((w) => w.type === type);

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      {/* Console strip: the glance. Clock + date, next meeting, weather,
          then sync / arrange / add / sources. */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-muted bg-surface px-4 py-3">
        <Clock />
        {next && (
          <div className="text-body text-muted">
            next: <span className="text-text">{next.title}</span> · {new Date(next.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </div>
        )}
        {weather && (
          <div className="text-body text-muted">
            {Math.round(weather.temp)}° <span className="text-text">{weather.text}</span>{weather.location ? ` · ${weather.location}` : ''}
          </div>
        )}
        <div className="ml-auto flex items-center gap-1">
          <button type="button" className="min-h-9 rounded px-3 text-body text-text hover:bg-base" onClick={() => setSyncKey((k) => k + 1)} title="refetch every widget">⟳ sync</button>
          <button type="button" aria-pressed={arranging} className={`min-h-9 rounded px-3 text-body ${arranging ? 'bg-accent text-on-accent' : 'text-text hover:bg-base'}`} onClick={() => setArranging((a) => !a)} title="drag and resize tiles">
            {arranging ? 'done' : 'arrange'}
          </button>
          <button type="button" className="min-h-9 rounded px-3 text-body text-text hover:bg-base" onClick={() => setDrawer('add')} title="add a widget">＋ add</button>
          <button type="button" className="min-h-9 rounded px-3 text-body text-text hover:bg-base" onClick={() => setDrawer('sources')} title="calendar, news, tickets, weather">⚙ sources</button>
        </div>
      </div>

      <div ref={containerRef as React.RefObject<HTMLDivElement>} className="min-h-0 flex-1 overflow-y-auto">
        {mounted && (
          <Responsive
            width={width}
            breakpoints={BREAKPOINTS as never}
            cols={COLS as never}
            rowHeight={ROW_HEIGHT}
            margin={MARGIN}
            layouts={prefs.layouts as never}
            dragConfig={{ enabled: arranging, handle: '.d-tile-grip' }}
            resizeConfig={{ enabled: arranging, handles: ['se'] }}
            compactor={verticalCompactor}
            onLayoutChange={onLayoutChange}
          >
            {prefs.widgets.map((w) => (
              <div key={w.id} className="min-h-0">
                {renderWidget(w)}
              </div>
            ))}
          </Responsive>
        )}
        {prefs.widgets.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-3 p-8">
            <p className="text-body text-muted">An empty desk.</p>
            <button type="button" className="min-h-11 rounded bg-accent px-4 text-body text-on-accent" onClick={() => setDrawer('add')}>Add a widget</button>
          </div>
        )}
      </div>

      {drawer === 'sources' && <DeskSources onClose={() => setDrawer(null)} onSaved={() => setSyncKey((k) => k + 1)} onUnauthorized={onSessionExpired} />}
      {drawer === 'add' && (
        <div className="fixed inset-0 z-40 flex items-end justify-center bg-black/40 sm:items-center" role="dialog" aria-label="Add a widget" onClick={() => setDrawer(null)}>
          <div className="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded-t-lg border border-muted bg-surface p-4 sm:rounded-lg" onClick={(e) => e.stopPropagation()}>
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
                        update((p) => addWidget({ ...p }, type, cfg, undefined));
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
        </div>
      )}
      {typeof drawer === 'object' && drawer !== null && 'widget' in drawer && (
        <WidgetConfigSheet
          widget={prefs.widgets.find((w) => w.id === (drawer as { widget: string }).widget)}
          onClose={() => setDrawer(null)}
          onSave={(title, config) => { update((p) => updateWidget(p, (drawer as { widget: string }).widget, { title, config })); setDrawer(null); }}
        />
      )}
      <ToastHost />
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
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-label={`configure ${widget.type}`} onClick={onClose}>
      <form className="w-full max-w-sm rounded-lg border border-muted bg-surface p-4" onClick={(e) => e.stopPropagation()} onSubmit={(e) => { e.preventDefault(); onSave(title, { ...(widget.config || {}), query }); }}>
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
    </div>
  );
}

// ToastHost listens for copy/save toasts anywhere on the desk and shows a
// small pill bottom-center; it fades itself.
function ToastHost() {
  const [items, setItems] = useState<Array<{ id: number; message: string; tone: string }>>([]);
  useEffect(() => {
    let id = 0;
    const on = (e: Event) => {
      const d = (e as CustomEvent<ToastEvent>).detail;
      id += 1;
      const mine = id;
      setItems((p) => [...p, { id: mine, message: d.message, tone: d.tone }]);
      window.setTimeout(() => setItems((p) => p.filter((t) => t.id !== mine)), 2200);
    };
    window.addEventListener('devcules:toast', on);
    return () => window.removeEventListener('devcules:toast', on);
  }, []);
  if (items.length === 0) return null;
  return (
    <div aria-live="polite" className="pointer-events-none fixed inset-x-0 bottom-16 z-50 flex flex-col items-center gap-1">
      {items.map((t) => (
        <div key={t.id} className={`rounded-full px-4 py-1.5 text-body ${t.tone === 'bad' ? 'bg-red-500 text-white' : 'bg-text text-base'}`}>{t.message}</div>
      ))}
    </div>
  );
}
