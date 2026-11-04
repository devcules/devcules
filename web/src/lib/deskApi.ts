// Desk API: typed wrappers over /api/desk/* and /api/secrets. Mirrors the
// established Devcules pattern (unauthorized | unreachable | data), so the
// tiles can read session expiry and offline the same way the rest of the
// app does.

import { toast } from './toast';

export type Result<T> = T | 'unauthorized' | 'unreachable';

async function req<T>(path: string, init?: RequestInit, isEmpty = false): Promise<Result<T>> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    return 'unreachable';
  }
  return answer<T>(res, isEmpty);
}

async function answer<T>(res: Response, isEmpty: boolean): Promise<Result<T>> {
  if (res.status === 401) return 'unauthorized';
  if (res.status >= 500) return 'unreachable';
  if (isEmpty) return (res.status < 300 ? (true as unknown as T) : 'unreachable') as Result<T>;
  if (!res.ok) {
    // A 400/502 from a desk proxy carries a readable line ("no Tavily
    // key…") the tile can show instead of a generic offline.
    try {
      const j = (await res.json()) as { error?: string };
      return ({ __deskError: j.error ?? `HTTP ${res.status}` }) as unknown as Result<T>;
    } catch {
      return 'unreachable';
    }
  }
  try {
    return (await res.json()) as T;
  } catch {
    return 'unreachable';
  }
}

export function deskError<T>(r: Result<T>): string | null {
  if (typeof r === 'object' && r !== null && '__deskError' in (r as object)) {
    return String((r as unknown as { __deskError: unknown }).__deskError);
  }
  return null;
}

// KEEPALIVE_MAX keeps keepalive bodies under the browser's 64 KiB budget.
// The budget is in bytes and is shared by every keepalive request still in
// flight, so it is counted in UTF-8 bytes across all of them.
export const KEEPALIVE_MAX = 60_000;
let keepaliveBytes = 0;
const utf8 = new TextEncoder();

// put sends a PUT. keepalive (for pagehide or a hidden tab, when the page
// may be torn down) is used only while the body fits the budget; a larger
// body, or a keepalive fetch the browser rejects, goes as a plain PUT.
async function put(path: string, body: unknown, keepalive = false): Promise<Result<true>> {
  const res = await send(path, body, keepalive);
  return res === 'unreachable' ? res : answer<true>(res, true);
}

// send is put without reading the answer, so a caller can tell a refusal
// (4xx) from being offline.
async function send(path: string, body: unknown, keepalive: boolean): Promise<Response | 'unreachable'> {
  const text = JSON.stringify(body);
  const init: RequestInit = { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: text };
  const plain = async () => { try { return await fetch(path, init); } catch { return 'unreachable' as const; } };
  const bytes = keepalive ? utf8.encode(text).length : 0;
  if (!keepalive || keepaliveBytes + bytes > KEEPALIVE_MAX) return plain();
  keepaliveBytes += bytes;
  try {
    return await fetch(path, { ...init, keepalive: true });
  } catch {
    return plain();
  } finally {
    keepaliveBytes -= bytes;
  }
}

export type Prefs = { version: number; widgets: unknown[]; layouts: Record<string, unknown>; seen?: Record<string, string> };
export const getPrefs = () => req<Prefs>('/api/desk/prefs');
export const putPrefs = (p: Prefs, keepalive = false) => put('/api/desk/prefs', p, keepalive);

// ---- ordered desk saves ----
// A save that will never land: refused by the server (a 4xx other than
// 401), or a failed one the server has moved past since it was made.
export const DESK_SAVE_LOST = "An earlier desk change couldn't be saved";

// The desk document is saved whole (last writer wins), so saves must reach
// the server in order and a remounted Desk must not load a copy older than
// one it is still sending. This state lives at module level, so it outlives
// the Desk: leaving the Desk and coming back before its save has landed
// finds the newer document here.
const deskSave = {
  // The newest document handed to saveDesk that the server has not yet
  // confirmed (still in flight, queued, or failed to send).
  unsaved: null as Prefs | null,
  // The server document `unsaved` was built on: the last load's answer or
  // the last confirmed save. A failed save is re-sent only while the
  // server still holds this, so it never overwrites a newer write made
  // elsewhere (another tab or device) in between.
  base: null as Prefs | null,
  // Counts confirmed saves, so a load can tell one landed while it ran.
  landed: 0,
  inflight: 0,
  // The newest document waiting for the in-flight save to finish.
  queued: null as Prefs | null,
  // A keepalive save was sent while another was in flight, so the two may
  // land out of order: re-send the newest once both are done.
  overlapped: false,
  onUnauthorized: null as null | (() => void),
};

// saveDesk saves the document. One save is in flight at a time and the
// newest waiting document replaces an older waiting one. keepalive (the
// page is being hidden or torn down) sends at once instead of waiting,
// since a queued save may never start once the page is gone.
export function saveDesk(doc: Prefs, keepalive = false, onUnauthorized?: () => void) {
  deskSave.unsaved = doc;
  if (onUnauthorized) deskSave.onUnauthorized = onUnauthorized;
  if (deskSave.inflight > 0) {
    if (!keepalive) { deskSave.queued = doc; return; }
    deskSave.overlapped = true;
  }
  deskSave.queued = null;
  sendDesk(doc, keepalive);
}

type SaveResult = true | 'unauthorized' | 'unreachable' | 'refused';
async function putDesk(doc: Prefs, keepalive: boolean): Promise<SaveResult> {
  const res = await send('/api/desk/prefs', doc, keepalive);
  if (res === 'unreachable') return res;
  if (res.status === 401) return 'unauthorized';
  if (res.status < 300) return true;
  // A 4xx (a body over 1 MiB, too many widgets, bad JSON) will be refused
  // every time: it is lost, not offline. A 5xx may pass later.
  return res.status < 500 ? 'refused' : 'unreachable';
}

function sendDesk(doc: Prefs, keepalive: boolean) {
  deskSave.inflight++;
  void putDesk(doc, keepalive).then((r) => {
    deskSave.inflight--;
    if (r === 'unauthorized') {
      deskSave.unsaved = null; deskSave.queued = null; deskSave.overlapped = false;
      deskSave.onUnauthorized?.();
      return;
    }
    if (r === true) { deskSave.base = doc; deskSave.landed++; }
    if (r === 'refused') {
      toast(DESK_SAVE_LOST, 'bad');
      if (deskSave.unsaved === doc) deskSave.unsaved = null;
    }
    if (deskSave.inflight > 0) return;
    if (deskSave.queued) { const q = deskSave.queued; deskSave.queued = null; sendDesk(q, false); return; }
    if (r === 'unreachable') { deskSave.overlapped = false; return; } // keep `unsaved`: the next load decides
    if (deskSave.overlapped) {
      deskSave.overlapped = false;
      if (deskSave.unsaved && deskSave.unsaved !== doc) { sendDesk(deskSave.unsaved, false); return; }
    }
    if (deskSave.unsaved === doc) deskSave.unsaved = null;
  });
}

// deskLoadMark is taken just before a Desk reads the server document;
// deskLoaded takes the answer and returns the document to show.
export const deskLoadMark = () => ({ unsaved: deskSave.unsaved, landed: deskSave.landed });

export function deskLoaded(server: Prefs, mark: ReturnType<typeof deskLoadMark>, onUnauthorized?: () => void): Prefs {
  const local = deskSave.unsaved;
  // Still sending: that PUT lands after this GET, so the local copy is
  // the newest.
  if (local && deskSaving()) return local;
  if (!local) {
    // A save confirmed while this GET ran may postdate its answer.
    if (mark.unsaved && deskSave.landed !== mark.landed && deskSave.base) return deskSave.base;
    deskSave.base = server;
    return server;
  }
  // A failed save. The server already has it (a lost answer): done.
  if (sameDesk(server, local)) { deskSave.unsaved = null; deskSave.base = server; return local; }
  // The server is where it was when the edit was made: send it again.
  if (deskSave.base && sameDesk(server, deskSave.base)) { saveDesk(local, false, onUnauthorized); return local; }
  // The server moved on (another tab or device wrote): the failed copy is
  // stale. Show the server's and say the earlier change was lost.
  deskSave.unsaved = null;
  deskSave.base = server;
  toast(DESK_SAVE_LOST, 'bad');
  return server;
}

// sameDesk compares two desk documents as the server stores them: key
// order, a missing or empty `seen`, a null list and the version (the
// server forces it) do not count.
export function sameDesk(a: Prefs, b: Prefs): boolean {
  const norm = (p: Prefs) => canon({
    widgets: p.widgets ?? [], layouts: p.layouts ?? {},
    seen: p.seen && Object.keys(p.seen).length > 0 ? p.seen : undefined,
  });
  return norm(a) === norm(b);
}

function canon(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map((x) => (x === undefined ? 'null' : canon(x))).join(',')}]`;
  if (v && typeof v === 'object') {
    const o = v as Record<string, unknown>;
    return `{${Object.keys(o).filter((k) => o[k] !== undefined).sort().map((k) => `${JSON.stringify(k)}:${canon(o[k])}`).join(',')}}`;
  }
  return JSON.stringify(v) ?? 'null';
}

// unsavedDesk is the newest desk document the server may not have yet.
export const unsavedDesk = () => deskSave.unsaved;
// deskSaving reports a save in flight or waiting.
export const deskSaving = () => deskSave.inflight > 0 || deskSave.queued !== null;

// resetDeskSave clears the module state; tests only.
export function resetDeskSave() {
  deskSave.unsaved = null; deskSave.base = null; deskSave.landed = 0; deskSave.queued = null; deskSave.inflight = 0; deskSave.overlapped = false; deskSave.onUnauthorized = null;
  keepaliveBytes = 0;
}

export type Sources = Record<string, unknown> & {
  location?: string; news_security_query?: string; news_tech_query?: string; news_custom_query?: string;
  has_tavily?: boolean; has_ticket_token?: boolean;
  ticket_provider?: string; ticket_site?: string; ticket_email?: string; ticket_jql?: string; ticket_project?: string;
  ics_feeds?: Array<{ label: string; url: string }>;
  tavily_key?: string; ticket_token?: string;
};
export const getSources = () => req<Sources>('/api/desk/sources');
export const putSources = (s: Sources) => put('/api/desk/sources', s);

export const getWeather = (refresh = false) => req<Record<string, unknown>>(`/api/desk/weather${refresh ? '?refresh=1' : ''}`);
export const getNews = (which: string, refresh = false) => req<{ configured?: boolean; items?: unknown[]; stale?: boolean }>(`/api/desk/news/${which}${refresh ? '?refresh=1' : ''}`);
export const getTickets = (refresh = false) => req<Record<string, unknown>>(`/api/desk/tickets${refresh ? '?refresh=1' : ''}`);
export const getMeetings = (refresh = false) => req<Record<string, unknown>>(`/api/desk/meetings${refresh ? '?refresh=1' : ''}`);

export const listSecrets = () => req<{ names: string[] }>('/api/secrets');
export const putSecret = (name: string, value: string) => put('/api/secrets', { name, value });

// ---- calendar (the user's own plans merged with feeds) ----
export type CalEvent = {
  id: string; title: string; start: string; end: string; all_day?: boolean;
  location?: string; url?: string; notes?: string; reminder_min?: number;
  rrule?: string; feed?: string; plan?: boolean; occurrence?: boolean;
  // On an occurrence of a series: the series' own stored start and end.
  series_start?: string; series_end?: string;
};
export const getCalendar = (from: string, to: string, refresh = false) =>
  req<{ events: CalEvent[]; configured: boolean; plans: number; stale?: boolean }>(
    `/api/calendar/events?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}${refresh ? '&refresh=1' : ''}`);
export const createCalendar = (e: Partial<CalEvent>) =>
  req<CalEvent>('/api/calendar/events', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(e) });
export const updateCalendar = (id: string, e: Partial<CalEvent>) =>
  req<CalEvent>(`/api/calendar/events/${encodeURIComponent(id)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(e) });
export const deleteCalendar = async (id: string): Promise<Result<true>> => {
  let res: Response;
  try {
    res = await fetch(`/api/calendar/events/${encodeURIComponent(id)}`, { method: 'DELETE' });
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) return 'unauthorized';
  // 404: the name is already gone (a second click, another tab), which
  // is the outcome the caller wanted.
  return res.status === 204 || res.status === 404 ? true : 'unreachable';
};
export const quickAddCalendar = (text: string) =>
  req<CalEvent>('/api/calendar/quick', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text }) });
export const deleteSecret = async (name: string): Promise<Result<true>> => {
  let res: Response;
  try {
    res = await fetch(`/api/secrets/${encodeURIComponent(name)}`, { method: 'DELETE' });
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) return 'unauthorized';
  // 404: the name is already gone (a second click, another tab), which
  // is the outcome the caller wanted.
  return res.status === 204 || res.status === 404 ? true : 'unreachable';
};
