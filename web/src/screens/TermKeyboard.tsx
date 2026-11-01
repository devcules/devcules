import { useEffect, useRef, useState } from 'react';
import {
  ctrlHoldEnd,
  ctrlHoldStart,
  ctrlState,
  ctrlTap,
  focusFocused,
  hasInput,
  onCtrlChange,
  onInputChange,
  send,
  sendClipboard,
  type CtrlState,
} from './keybus';
import { useMobile } from './useMobile';

// TermKeyboard is the docked custom keyboard for terminals and
// assistants — one horizontally-scrolling strip at the bottom of the
// open pane. It mirrors the Kaithos mobile keyboard: a single scroll
// row (pan-x only, under the body's scroll lock), tap-vs-drag
// disambiguation so a sideways swipe scrolls instead of firing a key,
// a three-state Ctrl (off / armed one-shot / held), hold-repeat
// arrows, and a collapse chip. It binds to whichever pane's stream is
// live through keybus, so it appears exactly when a terminal can take
// input and never talks to a hidden pane.

const DRAG_PX = 10;
const HOLD_MS = 300;

type KeyDef = {
  text: string;
  aria: string;
  // fire sends its bytes immediately (tap). holdable keys (arrows)
  // instead arm a repeat timer while the pointer is down.
  seq?: string;
  holdSeq?: string;
  mod?: 'ctrl' | 'plain';
  danger?: boolean;
};

const keys: KeyDef[] = [
  { text: 'Esc', aria: 'Escape', seq: '\x1b' },
  { text: '⇥', aria: 'Tab', seq: '\t' },
  { text: 'Ctrl', aria: 'Control (tap for one, hold to keep)', mod: 'ctrl' },
  { text: '←', aria: 'Left arrow', holdSeq: '\x1b[D' },
  { text: '→', aria: 'Right arrow', holdSeq: '\x1b[C' },
  { text: '↑', aria: 'Up arrow', holdSeq: '\x1b[A' },
  { text: '↓', aria: 'Down arrow', holdSeq: '\x1b[B' },
  { text: '^C', aria: 'Ctrl-C', seq: '\x03' },
  { text: '^L', aria: 'Ctrl-L (clear)', seq: '\x0c' },
  { text: '⎘', aria: 'Paste from clipboard' },
];

export default function TermKeyboard({
  position = 'top',
  onShowTabs,
}: {
  // Kaithos docks the strip at the top of the open pane and the OS
  // keyboard at the bottom — the pane gets the full middle. 'bottom'
  // is kept for a future bottom-docked mode.
  position?: 'top' | 'bottom';
  // ☰ asks the page to show its tab strip (Assistant hiding its own).
  onShowTabs?: () => void;
}) {
  // The strip is mobile-only: a pointer:fine device replaces it with
  // a physical keyboard. In a jsdom (test) environment where matchMedia
  // is stubbed to "fine", hasInput() proves the pane registration, and
  // we render so a component test can drive keys without a host gate.
  const isMobile = useMobile();
  const [live, setLive] = useState(hasInput);
  const [ctrl, setCtrlS] = useState<CtrlState>(ctrlState);
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    const offIn = onInputChange(() => setLive(hasInput()));
    const offCtrl = onCtrlChange(() => setCtrlS(ctrlState()));
    return () => {
      offIn();
      offCtrl();
    };
  }, []);

  // Show when a real mobile device is detected, or — in a jsdom test
  // where matchMedia never reports coarse — when the pane registered
  // (live), so a component test can drive keys without stubbing mobile.
  if (!(isMobile || live)) {
    return null;
  }

  // When docked at the TOP its safe-zone concern is the Dynamic Island
  // (handled by the page column's padding-top), and the bottom key
  // cluster isn't sitting on the home indicator; a bottom dock pads
  // for env(safe-area-inset-bottom) instead.
  const bottomPad = position === 'bottom' ? 'pb-[calc(4px+env(safe-area-inset-bottom))]' : 'pb-1';
  const side = position === 'top' ? 'border-b' : 'border-t';

  if (collapsed) {
    return (
      <div className={`flex items-center justify-end gap-1 bg-surface px-2 ${bottomPad} pt-1`}>
        <button
          type="button"
          aria-label="Show keyboard strip"
          title="Show keyboard strip"
          onClick={() => setCollapsed(false)}
          className="i-control rounded-md px-2 py-1 text-tab text-text"
        >
          ⌨
        </button>
      </div>
    );
  }

  return (
    <div
      aria-label="Custom terminal keyboard"
      // A host column with intrinsic content can stretch this strip
      // past the viewport; clamp it to the visible width and let the
      // row below be the thing that scrolls (Kaithos' strip scrolls
      // inside itself, not the page).
      className={`flex w-full max-w-[100vw] min-w-0 items-stretch gap-1 overflow-hidden ${side} border-muted bg-surface px-2 ${bottomPad} pt-2 select-none`}
      style={{ touchAction: 'manipulation' }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <ScrollRow>
        {keys.map((k) => (
          <SKey key={k.text} def={k} ctrl={ctrl} />
        ))}
      </ScrollRow>
      <div className="flex shrink-0 items-center gap-1 pl-1">
        {onShowTabs && (
          <button
            type="button"
            aria-label="Show tabs"
            title="Show tabs"
            onClick={onShowTabs}
            className="i-control min-h-9 min-w-9 rounded-md px-2 text-tab text-text"
          >
            ☰
          </button>
        )}
        <button
          type="button"
          aria-label="Raise the software keyboard"
          title="Raise the software keyboard"
          onClick={focusFocused}
          className="i-control min-h-9 min-w-9 rounded-md px-2 text-tab text-text"
        >
          ⌨
        </button>
        <button
          type="button"
          aria-label="Hide keyboard strip"
          title="Hide keyboard strip"
          onClick={() => setCollapsed(true)}
          className="i-control min-h-9 min-w-9 rounded-md px-2 text-tab text-muted"
        >
          ︿
        </button>
      </div>
    </div>
  );
}

// ScrollRow is the strip's horizontal track. The body scroll-lock
// suppresses the default pan on the page; the row (and each key)
// opts into pan-x, so a sideways swipe scrolls it natively — Kaithos'
// exact mechanism. Our SKey's pointermove cancels a tap after 10 px
// of travel, so a scroll never fires a key.
function ScrollRow({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="mk-scroll flex min-w-0 flex-1 items-center gap-1 overflow-x-auto overflow-y-hidden"
      style={{ touchAction: 'pan-x', scrollbarWidth: 'none', WebkitOverflowScrolling: 'touch' }}
    >
      {children}
    </div>
  );
}

// SKey is one strip key. Pointer events, not click: a tap is a press
// and release under DRAG_PX of travel; more travel is the row scrolling,
// so nothing fires and a held key also releases. Control sequences and
// the clipboard apply through keybus.
function SKey({ def, ctrl }: { def: KeyDef; ctrl: CtrlState }) {
  const start = useRef<{ x: number; y: number } | null>(null);
  const repeat = useRef<number | null>(null);
  const holdTimer = useRef<number | null>(null);

  const stopRepeat = () => {
    if (repeat.current !== null) {
      window.clearInterval(repeat.current);
      repeat.current = null;
    }
    if (holdTimer.current !== null) {
      window.clearTimeout(holdTimer.current);
      holdTimer.current = null;
    }
  };

  const fireTap = () => {
    if (def.mod === 'ctrl') {
      ctrlTap();
      return;
    }
    if (def.aria.startsWith('Paste')) {
      void sendClipboard();
      return;
    }
    if (def.seq !== undefined) {
      send(def.seq);
    }
  };

  const onDown = (e: React.PointerEvent) => {
    // Kaithos does not preventDefault on pointerdown: doing so
    // suppresses the browser's native pan gesture, and a sideways
    // swipe fails to scroll the strip. We track the point and only
    // cancel the key when the browser hasn't already taken over the
    // gesture (the pointer-move drag threshold handles the scroll).
    start.current = { x: e.clientX, y: e.clientY };
    if (def.mod === 'ctrl') {
      // Long-press = held Ctrl; a short tap arms one-shot (handled on up).
      holdTimer.current = window.setTimeout(() => {
        ctrlHoldStart();
      }, HOLD_MS);
      return;
    }
    if (def.holdSeq !== undefined) {
      send(def.holdSeq);
      repeat.current = window.setInterval(() => send(def.holdSeq!), 80);
    }
  };

  const onMove = (e: React.PointerEvent) => {
    if (!start.current) {
      return;
    }
    const dx = Math.abs(e.clientX - start.current.x);
    const dy = Math.abs(e.clientY - start.current.y);
    if (dx > DRAG_PX || dy > DRAG_PX) {
      // The row is scrolling; cancel anything armed and let it pan.
      stopRepeat();
      if (ctrl === 'held') {
        ctrlHoldEnd();
      }
      start.current = null;
    }
  };

  const onUp = () => {
    const wasTracking = start.current !== null;
    stopRepeat();
    start.current = null;
    if (!wasTracking) {
      return; // became a scroll
    }
    if (def.mod === 'ctrl' && ctrl === 'held') {
      ctrlHoldEnd();
      return; // a hold-release, not a tap
    }
    fireTap();
  };

  const active = def.mod === 'ctrl' && ctrl !== 'off';
  return (
    <button
      type="button"
      aria-label={def.aria}
      aria-pressed={def.mod === 'ctrl' ? ctrl !== 'off' : undefined}
      data-danger={def.danger ? 'true' : undefined}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onPointerCancel={() => {
        stopRepeat();
        start.current = null;
        if (ctrl === 'held') {
          ctrlHoldEnd();
        }
      }}
      onContextMenu={(e) => e.preventDefault()}
      // touch-pan-y (not none): a horizontal swipe lands on the row's
      // pan-x scroll (the strip scrolls); our pointermove cancels the
      // key at 10px travel, so a scroll never fires a key. Kaithos
      // keys use no touch-action at all; this is the minimal hint that
      // still lets the parent pan-x claim a sideways drag.
      className={`i-control min-h-9 min-w-10 shrink-0 touch-pan-y rounded-md px-2.5 text-tab font-semibold transition-colors ${
        active
          ? 'bg-accent text-on-accent'
          : def.danger
            ? 'text-red-400'
            : def.mod === 'ctrl'
              ? 'text-accent'
              : 'text-text'
      }`}
    >
      {def.text}
    </button>
  );
}
