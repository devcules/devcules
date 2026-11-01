import { useSyncExternalStore } from 'react';

// activity tracks which terminals are emitting output right now, so a
// row or tab can glow while its pane is working. TerminalView publishes
// each frame it renders; every subscriber holds a short rolling window
// of "recently alive". There is no server signal — it's derived from
// the stream the app already drains, so it costs nothing to keep and
// goes quiet the moment output stops.
const quietAfterMs = 1600;

const lastPush = new Map<string, number>();
const listeners = new Map<string, Set<() => void>>();

// bumpActivity records a frame for one terminal and wakes its
// subscribers if it crossed into "alive".
export function bumpActivity(id: string): void {
  lastPush.set(id, Date.now());
  notify(id);
}

// clearActivity drops a terminal (it closed), so a reused id never
// inherits another's pulse.
export function clearActivity(id: string): void {
  lastPush.delete(id);
  notify(id);
}

function isAlive(id: string): boolean {
  const at = lastPush.get(id);
  return at !== undefined && Date.now() - at < quietAfterMs;
}

function notify(id: string): void {
  listeners.get(id)?.forEach((fn) => fn());
}

// useAlive reports whether terminal id emitted within the window, and
// re-checks just past the moment it would go quiet so the glow fades
// on its own without a timer per row.
export function useAlive(id: string, paneActive: boolean): boolean {
  return useSyncExternalStore(
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
    () => {
      if (!paneActive) {
        return false;
      }
      return isAlive(id);
      },
    () => false,
  );
}
