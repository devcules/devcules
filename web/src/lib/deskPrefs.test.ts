import { describe, expect, it } from 'vitest';
import {
  addWidget, COLS, defaultPrefs, DESK_VERSION, generateLayouts, newWidgetId, normalizePrefs,
  removeWidget, setLayouts, toDocument, updateWidget,
} from './deskPrefs';

describe('deskPrefs', () => {
  it('default desk has the starter widgets and a layout per breakpoint', () => {
    const p = defaultPrefs();
    expect(p.version).toBe(DESK_VERSION);
    expect(p.widgets.length).toBeGreaterThan(4);
    for (const bp of Object.keys(COLS)) {
      expect(Array.isArray(p.layouts[bp])).toBe(true);
      expect(p.layouts[bp].length).toBe(p.widgets.length);
    }
  });

  it('normalizePrefs on empty input returns the default starter set', () => {
    expect(normalizePrefs(null).widgets.length).toBeGreaterThan(0);
    expect(normalizePrefs({}).widgets.length).toBeGreaterThan(0);
    expect(normalizePrefs({ widgets: [] }).widgets.length).toBeGreaterThan(0);
  });

  it('normalizePrefs drops unknown widget types but keeps known ones', () => {
    const p = normalizePrefs({ version: 2, widgets: [{ id: 'a', type: 'now' }, { id: 'b', type: 'nonsense' }], layouts: {}, seen: {} });
    expect(p.widgets.map((w) => w.type)).toEqual(['now']);
  });

  it('keeps a current-version saved layout and backfills the missing breakpoints', () => {
    const doc = normalizePrefs({ version: DESK_VERSION, widgets: [{ id: 'a', type: 'now' }, { id: 'b', type: 'timer' }], layouts: { lg: [{ i: 'a', x: 4, y: 0, w: 4, h: 5 }] }, seen: {} });
    expect(doc.layouts.lg.length).toBe(2);
    // The saved cell survives as placed.
    expect(doc.layouts.lg.find((l) => l.i === 'a')).toMatchObject({ x: 4, y: 0, w: 4 });
    expect(doc.layouts.xxs.some((l) => l.i === 'b')).toBe(true);
  });

  it('re-packs a version-2 desk once, so an existing desk gets the new shape', () => {
    const stored = {
      version: 2,
      widgets: [{ id: 'a', type: 'now' }, { id: 'b', type: 'calendar' }, { id: 'c', type: 'timer' }],
      // A ragged old row: 4 + 6 wide on a 12-column grid left two empty.
      layouts: { lg: [{ i: 'a', x: 0, y: 0, w: 4, h: 5 }, { i: 'b', x: 4, y: 5, w: 6, h: 9 }, { i: 'c', x: 0, y: 14, w: 3, h: 5 }] },
      seen: {},
    };
    const doc = normalizePrefs(stored);
    expect(doc.version).toBe(DESK_VERSION);
    // The saved ragged rows are gone; the document is re-packed, and the
    // calendar takes the whole width (the saved order, not the default
    // one, decides whether it leads).
    expect(doc.layouts.lg.find((l) => l.i === 'b')).toMatchObject({ x: 0, w: COLS.lg });
    expect(doc.layouts.lg.find((l) => l.i === 'a')).toMatchObject({ x: 0, y: 0 });
    // And the re-pack is written back, so the next read is stable.
    expect(toDocument(doc).version).toBe(DESK_VERSION);
  });

  it('addWidget refuses a second non-multiple widget but allows multiples', () => {
    let p = defaultPrefs();
    const before = p.widgets.filter((w) => w.type === 'now').length;
    p = addWidget(p, 'now');
    expect(p.widgets.filter((w) => w.type === 'now').length).toBe(before); // refused
    p = addWidget(p, 'custom', { query: 'rust' });
    p = addWidget(p, 'custom', { query: 'go' });
    expect(p.widgets.filter((w) => w.type === 'custom').length).toBe(2);
  });

  it('addWidget seats the new widget at the bottom of every breakpoint', () => {
    let p = defaultPrefs();
    p = addWidget(p, 'scratch');
    for (const bp of Object.keys(COLS)) {
      const last = p.layouts[bp][p.layouts[bp].length - 1];
      expect(last.y).toBe(Infinity);
    }
  });

  it('removeWidget strips the widget and its layout cells', () => {
    let p = defaultPrefs();
    const id = p.widgets[0].id;
    p = removeWidget(p, id);
    expect(p.widgets.some((w) => w.id === id)).toBe(false);
    for (const bp of Object.keys(COLS)) {
      expect(p.layouts[bp].some((l) => l.i === id)).toBe(false);
    }
  });

  it('updateWidget merges config without dropping existing keys', () => {
    let p = defaultPrefs();
    p = addWidget(p, 'custom', { query: 'rust', extra: 1 }, 'mine');
    const w = p.widgets.find((x) => x.type === 'custom')!;
    p = updateWidget(p, w.id, { title: 'renamed', config: { query: 'go' } });
    const after = p.widgets.find((x) => x.id === w.id)!;
    expect(after.title).toBe('renamed');
    expect(after.config).toMatchObject({ query: 'go', extra: 1 });
  });

  it('setLayouts strips a layout down to the persisted five fields', () => {
    const p = defaultPrefs();
    const withNoise = { lg: [{ i: 'a', x: 1, y: 2, w: 3, h: 4, moved: true, static: false }] };
    const out = setLayouts(p, withNoise as never);
    expect(out.layouts.lg[0]).toEqual({ i: 'a', x: 1, y: 2, w: 3, h: 4 });
  });

  it('toDocument is a clean round-trip through normalizePrefs', () => {
    const p = defaultPrefs();
    const doc = toDocument(p);
    expect(doc.version).toBe(DESK_VERSION);
    const back = normalizePrefs(doc);
    expect(back.widgets.map((w) => w.id)).toEqual(p.widgets.map((w) => w.id));
  });

  it('generateLayouts stays inside the column budget at every breakpoint', () => {
    const layouts = generateLayouts(defaultPrefs().widgets);
    for (const [bp, cols] of Object.entries(COLS)) {
      for (const cell of layouts[bp]) {
        expect(cell.x).toBeGreaterThanOrEqual(0);
        expect(cell.w).toBeGreaterThan(0);
        expect(cell.w).toBeLessThanOrEqual(cols);
      }
    }
  });

  it('generateLayouts leaves no gap: every row spans the full grid', () => {
    const widgets = defaultPrefs().widgets;
    const layouts = generateLayouts(widgets);
    for (const [bp, cols] of Object.entries(COLS)) {
      // Group the cells by their y (a row), then add up the widths.
      const rows = new Map<number, number>();
      for (const cell of layouts[bp]) rows.set(cell.y, (rows.get(cell.y) ?? 0) + cell.w);
      for (const [y, total] of rows) {
        expect(`${bp} y=${y}: ${total}/${cols}`).toBe(`${bp} y=${y}: ${cols}/${cols}`);
      }
      // No two widgets may share a slot.
      const slots = new Set<string>();
      for (const cell of layouts[bp]) {
        for (let x = cell.x; x < cell.x + cell.w; x++) {
          const key = `${x}/${cell.y}`;
          expect(slots.has(key)).toBe(false);
          slots.add(key);
        }
      }
    }
  });

  it('every tile in a row is as tall as the row, so nothing notches underneath', () => {
    const widgets = defaultPrefs().widgets;
    const layouts = generateLayouts(widgets);
    for (const [bp, cols] of Object.entries(COLS)) {
      const rows = new Map<number, number[]>();
      for (const cell of layouts[bp]) rows.set(cell.y, [...(rows.get(cell.y) ?? []), cell.h]);
      for (const [y, heights] of rows) {
        const tallest = Math.max(...heights);
        for (const h of heights) expect(`${bp} y=${y} h${h}`).toBe(`${bp} y=${y} h${tallest}`);
      }
      // A row starts where the one above ended: no vertical band gap.
      const starts = [...rows.keys()].sort((a, b) => a - b);
      let shelfEnd = 0;
      for (const y of starts) {
        expect(y).toBe(shelfEnd);
        shelfEnd = y + Math.max(...(rows.get(y) ?? [0]));
      }
      expect(cols).toBeGreaterThan(0);
    }
  });

  it('the calendar leads the default desk at full width, tall enough for a month', () => {
    const p = defaultPrefs();
    expect(p.widgets[0].type).toBe('calendar');
    const lg = p.layouts.lg.find((l) => l.i === p.widgets[0].id);
    expect(lg?.x).toBe(0);
    expect(lg?.y).toBe(0);
    expect(lg?.w).toBe(COLS.lg);
    // Six week rows plus the toolbar need the height.
    expect(lg!.h).toBeGreaterThanOrEqual(14);
  });

  it('newWidgetId is unique per call', () => {
    expect(newWidgetId('scratch')).not.toBe(newWidgetId('scratch'));
  });
});
