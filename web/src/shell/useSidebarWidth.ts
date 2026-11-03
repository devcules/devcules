// useSidebarWidth — the desktop sidebar's width, dragged and remembered.
//
// The nav column holds tab labels and a deep project tree, and how much
// room each needs is personal: a long project name wants a wider column,
// a small screen wants it back. The width is one number in localStorage
// (per browser, like the theme and the fold state) and is clamped so the
// handle can never squeeze the main area below something usable or push
// the sidebar past the window.

import { useCallback, useEffect, useRef, useState } from 'react';

const KEY = 'devcules.sidebar.width';

// MIN/MAX bound the drag. The max is also clamped to the window at drag
// time, so a narrow laptop never gets a 480px column.
export const SIDEBAR_MIN = 160;
export const SIDEBAR_MAX = 480;
export const SIDEBAR_DEFAULT = 240; // Tailwind w-60, what the shell shipped with

// Room the main area must keep: below this the drag stops, so the desk
// and terminals are never squeezed to nothing.
const MAIN_MIN = 360;

function clamp(v: number): number {
  const room = typeof window === 'undefined' ? SIDEBAR_MAX : window.innerWidth - MAIN_MIN;
  const max = Math.max(SIDEBAR_MIN, Math.min(SIDEBAR_MAX, room));
  return Math.min(max, Math.max(SIDEBAR_MIN, Math.round(v)));
}

/** readSidebarWidth returns the saved width, or the default. Never throws:
 *  private windows and cleared storage can make localStorage unreadable. */
export function readSidebarWidth(): number {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (raw === null) return clamp(SIDEBAR_DEFAULT);
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? clamp(n) : clamp(SIDEBAR_DEFAULT);
  } catch {
    return clamp(SIDEBAR_DEFAULT);
  }
}

function writeSidebarWidth(width: number): void {
  try {
    window.localStorage.setItem(KEY, String(width));
  } catch {
    // A full or blocked store is not worth breaking the drag over.
  }
}

export type SidebarResize = {
  width: number;
  /** dragging is true while the pointer is down, for the handle's look. */
  dragging: boolean;
  /** onPointerDown starts a drag; spread it onto the handle. */
  onPointerDown: (e: React.PointerEvent) => void;
  /** nudge moves the width by delta and saves (the keyboard path). */
  nudge: (delta: number) => void;
  /** reset returns the column to its shipped width. */
  reset: () => void;
};

export function useSidebarWidth(): SidebarResize {
  const [width, setWidth] = useState(readSidebarWidth);
  const [dragging, setDragging] = useState(false);
  // start holds where the drag began: the pointer x and the width at
  // that moment, so every move is relative to the press, not the last move.
  const start = useRef<{ x: number; width: number } | null>(null);

  const stop = useCallback(() => {
    start.current = null;
    setDragging(false);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  }, []);

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    // Left button (or a pen/touch) only; ignore a right-click.
    if (e.button !== 0) return;
    e.preventDefault();
    start.current = { x: e.clientX, width };
    setDragging(true);
    // The handle keeps the pointer capture; the body cursor makes the
    // whole window read as a resize while dragging.
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, [width]);

  useEffect(() => {
    if (!dragging) return;
    const onMove = (e: PointerEvent) => {
      if (start.current === null) return;
      setWidth(clamp(start.current.width + (e.clientX - start.current.x)));
    };
    const onUp = () => {
      if (start.current !== null) {
        // Persist the final width, computed fresh so a stale render
        // cannot save the width from before the last move.
        setWidth((w) => {
          writeSidebarWidth(w);
          return w;
        });
      }
      stop();
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
    return () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
    };
  }, [dragging, stop]);

  // A window resize can make the saved width invalid (a laptop screen
  // after a desktop one), so re-clamp when the window changes.
  useEffect(() => {
    const onResize = () => setWidth((w) => clamp(w));
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => () => {
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  }, []);

  const nudge = useCallback((delta: number) => {
    setWidth((w) => {
      const next = clamp(w + delta);
      writeSidebarWidth(next);
      return next;
    });
  }, []);

  const reset = useCallback(() => {
    const w = clamp(SIDEBAR_DEFAULT);
    setWidth(w);
    writeSidebarWidth(w);
  }, []);

  return { width, dragging, onPointerDown, nudge, reset };
}
