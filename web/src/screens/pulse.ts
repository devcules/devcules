import { useSyncExternalStore } from 'react';

// pulse tracks which terminals are emitting output right now, so a
// row or tab can glow while its pane is working. TerminalView publishes
// each frame it renders; the store keeps a short rolling window of
// "recently alive" per terminal. There is no server signal — it's
// derived from the stream the app already drains, so it costs nothing
// to keep. When the window lapses without a new frame, one timer turns
// the glow off, so a stopped pane never stays lit.
const quietAfterMs = 1600;

const lastPush = new Map<string, number>();
const quietTimer = new Map<string, number>();
const listeners = new Map<string, Set<() => void>>();

// bumpActivity records a frame for one terminal and wakes its
// subscribers. A still-warm frame only refreshes the timestamp; a cold
// one (first, or after it went quiet) also notifies and arms the timer
// that will report the pane going quiet.
export function bumpActivity(id: string): void {
  const wasAlive = isAlive(id);
  lastPush.set(id, Date.now());
  if (!wasAlive) {
    notify(id);
  }
  armQuiet(id);
}

// clearActivity drops a terminal (it closed), so a reused id never
// inherits another's pulse or timer.
export function clearActivity(id: string): void {
  const t = quietTimer.get(id);
  if (t !== undefined) {
    window.clearTimeout(t);
    quietTimer.delete(id);
  }
  lastPush.delete(id);
  notify(id);
}

// armQuiet (re)schedules the one timer that fires just past the
// current window. Each new frame re-arms, so a busy pane keeps exactly
// one pending timer rather than one per frame.
function armQuiet(id: string): void {
  const old = quietTimer.get(id);
  if (old !== undefined) {
    window.clearTimeout(old);
  }
  quietTimer.set(
    id,
    window.setTimeout(() => {
      quietTimer.delete(id);
      // Notify only if it's genuinely quiet now: a frame that landed in
      // the final stretch already re-armed this, so we'd have been
      // cleared. This also re-arms nothing when a listener unsubscribed
      // in the meantime (notify is a no-op on an absent set).
      if (!isAlive(id)) {
        notify(id);
      }
    }, quietAfterMs + 20),
  );
}

function isAlive(id: string): boolean {
  const at = lastPush.get(id);
  return at !== undefined && Date.now() - at < quietAfterMs;
}

function notify(id: string): void {
  listeners.get(id)?.forEach((fn) => fn());
}

// useAlive reports whether terminal id emitted within the window. It
// re-renders on bump (alive) and on the quiet timer (dim), so the glow
// both starts and stops by itself.
export function useAlive(id: string, paneActive: boolean): boolean {
  const alive = useSyncExternalStore(
    (onStoreChange) => {
      let set = listeners.get(id);
      if (!set) {
        set = new Set();
        listeners.set(id, set);
      }
      set.add(onStoreChange);
      return () => {
        set.delete(onStoreChange);
        if (set.size === 0) {
          listeners.delete(id);
        }
      };
    },
    () => isAlive(id),
    () => false,
  );
  return paneActive && alive;
}
