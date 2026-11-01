// Desk timer — pure state helpers. The running timer is persisted as an
// absolute end time (plus the chosen length) so leaving the Desk tab, or
// reloading, does not reset it; a paused timer stores the remaining ms.

export const TIMER_KEY = 'devcules.desk.timer';
export const PRESETS_MIN = [25, 5, 15];

export type Timer = { totalMs: number; running: boolean; endsAt: number; remainingMs?: number };

export function readTimer(storage = safeStorage()): Timer | null {
  try {
    const raw = storage?.getItem(TIMER_KEY);
    if (!raw) return null;
    const t = JSON.parse(raw) as Timer;
    if (!t || typeof t.totalMs !== 'number') return null;
    return t;
  } catch {
    return null;
  }
}

export function writeTimer(t: Timer | null, storage = safeStorage()): void {
  try {
    if (!t) storage?.removeItem(TIMER_KEY);
    else storage?.setItem(TIMER_KEY, JSON.stringify(t));
  } catch {
    /* private mode */
  }
}

export function startTimer(minutes: number, now = Date.now()): Timer {
  const totalMs = Math.max(1, Math.round(minutes * 60000));
  return { totalMs, running: true, endsAt: now + totalMs };
}

export function pauseTimer(t: Timer, now = Date.now()): Timer {
  return { ...t, running: false, remainingMs: Math.max(0, t.endsAt - now) };
}

export function resumeTimer(t: Timer, now = Date.now()): Timer {
  return { ...t, running: true, endsAt: now + (t.remainingMs ?? t.totalMs) };
}

export function remainingMs(t: Timer | null, now = Date.now()): number {
  if (!t) return 0;
  if (t.running) return Math.max(0, t.endsAt - now);
  return Math.max(0, t.remainingMs ?? t.totalMs);
}

export function isFinished(t: Timer | null, now = Date.now()): boolean {
  return !!t && t.running && t.endsAt <= now;
}

export function fmtClock(ms: number): string {
  const s = Math.ceil(Math.max(0, ms) / 1000);
  const m = Math.floor(s / 60);
  const r = s % 60;
  return `${String(m).padStart(2, '0')}:${String(r).padStart(2, '0')}`;
}

function safeStorage(): Storage | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null;
  } catch {
    return null;
  }
}
