import { describe, expect, it } from 'vitest';
import {
  addWidget, COLS, defaultPrefs, DESK_VERSION, generateLayouts, newWidgetId, normalizePrefs,
  removeWidget, sameLayout, setLayouts, toDocument, updateWidget,
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
    // A pre-v4 desk also gains the showcase tiles up top (Pulse after the feed, v5).
    expect(p.widgets.map((w) => w.type)).toEqual(['vibe', 'ship', 'feed', 'pulse', 'now']);
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
    expect(doc.widgets[0].type).toBe('vibe');
    expect(doc.layouts.lg.find((l) => l.i === 'w-vibe')).toMatchObject({ x: 0, y: 0 });
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

  it('the showcase leads, then the calendar at full width, tall enough for a month', () => {
    const p = defaultPrefs();
    expect(p.widgets[0].type).toBe('vibe');
    const cal = p.widgets.find((w) => w.type === 'calendar')!;
    const lg = p.layouts.lg.find((l) => l.i === cal.id);
    expect(lg?.x).toBe(0);
    expect(lg?.w).toBe(COLS.lg);
    // Six week rows plus the toolbar need the height.
    expect(lg!.h).toBeGreaterThanOrEqual(14);
  });

  it('newWidgetId is unique per call', () => {
    expect(newWidgetId('scratch')).not.toBe(newWidgetId('scratch'));
  });

  // DESK-07: the caller can name the new widget, so it can open its sheet.
  it('addWidget uses the id it is given', () => {
    const p = addWidget(defaultPrefs(), 'custom', { query: '' }, undefined, 'w-custom-x');
    expect(p.widgets.at(-1)).toMatchObject({ id: 'w-custom-x', type: 'custom' });
    expect(p.layouts.lg.some((l) => l.i === 'w-custom-x')).toBe(true);
    // The same id twice is refused rather than duplicated.
    expect(addWidget(p, 'custom', {}, undefined, 'w-custom-x')).toBe(p);
  });

  // DESK-05: the grid lists the same layout in another order on a
  // breakpoint echo; that is not a change.
  it('sameLayout compares by widget id, not by array index', () => {
    const a = [{ i: 'a', x: 0, y: 0, w: 4, h: 5 }, { i: 'b', x: 4, y: 0, w: 4, h: 5 }];
    const b = [{ i: 'b', x: 4, y: 0, w: 4, h: 5, minW: 2 }, { i: 'a', x: 0, y: 0, w: 4, h: 5 }];
    expect(sameLayout(a, b)).toBe(true);
    expect(sameLayout(a, [{ ...b[0], x: 5 }, b[1]])).toBe(false);
    expect(sameLayout(a, [b[0], { ...b[0] }])).toBe(false);
    expect(sameLayout(a, undefined)).toBe(false);
  });

  it('normalizePrefs sends a saved null y (an unplaced Infinity) to the bottom', () => {
    const doc = normalizePrefs({ version: DESK_VERSION, widgets: [{ id: 'a', type: 'now' }], layouts: { lg: [{ i: 'a', x: 0, y: null, w: 4, h: 5 }] }, seen: {} });
    expect(doc.layouts.lg[0].y).toBe(Infinity);
  });

  // B19: the server stamped every saved desk version 2, so a desk a v4
  // page arranged came back "v2" and was re-packed on every reload.
  describe('stored version (B19)', () => {
    const lg = (doc: ReturnType<typeof normalizePrefs>, id: string) => doc.layouts.lg.find((l) => l.i === id);
    const arranged = (version: number, extra: Array<{ id: string; type: string }> = []) => ({
      version,
      widgets: [{ id: 'w-vibe', type: 'vibe' }, { id: 'w-ship', type: 'ship' }, { id: 'w-feed', type: 'feed' }, { id: 'w-now', type: 'now' }, ...extra],
      // The owner dragged the vibe tile to the bottom.
      layouts: { lg: [{ i: 'w-vibe', x: 0, y: 40, w: 8, h: 9 }, { i: 'w-ship', x: 0, y: 0, w: 4, h: 8 }, { i: 'w-feed', x: 4, y: 0, w: 4, h: 10 }, { i: 'w-now', x: 8, y: 0, w: 4, h: 5 }, ...extra.map((e, n) => ({ i: e.id, x: 0, y: 20 + n, w: 4, h: 7 }))] },
      seen: {},
    });

    it('a v2-stamped desk a v4 page wrote keeps its arrangement', () => {
      const doc = normalizePrefs(arranged(2, [{ id: 'w-pulse', type: 'pulse' }]));
      expect(lg(doc, 'w-vibe')).toMatchObject({ x: 0, y: 40, w: 8 });
      expect(lg(doc, 'w-now')).toMatchObject({ x: 8, y: 0 });
      expect(doc.widgets.filter((w) => w.type === 'pulse')).toHaveLength(1);
    });

    it('a v2 desk with no showcase tile is still a legacy desk and is re-packed', () => {
      const doc = normalizePrefs({ version: 2, widgets: [{ id: 'a', type: 'now' }], layouts: { lg: [{ i: 'a', x: 5, y: 30, w: 4, h: 5 }] }, seen: {} });
      expect(doc.widgets[0].type).toBe('vibe');
      expect(lg(doc, 'a')).not.toMatchObject({ x: 5, y: 30 });
    });

    it('the document the page writes reads back unchanged', () => {
      const once = normalizePrefs(arranged(DESK_VERSION, [{ id: 'w-pulse', type: 'pulse' }]));
      const twice = normalizePrefs(JSON.parse(JSON.stringify(toDocument(once))));
      expect(sameLayout(twice.layouts.lg, once.layouts.lg)).toBe(true);
      expect(lg(twice, 'w-vibe')).toMatchObject({ y: 40 });
      expect(twice.widgets.map((w) => w.id)).toEqual(once.widgets.map((w) => w.id));
    });

    it('a v4 desk gains Pulse once, after the feed, at the bottom, keeping its layout', () => {
      const doc = normalizePrefs(arranged(4));
      expect(doc.widgets.map((w) => w.type)).toEqual(['vibe', 'ship', 'feed', 'pulse', 'now']);
      expect(lg(doc, 'w-vibe')).toMatchObject({ y: 40 });
      const pulse = lg(doc, 'w-pulse')!;
      expect(pulse.y).toBe(Infinity);
      for (const bp of Object.keys(COLS)) expect(doc.layouts[bp].filter((l) => l.i === 'w-pulse')).toHaveLength(1);
      // Normalising the saved result again adds nothing.
      const again = normalizePrefs(JSON.parse(JSON.stringify(toDocument(doc))));
      expect(again.widgets.filter((w) => w.type === 'pulse')).toHaveLength(1);
    });

    it('never duplicates an existing Pulse, and a v5 desk that removed it stays without', () => {
      const kept = normalizePrefs(arranged(4, [{ id: 'w-pulse-x', type: 'pulse' }]));
      expect(kept.widgets.filter((w) => w.type === 'pulse').map((w) => w.id)).toEqual(['w-pulse-x']);
      const removed = normalizePrefs(arranged(DESK_VERSION));
      expect(removed.widgets.some((w) => w.type === 'pulse')).toBe(false);
    });

    it('a stray w-pulse id of another type does not collide with the added Pulse', () => {
      const doc = normalizePrefs({ ...arranged(4), widgets: [...arranged(4).widgets, { id: 'w-pulse', type: 'timer' }] });
      expect(new Set(doc.widgets.map((w) => w.id)).size).toBe(doc.widgets.length);
      expect(doc.widgets.filter((w) => w.type === 'pulse')).toHaveLength(1);
    });
  });
});
