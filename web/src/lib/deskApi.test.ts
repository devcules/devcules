import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { deskLoaded, deskLoadMark, KEEPALIVE_MAX, putPrefs, resetDeskSave, saveDesk, sameDesk, unsavedDesk, type Prefs } from './deskApi';

type Call = { body: string; keepalive?: boolean };
let calls: Call[] = [];
let answer: (c: Call) => Promise<Response>;

const doc = (text: string): Prefs => ({ version: 2, widgets: [{ id: 'w', type: 'scratch', config: { text } }], layouts: {} });
const bytes = (s: string) => new TextEncoder().encode(s).length;

describe('deskApi keepalive and ordered saves', () => {
  beforeEach(() => {
    calls = [];
    resetDeskSave();
    answer = async () => new Response(null, { status: 204 });
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      const c = { body: init?.body as string, keepalive: init?.keepalive };
      calls.push(c);
      return answer(c);
    }));
  });
  afterEach(() => { vi.unstubAllGlobals(); });

  it('measures the keepalive budget in UTF-8 bytes, not chars', async () => {
    const small = doc('日本語のメモ ✓');
    expect(await putPrefs(small, true)).toBe(true);
    expect(calls[0].keepalive).toBe(true);

    const big = doc('日'.repeat(25_016)); // < 60,000 chars, ~75 KB
    expect(JSON.stringify(big).length).toBeLessThan(KEEPALIVE_MAX);
    expect(bytes(JSON.stringify(big))).toBeGreaterThan(64 * 1024);
    expect(await putPrefs(big, true)).toBe(true);
    expect(calls[1].keepalive).toBeFalsy();
    expect(calls[1].body).toBe(JSON.stringify(big));
  });

  it('shares the budget across keepalive requests in flight', async () => {
    let release: () => void = () => {};
    answer = (c) => (c === calls[0] ? new Promise((r) => { release = () => r(new Response(null, { status: 204 })); }) : Promise.resolve(new Response(null, { status: 204 })));
    const a = putPrefs(doc('a'.repeat(40_000)), true);
    const b = putPrefs(doc('b'.repeat(40_000)), true);
    await b;
    expect(calls[0].keepalive).toBe(true);
    expect(calls[1].keepalive).toBeFalsy();
    release();
    await a;
    await putPrefs(doc('c'.repeat(40_000)), true);
    expect(calls[2].keepalive).toBe(true); // the budget is released
  });

  it('a keepalive fetch the browser rejects falls back to a plain PUT', async () => {
    answer = async (c) => { if (c.keepalive) throw new TypeError('Failed to fetch'); return new Response(null, { status: 204 }); };
    expect(await putPrefs(doc('x'), true)).toBe(true);
    expect(calls.map((c) => !!c.keepalive)).toEqual([true, false]);
  });

  it('saveDesk keeps one save in flight and sends only the newest waiting one', async () => {
    const releases: Array<() => void> = [];
    answer = () => new Promise((r) => releases.push(() => r(new Response(null, { status: 204 }))));
    saveDesk(doc('1'));
    saveDesk(doc('2'));
    saveDesk(doc('3'));
    expect(calls).toHaveLength(1);
    expect(unsavedDesk()).toEqual(doc('3'));
    releases[0]();
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    expect(JSON.parse(calls[1].body)).toEqual(doc('3'));
    releases[1]();
    await vi.waitFor(() => expect(unsavedDesk()).toBeNull());
    expect(calls).toHaveLength(2);
  });

  it('a keepalive save overlapping an in-flight one re-sends the newest once both land', async () => {
    const releases: Array<() => void> = [];
    answer = () => new Promise((r) => releases.push(() => r(new Response(null, { status: 204 }))));
    saveDesk(doc('old'));
    saveDesk(doc('new'), true); // page hiding: sent at once
    expect(calls).toHaveLength(2);
    expect(calls[1].keepalive).toBe(true);
    releases[1](); // the newer one lands first
    await Promise.resolve();
    releases[0](); // then the older one would overwrite it
    await vi.waitFor(() => expect(calls).toHaveLength(3));
    expect(JSON.parse(calls[2].body)).toEqual(doc('new'));
    releases[2]();
    await vi.waitFor(() => expect(unsavedDesk()).toBeNull());
  });

  it('sameDesk ignores key order, an empty seen, null lists and the version', () => {
    const a: Prefs = { version: 2, widgets: [{ id: 'w', type: 'scratch', config: { text: 'x', n: 1 } }], layouts: { lg: [] }, seen: {} };
    const b = JSON.parse('{"layouts":{"lg":[]},"widgets":[{"type":"scratch","config":{"n":1,"text":"x"},"id":"w"}],"version":3}') as Prefs;
    expect(sameDesk(a, b)).toBe(true);
    expect(sameDesk({ version: 2, widgets: null as unknown as unknown[], layouts: {} }, { version: 2, widgets: [], layouts: {} })).toBe(true);
    expect(sameDesk(a, doc('x'))).toBe(false);
  });

  it('a save confirmed while a load ran wins over that load\'s older answer', async () => {
    const server = doc('old');
    deskLoaded(server, deskLoadMark()); // the first load: base = server
    let release: () => void = () => {};
    answer = () => new Promise((r) => { release = () => r(new Response(null, { status: 204 })); });
    const mine = doc('mine');
    saveDesk(mine);
    const mark = deskLoadMark();
    release();
    await new Promise((r) => setTimeout(r, 0));
    expect(unsavedDesk()).toBeNull();
    expect(deskLoaded(server, mark)).toBe(mine);
  });
});
