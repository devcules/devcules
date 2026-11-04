import { useEffect, useRef, useState } from 'react';
import {
  ctrlHoldEnd,
  ctrlHoldStart,
  ctrlState,
  ctrlTap,
  focusFocused,
  haptic,
  hasInput,
  onCtrlChange,
  onInputChange,
  send,
  sendClipboard,
  sendKey,
  stepTermFont,
  requestFs,
  type CtrlState,
} from './keybus';
import { useMobile } from './useMobile';

// TermKeyboard is the docked custom keyboard for terminals and
// assistants — one horizontally-scrolling strip at the bottom of the
// open pane. It is a single scroll
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
  // font steps the terminal text size instead of sending bytes.
  font?: number;
};

const keys: KeyDef[] = [
  { text: 'Esc', aria: 'Escape', seq: '\x1b' },
  { text: '⇥', aria: 'Tab', seq: '\t' },
  { text: 'Ctrl', aria: 'Control (tap for one, hold to keep)', mod: 'ctrl' },
  { text: '←', aria: 'Left arrow', holdSeq: '\x1b[D' },
  { text: '↑', aria: 'Up arrow', holdSeq: '\x1b[A' },
  { text: '↓', aria: 'Down arrow', holdSeq: '\x1b[B' },
  { text: '→', aria: 'Right arrow', holdSeq: '\x1b[C' },
  { text: '^C', aria: 'Ctrl-C', seq: '\x03', danger: true },
  { text: '^D', aria: 'Ctrl-D (end of input)', seq: '\x04' },
  { text: '^R', aria: 'Ctrl-R (history search)', seq: '\x12' },
  { text: '^Z', aria: 'Ctrl-Z (suspend)', seq: '\x1a' },
  { text: '^L', aria: 'Ctrl-L (clear)', seq: '\x0c' },
  { text: '|', aria: 'Pipe', seq: '|' },
  { text: '/', aria: 'Slash', seq: '/' },
  { text: '~', aria: 'Tilde', seq: '~' },
  { text: '-', aria: 'Dash', seq: '-' },
  { text: 'Home', aria: 'Home', seq: '\x1b[H' },
  { text: 'End', aria: 'End', seq: '\x1b[F' },
  { text: 'PgUp', aria: 'Page up', holdSeq: '\x1b[5~' },
  { text: 'PgDn', aria: 'Page down', holdSeq: '\x1b[6~' },
  { text: 'Paste', aria: 'Paste from clipboard' },
  { text: 'A−', aria: 'Smaller terminal text', font: -1 },
  { text: 'A+', aria: 'Larger terminal text', font: 1 },
];

export default function TermKeyboard({
  position = 'top',
  onShowTabs,
}: {
  // The strip docks at the top of the open pane and the OS
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

  // Enter and ☰ stay in reach whether or not the strip is collapsed:
  // in fullscreen ☰ is the only way back to the app's tabs.
  const enterKey = (
    <button
      type="button"
      aria-label="Enter"
      title="Enter"
      onPointerDown={(e) => e.preventDefault()}
      onClick={() => {
        haptic();
        sendKey('\r');
      }}
      className="mk-key mk-enter min-h-9 min-w-11 rounded-md px-3 text-tab font-semibold"
    >
      ⏎
    </button>
  );
  const tabsKey = (
    <button
      type="button"
      aria-label="Show tabs"
      title="Show tabs and menus"
      onPointerDown={(e) => e.preventDefault()}
      onClick={() => (onShowTabs ? onShowTabs() : requestFs(false))}
      className="mk-key i-control min-h-9 min-w-9 rounded-md px-2 text-tab text-text"
    >
      ☰
    </button>
  );

  if (collapsed) {
    return (
      <div className={`flex items-center justify-end gap-1 bg-surface px-2 ${bottomPad} pt-1`}>
        <button
          type="button"
          aria-label="Show keyboard strip"
          title="Show keyboard strip"
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => setCollapsed(false)}
          className="i-control rounded-md px-2 py-1 text-tab text-text"
        >
          ⌨
        </button>
        {enterKey}
        {tabsKey}
      </div>
    );
  }

  return (
    <div
      aria-label="Custom terminal keyboard"
      // A host column with intrinsic content can stretch this strip
      // past the viewport; clamp it to the visible width and let the
      // row below be the thing that scrolls (the strip scrolls
      // inside itself, not the page).
      className={`flex w-full max-w-[100vw] min-w-0 items-stretch gap-1 overflow-hidden ${side} border-muted bg-surface px-2 ${bottomPad} pt-2 select-none`}
      style={{ touchAction: 'manipulation' }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <ScrollRow>
        {keys.map((k) => (
          <SKey key={k.text} def={k} ctrl={ctrl} />
        ))}
        <button
          type="button"
          aria-label="Hide keyboard strip"
          title="Hide keyboard strip"
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => setCollapsed(true)}
          className="mk-key i-control min-h-9 min-w-10 shrink-0 rounded-md px-2 text-tab text-muted"
        >
          ▾
        </button>
      </ScrollRow>
      <div className="flex shrink-0 items-center gap-1 pl-1">
        {/* Enter is pinned outside the scrolling row so it is always one
            thumb away, whatever the row is scrolled to. */}
        {enterKey}
        {tabsKey}
        <button
          type="button"
          aria-label="Raise the software keyboard"
          title="Raise the software keyboard"
          onMouseDown={(e) => e.preventDefault()}
          onClick={focusFocused}
          className="mk-key i-control min-h-9 min-w-9 rounded-md px-2 text-tab text-text"
        >
          ⌨
        </button>
      </div>
    </div>
  );
}

// ScrollRow is the strip's horizontal track. The body scroll-lock
// suppresses the default pan on the page; the row (and each key)
// opts into pan-x, so a sideways swipe scrolls it natively. Our SKey's pointermove cancels a tap after 10 px
// of travel, so a scroll never fires a key.
function ScrollRow({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="mk-scroll flex min-w-0 flex-1 items-center gap-1 overflow-x-auto overflow-y-hidden"
      style={{ touchAction: 'pan-x', scrollbarWidth: 'none', WebkitOverflowScrolling: 'touch', overscrollBehaviorX: 'contain' }}
    >
      {children}
    </div>
  );
}

// SKey is one strip key. Pointer events drive it: a tap is a press
// and release under DRAG_PX of travel; more travel is the row scrolling,
// so nothing fires and a held key also releases. One pointer is tracked
// at a time: a new press supersedes the old one (its timers are cleared
// first), and events from any other pointer are ignored. click is only
// for activation that comes without a pointer press (keyboard Enter or
// Space, a screen reader's click), so a touch tap never fires twice:
// each pointer press owes one click, which the next pointer click
// consumes however late it lands. Keyboard and assistive clicks carry
// detail 0 and always fire. Control sequences and the clipboard apply
// through keybus.

function SKey({ def, ctrl }: { def: KeyDef; ctrl: CtrlState }) {
  const start = useRef<{ x: number; y: number } | null>(null);
  const pointer = useRef<number | null>(null);
  const clickOwed = useRef(false);
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

  // A strip that unmounts mid-press (back, a tab change, a pane swap)
  // must not leave a repeat firing into whichever pane registers next,
  // nor a held Ctrl latched for good.
  useEffect(
    () => () => {
      if (repeat.current !== null) {
        window.clearInterval(repeat.current);
        repeat.current = null;
      }
      if (holdTimer.current !== null) {
        window.clearTimeout(holdTimer.current);
        holdTimer.current = null;
      }
      if (def.mod === 'ctrl') {
        ctrlHoldEnd();
      }
    },
    [def.mod],
  );

  const fireTap = () => {
    haptic();
    if (def.font !== undefined) {
      stepTermFont(def.font);
      return;
    }
    if (def.mod === 'ctrl') {
      ctrlTap();
      return;
    }
    if (def.aria.startsWith('Paste')) {
      void sendClipboard();
      return;
    }
    if (def.seq !== undefined) {
      sendKey(def.seq);
    } else if (def.holdSeq !== undefined) {
      sendKey(def.holdSeq);
    }
  };
  const repeated = useRef(false);

  const onDown = (e: React.PointerEvent) => {
    // No preventDefault on pointerdown: doing so
    // suppresses the browser's native pan gesture, and a sideways
    // swipe fails to scroll the strip. We track the point and only
    // cancel the key when the browser hasn't already taken over the
    // gesture (the pointer-move drag threshold handles the scroll).
    // A second finger, or a press whose release was lost, must not
    // orphan the running timers: clear them before arming new ones.
    stopRepeat();
    clickOwed.current = true;
    pointer.current = e.pointerId ?? null;
    start.current = { x: e.clientX, y: e.clientY };
    if (def.mod === 'ctrl') {
      // Long-press = held Ctrl; a short tap arms one-shot (handled on up).
      holdTimer.current = window.setTimeout(() => {
        ctrlHoldStart();
      }, HOLD_MS);
      return;
    }
    if (def.holdSeq !== undefined) {
      // Nothing fires on press: a swipe that starts on an arrow must
      // scroll the row, not move the cursor. A tap fires on release; a
      // hold past HOLD_MS starts the repeat.
      repeated.current = false;
      holdTimer.current = window.setTimeout(() => {
        repeated.current = true;
        haptic();
        // The first send applies (and uses up) an armed Ctrl, and the
        // repeats keep sending those same bytes. Otherwise each repeat
        // follows the live Ctrl, like a physical keyboard: releasing a
        // held Ctrl mid-repeat switches it to the plain key.
        const oneShot = ctrlState() === 'armed';
        const seq = sendKey(def.holdSeq!);
        repeat.current = window.setInterval(() => {
          if (oneShot) {
            send(seq);
          } else if (ctrlState() === 'armed') {
            send(def.holdSeq!); // a tap mid-repeat arms the next key, not this one
          } else {
            sendKey(def.holdSeq!);
          }
        }, 70);
      }, HOLD_MS);
    }
  };

  // ours reports whether an event belongs to the tracked press.
  const ours = (e: React.PointerEvent) => start.current !== null && (e.pointerId ?? null) === pointer.current;

  // cancelPress drops the tracked press without firing: the pointer
  // left the key, the browser took the gesture, or capture was lost.
  const cancelPress = (e: React.PointerEvent) => {
    if (!ours(e)) {
      return;
    }
    stopRepeat();
    start.current = null;
    pointer.current = null;
    repeated.current = false;
    if (def.mod === 'ctrl') {
      ctrlHoldEnd();
    }
  };

  const onMove = (e: React.PointerEvent) => {
    if (!ours(e) || !start.current) {
      return;
    }
    const dx = Math.abs(e.clientX - start.current.x);
    const dy = Math.abs(e.clientY - start.current.y);
    if (dx > DRAG_PX || dy > DRAG_PX) {
      // The row is scrolling; cancel anything armed and let it pan.
      stopRepeat();
      if (def.mod === 'ctrl') {
        ctrlHoldEnd();
      }
      start.current = null;
      pointer.current = null;
    }
  };

  const onUp = (e: React.PointerEvent) => {
    if (!ours(e)) {
      return; // another finger, or the press became a scroll
    }
    stopRepeat();
    start.current = null;
    pointer.current = null;
    if (def.mod === 'ctrl' && ctrlState() === 'held') {
      ctrlHoldEnd();
      return; // a hold-release, not a tap
    }
    if (def.holdSeq !== undefined && repeated.current) {
      repeated.current = false;
      return; // the hold already sent
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
      // A mouse dragged off the key, or a lost capture, ends the press;
      // a touch keeps implicit capture, so these only follow its up.
      onPointerLeave={cancelPress}
      onLostPointerCapture={cancelPress}
      onPointerCancel={cancelPress}
      // Keyboard Enter/Space and a screen reader's click arrive as a
      // click with detail 0 and no pointer press before it; a pointer
      // tap already fired on pointerup, so its trailing click (detail
      // >= 1) only pays off the press. A press with no click (a swipe,
      // a long-press) leaves the flag set, which is harmless: any later
      // pointer click has its own press, which sets it again.
      onClick={(e) => {
        if (start.current !== null) {
          return;
        }
        if (e.detail !== 0 && clickOwed.current) {
          clickOwed.current = false;
          return;
        }
        fireTap();
      }}
      // Cancel only the compatibility mousedown: the tap must not move
      // focus off the terminal (that drops the Android soft keyboard),
      // and pointerdown stays untouched so native pan still works.
      onMouseDown={(e) => e.preventDefault()}
      onContextMenu={(e) => e.preventDefault()}
      // mk-key is touch-action: pan-x, matching the row: a sideways
      // swipe that starts on a key scrolls natively (with momentum),
      // and the browser's pointercancel / our 10 px check drops the key.
      className={`mk-key i-control min-h-9 min-w-10 shrink-0 rounded-md px-2.5 text-tab font-semibold transition-colors ${
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
