import { describe, expect, it } from 'vitest';
import {
  addWidget, COLS, defaultPrefs, generateLayouts, newWidgetId, normalizePrefs,
  removeWidget, setLayouts, toDocument, updateWidget,
} from './deskPrefs';

describe('deskPrefs', () => {
  it('default desk has the starter widgets and a layout per breakpoint', () => {
    const p = defaultPrefs();
    expect(p.version).toBe(2);
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

  it('normalizePrefs keeps a saved single-breakpoint layout and backfills the missing ones', () => {
    const doc = normalizePrefs({ version: 2, widgets: [{ id: 'a', type: 'now' }, { id: 'b', type: 'timer' }], layouts: { lg: [{ i: 'a', x: 0, y: 0, w: 4, h: 5 }] }, seen: {} });
    expect(doc.layouts.lg.length).toBe(2);
    expect(doc.layouts.xxs.some((l) => l.i === 'b')).toBe(true);
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
    expect(doc.version).toBe(2);
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

  it('newWidgetId is unique per call', () => {
    expect(newWidgetId('scratch')).not.toBe(newWidgetId('scratch'));
  });
});
