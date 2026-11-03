// Desk preferences: the widget list and per-breakpoint layouts the server
// stores as one JSON document (GET/PUT /api/desk/prefs). Pure helpers; the
// page owns fetching and saving.

export const BREAKPOINTS = { lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 } as const;
export const COLS = { lg: 12, md: 8, sm: 6, xs: 2, xxs: 1 } as const;
export const ROW_HEIGHT = 28;
export const MARGIN: [number, number] = [10, 10];

export type WidgetType = {
  title: string; desc: string;
  size: { w: number; h: number }; min: { w: number; h: number };
  needs?: 'tavily' | 'ticket'; multiple?: boolean; configurable?: boolean;
};

// The catalog. Devcules has no FullCalendar suite, so a big calendar
// tile collapses into Meetings (the ICS-backed list) and the email tile
// surfaces as Meetings too; tickets is the Jira/Asana/Linear union.
export const WIDGET_TYPES: Record<string, WidgetType> = {
  now:      { title: 'Now',           desc: 'Clock, date and the weather where you are.',                     size: { w: 4, h: 5 }, min: { w: 2, h: 4 } },
  meetings: { title: 'Meetings',      desc: 'Next 7 days from your calendar feeds (Google, Outlook).',      size: { w: 6, h: 7 }, min: { w: 3, h: 4 } },
  calendar: { title: 'Calendar',      desc: 'Your feeds as a month, week or list grid.',                    size: { w: 6, h: 9 }, min: { w: 3, h: 6 } },
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

export const DEFAULT_WIDGET_ORDER = ['now', 'meetings', 'calendar', 'brief', 'tickets', 'security', 'tech', 'timer', 'links', 'scratch'];

export type Widget = { id: string; type: string; title?: string; config?: Record<string, unknown> };
export type LayoutItem = { i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number };
export type PrefsDoc = { version: number; widgets: Widget[]; layouts: Record<string, LayoutItem[]>; seen: Record<string, boolean> };

let seq = 0;
export function newWidgetId(type: string): string {
  seq += 1;
  return `w-${type}-${Date.now().toString(36)}${seq.toString(36)}`;
}

export function generateLayouts(widgets: Widget[]): Record<string, LayoutItem[]> {
  const out: Record<string, LayoutItem[]> = {};
  for (const [bp, cols] of Object.entries(COLS)) {
    let x = 0, y = 0, rowH = 0;
    out[bp] = widgets.map((w) => {
      const t = WIDGET_TYPES[w.type] || { size: { w: 4, h: 6 }, min: { w: 2, h: 3 } };
      const width = Math.max(Math.min(t.min.w, cols), Math.min(cols, Math.round((t.size.w * cols) / 12)));
      const h = t.size.h;
      if (x + width > cols) { x = 0; y += rowH; rowH = 0; }
      const item: LayoutItem = { i: w.id, x, y, w: width, h, minW: Math.min(t.min.w, cols), minH: t.min.h };
      x += width;
      rowH = Math.max(rowH, h);
      return item;
    });
  }
  return out;
}

export function defaultPrefs(): PrefsDoc {
  const widgets = DEFAULT_WIDGET_ORDER.map((type) => ({ id: `w-${type}`, type }));
  return { version: 2, widgets, layouts: generateLayouts(widgets), seen: {} };
}

export function normalizePrefs(doc: unknown): PrefsDoc {
  const d = doc as Partial<PrefsDoc> | null | undefined;
  if (!d || typeof d !== 'object' || !Array.isArray(d.widgets) || d.widgets.length === 0) return defaultPrefs();
  const widgets = d.widgets
    .filter((w): w is Widget => !!w && typeof (w as Widget).id === 'string' && !!WIDGET_TYPES[(w as Widget).type])
    .map((w) => ({ id: w.id, type: w.type, title: w.title, config: (w.config as Record<string, unknown>) || {} }));
  if (widgets.length === 0) return defaultPrefs();
  const layouts = { ...generateLayouts(widgets) };
  if (d.layouts && typeof d.layouts === 'object') {
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
            return { ...s, minW: Math.min(t.min.w, (COLS as Record<string, number>)[bp]), minH: t.min.h };
          }),
          ...missing,
        ];
      }
    }
  }
  return { version: 2, widgets, layouts, seen: { ...(d.seen && typeof d.seen === 'object' ? d.seen : {}) } };
}

export function addWidget(prefs: PrefsDoc, type: string, config: Record<string, unknown> = {}, title?: string): PrefsDoc {
  const t = WIDGET_TYPES[type];
  if (!t) return prefs;
  if (!t.multiple && prefs.widgets.some((w) => w.type === type)) return prefs;
  const widget: Widget = { id: newWidgetId(type), type, config, ...(title ? { title } : {}) };
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

export function toDocument(prefs: PrefsDoc): { version: number; widgets: Widget[]; layouts: Record<string, LayoutItem[]>; seen: Record<string, boolean> } {
  return { version: 2, widgets: prefs.widgets, layouts: setLayouts(prefs, prefs.layouts).layouts, seen: prefs.seen || {} };
}
