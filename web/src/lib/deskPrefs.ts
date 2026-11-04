// Desk preferences: the widget list and per-breakpoint layouts the server
// stores as one JSON document (GET/PUT /api/desk/prefs). Pure helpers; the
// page owns fetching and saving.

export const BREAKPOINTS = { lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 } as const;
export const COLS = { lg: 12, md: 8, sm: 6, xs: 2, xxs: 1 } as const;
export const ROW_HEIGHT = 28;
export const MARGIN: [number, number] = [10, 10];

// DESK_VERSION is the document shape this module writes. v3 re-packed
// the desk gap-free with the calendar first and full width, so a v2
// document's saved layouts are rebuilt once on read (normalizePrefs).
// v4 added the showcase tiles (vibe, ship, feed); v5 adds Pulse, which
// joined the default desk after v4 shipped, to existing desks once.
export const DESK_VERSION = 5;

// SHOWCASE_TYPES are tiles only a v4-or-later page can write: older
// pages dropped any type missing from their catalog on read.
const SHOWCASE_TYPES = ['vibe', 'ship', 'feed', 'pulse'];

// storedVersion is the version a document was really written at. Until
// B19 the server stamped every saved desk version 2 whatever the page
// sent, so a "v2" document holding a showcase tile was written by a v4
// page and carries an arrangement the owner made: it reads as v4 and
// keeps its layouts. A v2 document without one is a genuine pre-v4 desk
// (or one indistinguishable from it) and is re-packed as before.
function storedVersion(d: Partial<PrefsDoc>): number {
  const v = typeof d.version === 'number' && Number.isFinite(d.version) ? d.version : 0;
  if (v === 2 && Array.isArray(d.widgets) && d.widgets.some((w) => !!w && SHOWCASE_TYPES.includes((w as Widget).type))) return 4;
  return v;
}

export type WidgetType = {
  title: string; desc: string;
  size: { w: number; h: number }; min: { w: number; h: number };
  needs?: 'tavily' | 'ticket'; multiple?: boolean; configurable?: boolean;
};

// The catalog. Devcules has no FullCalendar suite, so a big calendar
// tile collapses into Meetings (the ICS-backed list) and the email tile
// surfaces as Meetings too; tickets is the Jira/Asana/Linear union.
export const WIDGET_TYPES: Record<string, WidgetType> = {
  vibe:     { title: 'Vibe check',    desc: 'Live headline numbers: terminals, machines, approvals, models.', size: { w: 8, h: 9 }, min: { w: 3, h: 6 } },
  ship:     { title: 'Ship clock',    desc: 'Countdown ring to your demo or deadline.',                     size: { w: 4, h: 8 }, min: { w: 2, h: 4 } },
  pulse:    { title: 'Pulse',         desc: 'The last 24 hours of activity as hourly bars.',                size: { w: 4, h: 7 }, min: { w: 2, h: 4 } },
  feed:     { title: 'Agent feed',    desc: 'Everything agents and people did, as a live timeline.',        size: { w: 4, h: 10 }, min: { w: 2, h: 4 } },
  now:      { title: 'Now',           desc: 'Clock, date and the weather where you are.',                     size: { w: 4, h: 5 }, min: { w: 2, h: 4 } },
  meetings: { title: 'Meetings',      desc: 'Next 7 days from your calendar feeds (Google, Outlook).',      size: { w: 6, h: 7 }, min: { w: 3, h: 4 } },
  // The calendar leads the desk and takes the full width at every
  // breakpoint: a month grid needs the room, and h 14 is the height at
  // which six week rows fit without scrolling.
  calendar: { title: 'Calendar',      desc: 'Your feeds as a month, week or list grid.',                    size: { w: 12, h: 14 }, min: { w: 3, h: 6 } },
  brief:    { title: 'Next up',       desc: 'Pending approvals and the next thing on your calendar.',       size: { w: 4, h: 8 }, min: { w: 3, h: 5 } },
  tickets:  { title: 'Tickets',       desc: 'Issues assigned to you — Jira, Asana or Linear.',              size: { w: 5, h: 7 }, min: { w: 3, h: 4 }, needs: 'ticket' },
  security: { title: 'Hacked today',  desc: 'Breaches and exploited bugs from the last day.',               size: { w: 4, h: 8 }, min: { w: 3, h: 4 }, needs: 'tavily' },
  tech:     { title: 'Tech news',     desc: 'Technology news from the last day.',                           size: { w: 4, h: 8 }, min: { w: 3, h: 4 }, needs: 'tavily' },
  custom:   { title: 'Custom feed',   desc: 'A news search of your own (any topic).',                       size: { w: 4, h: 8 }, min: { w: 3, h: 4 }, needs: 'tavily', multiple: true, configurable: true },
  todos:    { title: 'Todos',         desc: 'A simple list that stays on this desk.',                       size: { w: 3, h: 7 }, min: { w: 2, h: 4 } },
  timer:    { title: 'Timer',         desc: 'Focus timer; chimes when it ends.',                            size: { w: 3, h: 5 }, min: { w: 2, h: 4 } },
  links:    { title: 'Quick links',   desc: 'Bookmarks you keep going back to.',                            size: { w: 3, h: 6 }, min: { w: 2, h: 3 }, multiple: true },
  scratch:  { title: 'Scratchpad',    desc: 'A note that is always there.',                                 size: { w: 4, h: 6 }, min: { w: 2, h: 3 }, multiple: true },
};

export const DEFAULT_WIDGET_ORDER = ['vibe', 'ship', 'calendar', 'feed', 'pulse', 'now', 'meetings', 'brief', 'tickets', 'security', 'tech', 'timer', 'links', 'scratch'];

export type Widget = { id: string; type: string; title?: string; config?: Record<string, unknown> };
export type LayoutItem = { i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number };
export type PrefsDoc = { version: number; widgets: Widget[]; layouts: Record<string, LayoutItem[]>; seen: Record<string, boolean> };

let seq = 0;
export function newWidgetId(type: string): string {
  seq += 1;
  return `w-${type}-${Date.now().toString(36)}${seq.toString(36)}`;
}

// gridWidth is the column span a widget takes at a breakpoint, clamped
// to its minimum and to the grid.
function gridWidth(t: WidgetType, cols: number): number {
  return Math.max(Math.min(t.min.w, cols), Math.min(cols, Math.round((t.size.w * cols) / 12)));
}

// generateLayouts seats widgets in rows that always add up to the grid
// width, so the desk has no holes. A row fills left to right; when the
// next widget no longer fits, the row closes and its last widget takes
// the slack (a 4-wide widget ending a row that has 2 columns spare
// becomes 6 wide). Without that stretch the leftover columns sat empty
// to the right of a short row — the ragged edge the desk used to show.
//
// A widget whose minimum width is the whole grid (the calendar at a
// narrow breakpoint) always sits alone on its own row.
export function generateLayouts(widgets: Widget[]): Record<string, LayoutItem[]> {
  const out: Record<string, LayoutItem[]> = {};
  for (const [bp, cols] of Object.entries(COLS)) {
    const placed: LayoutItem[] = [];
    let row: LayoutItem[] = [];
    let used = 0;
    let y = 0;
    let rowH = 0;

    const closeRow = () => {
      if (row.length === 0) return;
      // The last widget takes the slack, so the row ends flush.
      const last = row[row.length - 1];
      if (used < cols) {
        last.w += cols - used;
        last.minW = last.w;
      }
      // A row reads as one band, and the vertical compactor cannot fill
      // a hole under a short tile while a taller neighbour holds the
      // row open — so every tile in a row grows to the row's tallest.
      // That is what keeps rows flush with no notches beneath them.
      rowH = row.reduce((h, it) => Math.max(h, it.h), 0);
      for (const it of row) {
        it.h = rowH;
        it.minH = Math.min(it.minH ?? rowH, rowH);
      }
      placed.push(...row);
      y += rowH;
      row = [];
      used = 0;
      rowH = 0;
    };

    for (const w of widgets) {
      const t = WIDGET_TYPES[w.type] || { size: { w: 4, h: 6 }, min: { w: 2, h: 3 } };
      const width = gridWidth(t, cols);
      const minW = Math.min(t.min.w, cols);
      // Full-width, or no room left on this row: close it and start a new one.
      if (width >= cols || used + width > cols) closeRow();
      row.push({ i: w.id, x: used, y, w: width, h: t.size.h, minW, minH: t.min.h });
      used += width;
      rowH = Math.max(rowH, t.size.h);
    }
    closeRow();
    out[bp] = placed;
  }
  return out;
}

export function defaultPrefs(): PrefsDoc {
  const widgets = DEFAULT_WIDGET_ORDER.map((type) => ({ id: `w-${type}`, type }));
  return { version: DESK_VERSION, widgets, layouts: generateLayouts(widgets), seen: {} };
}

export function normalizePrefs(doc: unknown): PrefsDoc {
  const d = doc as Partial<PrefsDoc> | null | undefined;
  if (!d || typeof d !== 'object' || !Array.isArray(d.widgets) || d.widgets.length === 0) return defaultPrefs();
  const widgets = d.widgets
    .filter((w): w is Widget => !!w && typeof (w as Widget).id === 'string' && !!WIDGET_TYPES[(w as Widget).type])
    .map((w) => ({ id: w.id, type: w.type, title: w.title, config: (w.config as Record<string, unknown>) || {} }));
  if (widgets.length === 0) return defaultPrefs();
  const version = storedVersion(d);
  // Version 4 adds the showcase tiles: an older desk gets them once, up top.
  if (version < 4) {
    for (const type of ['feed', 'ship', 'vibe']) {
      if (!widgets.some((w) => w.type === type)) widgets.unshift({ id: `w-${type}`, type, title: undefined, config: {} });
    }
  }
  // Version 5 adds Pulse once, after the agent feed as on a new desk.
  // A desk that kept its layouts gets it at the bottom (the missing-cell
  // path below); a removal saved at v5 is never undone.
  if (version < 5 && !widgets.some((w) => w.type === 'pulse')) {
    const at = widgets.findIndex((w) => w.type === 'feed');
    const id = widgets.some((w) => w.id === 'w-pulse') ? newWidgetId('pulse') : 'w-pulse';
    widgets.splice(at < 0 ? widgets.length : at + 1, 0, { id, type: 'pulse', title: undefined, config: {} });
  }
  // Pre-v4 desks predate the gap-free packing, the full-width calendar
  // and the showcase row. Their saved layouts are re-packed once here, so
  // an existing desk picks up the new shape instead of keeping its ragged
  // rows; v4 and later keep whatever the owner has since arranged.
  const legacy = version < 4;
  const layouts = { ...generateLayouts(widgets) };
  if (!legacy && d.layouts && typeof d.layouts === 'object') {
    for (const bp of Object.keys(COLS)) {
      const saved = Array.isArray((d.layouts as Record<string, LayoutItem[]>)[bp])
        ? (d.layouts as Record<string, LayoutItem[]>)[bp].filter((l) => widgets.some((w) => w.id === l.i))
        : [];
      if (saved.length) {
        const missing = layouts[bp].filter((l) => !saved.some((s) => s.i === l.i)).map((l) => ({ ...l, y: Infinity }));
        layouts[bp] = [
          ...saved.map((s) => {
            const w = widgets.find((x) => x.id === s.i)!;
            const t = WIDGET_TYPES[w.type];
            // A widget added and saved before the grid placed it has
            // y: Infinity, which JSON writes as null: send it to the bottom.
            return { ...s, y: Number.isFinite(s.y) ? s.y : Infinity, minW: Math.min(t.min.w, (COLS as Record<string, number>)[bp]), minH: t.min.h };
          }),
          ...missing,
        ];
      }
    }
  }
  return { version: DESK_VERSION, widgets, layouts, seen: { ...(d.seen && typeof d.seen === "object" ? d.seen : {}) } };
}

// addWidget appends a widget of `type`. Pass `id` when the caller needs
// to refer to the new widget afterwards (the custom feed's configure sheet).
export function addWidget(prefs: PrefsDoc, type: string, config: Record<string, unknown> = {}, title?: string, id?: string): PrefsDoc {
  const t = WIDGET_TYPES[type];
  if (!t) return prefs;
  if (!t.multiple && prefs.widgets.some((w) => w.type === type)) return prefs;
  if (id && prefs.widgets.some((w) => w.id === id)) return prefs;
  const widget: Widget = { id: id || newWidgetId(type), type, config, ...(title ? { title } : {}) };
  const widgets = [...prefs.widgets, widget];
  const layouts: Record<string, LayoutItem[]> = {};
  for (const [bp, cols] of Object.entries(COLS)) {
    const width = Math.max(Math.min(t.min.w, cols), Math.min(cols, Math.round((t.size.w * cols) / 12)));
    layouts[bp] = [...(prefs.layouts[bp] || []), { i: widget.id, x: 0, y: Infinity, w: width, h: t.size.h, minW: Math.min(t.min.w, cols), minH: t.min.h }];
  }
  return { ...prefs, widgets, layouts };
}

export function removeWidget(prefs: PrefsDoc, id: string): PrefsDoc {
  const layouts: Record<string, LayoutItem[]> = {};
  for (const bp of Object.keys(COLS)) layouts[bp] = (prefs.layouts[bp] || []).filter((l) => l.i !== id);
  return { ...prefs, widgets: prefs.widgets.filter((w) => w.id !== id), layouts };
}

export function updateWidget(prefs: PrefsDoc, id: string, patch: Partial<Widget>): PrefsDoc {
  return { ...prefs, widgets: prefs.widgets.map((w) => (w.id === id ? { ...w, ...patch, config: { ...(w.config || {}), ...(patch.config || {}) } } : w)) };
}

export function setLayouts(prefs: PrefsDoc, layouts: Record<string, LayoutItem[]>): PrefsDoc {
  const clean: Record<string, LayoutItem[]> = {};
  for (const bp of Object.keys(COLS)) {
    clean[bp] = (layouts[bp] || prefs.layouts[bp] || []).map((l) => ({ i: l.i, x: l.x, y: l.y, w: l.w, h: l.h }));
  }
  return { ...prefs, layouts: clean };
}

// sameLayout is geometry-only equality (i/x/y/w/h) keyed by widget id: it
// ignores minW/minH and the order the grid lists items in, so a
// round-trip through the grid counts as "unchanged".
type Geo = { i: string; x: number; y: number; w: number; h: number };
export function sameLayout(a: readonly Geo[] | undefined, b: readonly Geo[] | undefined): boolean {
  if (a === b) return true;
  if (!a || !b || a.length !== b.length) return false;
  const byId = new Map(b.map((m) => [m.i, m]));
  if (byId.size !== b.length) return false;
  return a.every((l) => { const m = byId.get(l.i); return !!m && l.x === m.x && l.y === m.y && l.w === m.w && l.h === m.h; });
}

export function toDocument(prefs: PrefsDoc): { version: number; widgets: Widget[]; layouts: Record<string, LayoutItem[]>; seen: Record<string, boolean> } {
  return { version: DESK_VERSION, widgets: prefs.widgets, layouts: setLayouts(prefs, prefs.layouts).layouts, seen: prefs.seen || {} };
}
