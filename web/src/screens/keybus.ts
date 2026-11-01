// keybus is the wire between the one docked custom keyboard and
// whichever terminal pane currently owns a stream. Panes register a
// sender when their WebSocket opens and drop it when they unmount; the
// keyboard never holds a React ref or context to them. Ctrl is a
// module-level modifier (armed = next char, held = until released), so
// both the strip keys and the OS keyboard's typed letters can carry it.
//
// This is the Kaithos pattern: module state, not component state, so a
// pane swap mid-typing keeps the modifier and the keyboard instantly
// rebinds to the new pane's input path.

type Sender = (bytes: Uint8Array) => void;
type Focuser = () => void;
// Opts carries the per-pane extras the keyboard needs beyond raw
// bytes: scroll-to-bottom after a reflow, and the like.
type Opts = { afterReflow?: () => void };

let sender: Sender | null = null;
let focuser: Focuser | null = null;
let afterReflow: (() => void) | null = null;
const listeners = new Set<() => void>();

// Ctrl is 'off', a one-shot 'armed' latch, or 'held' across many keys.
export type CtrlState = 'off' | 'armed' | 'held';
let ctrl: CtrlState = 'off';
const ctrlListeners = new Set<() => void>();

// registerInput binds the keyboard to one pane's stream; the pane's
// cleanup unregisters. Only ever one pane is live (the active tab), so
// a single sender suffices — a fresh mount of the same pane rebinds.
export function registerInput(send: Sender, focus: Focuser, opts: Opts = {}): () => void {
  sender = send;
  focuser = focus;
  afterReflow = opts.afterReflow ?? null;
  notify();
  return () => {
    if (sender === send) {
      sender = null;
      focuser = null;
      afterReflow = null;
      notify();
    }
  };
}

// hasInput reports whether any pane can take keys right now — the
// keyboard shows only then (a stream handshake finished).
export function hasInput(): boolean {
  return sender !== null;
}

export function onInputChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function notify(): void {
  listeners.forEach((fn) => fn());
}

// sendBytes delivers raw bytes to the live pane; a no-op when nothing
// is attached (the keyboard hides then, but a stale tap must not throw).
function sendBytes(bytes: Uint8Array): void {
  sender?.(bytes);
}

const encoder = new TextEncoder();

// sendText encodes and sends a control sequence (string of code units
// 0-255 like '\x03' or '\x1b[A'), with no Ctrl staging.
export function sendText(seq: string): void {
  const bytes = new Uint8Array(seq.length);
  for (let i = 0; i < seq.length; i++) {
    bytes[i] = seq.charCodeAt(i);
  }
  sendBytes(bytes);
}

// composeCtrl applies the Ctrl modifier to a single printable char,
// mirroring what the terminal does with a physical Ctrl held.
function composeCtrl(ch: string): string {
  if (ch.length !== 1) {
    return ch;
  }
  const code = ch.charCodeAt(0);
  if (code >= 97 && code <= 122) {
    return String.fromCharCode(code - 96); // a→0x01 … z→0x1a
  }
  if (ch === '[') {
    return '\x1b';
  }
  if (ch === ' ') {
    return '\x00';
  }
  if (ch === '/') {
    return '\x1f';
  }
  return ch;
}

// sendTyped carries one typed char (from the OS keyboard through
// xterm's onData, or a strip letter): Ctrl is applied, and a one-shot
// armed latch clears after this one char.
export function sendTyped(ch: string): void {
  if (ctrl === 'off') {
    sendText(ch);
    return;
  }
  const out = composeCtrl(ch);
  if (ctrl === 'armed') {
    setCtrl('off');
  }
  sendText(out);
}

// --- Ctrl modifier (component state mirror kept tiny) ---
export function ctrlState(): CtrlState {
  return ctrl;
}

export function onCtrlChange(fn: () => void): () => void {
  ctrlListeners.add(fn);
  return () => ctrlListeners.delete(fn);
}

export function setCtrl(next: CtrlState): void {
  if (next === ctrl) {
    return;
  }
  ctrl = next;
  ctrlListeners.forEach((fn) => fn());
}

// ctrlTap cycles a one-shot latch: tap to arm the next key, tap again
// to cancel. Held comes from a long-press in the keyboard component.
export function ctrlTap(): void {
  setCtrl(ctrl === 'off' ? 'armed' : 'off');
}

export function ctrlHoldStart(): void {
  setCtrl('held');
}

export function ctrlHoldEnd(): void {
  if (ctrl === 'held') {
    setCtrl('off');
  }
}

// tapFlip applies Ctrl to a strip char directly (no latch) for the
// chars the strip sends as control bytes (^C etc.).
export function sendCtrl(ch: string): void {
  sendText(composeCtrl(ch));
}

// focusFocused raises the OS keyboard on the live pane.
export function focusFocused(): void {
  focuser?.();
}

// afterReflowNow asks the live pane to pull its prompt to the last
// line — called after a container reflow (keyboard, rotation) so the
// cursor is where the eye is.
export function afterReflowNow(): void {
  afterReflow?.();
}

// sendClipboard reads the clipboard and pastes its text into the live
// pane (mobile has no Ctrl+Shift+V).
export async function sendClipboard(): Promise<boolean> {
  try {
    const text = await navigator.clipboard.readText();
    if (text === '') {
      return false;
    }
    sendBytes(encoder.encode(text));
    return true;
  } catch {
    return false;
  }
}

// The strip keys call send() with a control sequence as a plain string
// of code units 0-255 ('\x03', '\x1b[A').
export { sendText as send };
