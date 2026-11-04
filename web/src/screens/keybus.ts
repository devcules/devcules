// keybus is the wire between the one docked custom keyboard and
// whichever terminal pane currently owns a stream. Panes register a
// sender when their WebSocket opens and drop it when they unmount; the
// keyboard never holds a React ref or context to them. Ctrl is a
// module-level modifier (armed = next char, held = until released), so
// both the strip keys and the OS keyboard's typed letters can carry it.
//
// Module state, not component state, so the keyboard instantly
// rebinds to the new pane's input path. A pane that unregisters takes
// its Ctrl latch with it: the next pane starts with Ctrl off.

type Sender = (bytes: Uint8Array) => void;
type Focuser = () => void;
// Opts carries the per-pane extras the keyboard needs beyond raw
// bytes: scroll-to-bottom after a reflow, and the like.
// paste hands clipboard text to the pane's terminal (xterm's
// term.paste), so bracketed-paste mode and \n→\r apply exactly as
// they do for a desktop paste.
type Opts = { afterReflow?: () => void; paste?: (text: string) => void };

let sender: Sender | null = null;
let focuser: Focuser | null = null;
let afterReflow: (() => void) | null = null;
let paster: ((text: string) => void) | null = null;
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
  paster = opts.paste ?? null;
  notify();
  return () => {
    if (sender === send) {
      sender = null;
      focuser = null;
      afterReflow = null;
      paster = null;
      // A latch armed or held for the pane that is going away must not
      // turn the next pane's first letters into control codes.
      setCtrl('off');
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
export function composeCtrl(ch: string): string {
  if (ch.length !== 1) {
    return ch;
  }
  // Case does not matter to Ctrl: a shifted C is still ^C.
  const raw = ch.charCodeAt(0);
  const code = raw >= 0x61 && raw <= 0x7a ? raw - 0x20 : raw;
  if (code >= 0x40 && code <= 0x5f) {
    return String.fromCharCode(code - 0x40); // @→0x00, A→0x01 … Z→0x1a, [→ESC, \ ] ^ _→0x1c-0x1f
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

// ctrlCsi adds the Ctrl modifier to a cursor or editing key the way
// xterm does: ESC[A → ESC[1;5A, ESC[5~ → ESC[5;5~. Anything else is
// returned unchanged.
function ctrlCsi(seq: string): string {
  const letter = /^\x1b\[([A-DHF])$/.exec(seq);
  if (letter) {
    return `\x1b[1;5${letter[1]}`;
  }
  const tilde = /^\x1b\[(\d+)~$/.exec(seq);
  if (tilde) {
    return `\x1b[${tilde[1]};5~`;
  }
  return seq;
}

// sendKey is the strip's send: a single char goes through sendTyped
// (so Ctrl applies and a one-shot latch is used up), a cursor key gets
// the Ctrl modifier while Ctrl is on, and an armed latch clears after
// any key. It returns the sequence it sent, so a hold-repeat can keep
// sending the same bytes after the one-shot is gone.
export function sendKey(seq: string): string {
  if (seq.length === 1) {
    const out = ctrl === 'off' ? seq : composeCtrl(seq);
    sendTyped(seq);
    return out;
  }
  const out = ctrl === 'off' ? seq : ctrlCsi(seq);
  if (ctrl === 'armed') {
    setCtrl('off');
  }
  sendText(out);
  return out;
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

// stripPasteControls removes every C0 control except \t, \n and \r,
// plus DEL and every C1 control, from pasted text. ESC and C1 could
// start an escape sequence such as ESC[201~ (closing the bracket
// early); ^C, ^Z and ^\ raise a signal at the tty, below the shell's
// bracketed paste, so the rest of the paste runs as typed input; and
// XOFF freezes the pane. Pasted text has no legitimate use for any.
export function stripPasteControls(text: string): string {
  return text.replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/g, '');
}

// sendClipboard reads the clipboard and pastes its text into the live
// pane (mobile has no Ctrl+Shift+V). It goes through the pane's
// terminal paste, so a multi-line clipboard is bracketed and does not
// run line by line; raw bytes only when the pane gave no paste hook.
export async function sendClipboard(): Promise<boolean> {
  // Paste is a key: an armed one-shot Ctrl is used up by it (not
  // applied to the text). Cleared before the await, so a key typed
  // while the clipboard is read is not modified either.
  if (ctrl === 'armed') {
    setCtrl('off');
  }
  try {
    // An end-of-paste marker inside the text would close the bracket
    // early, and a ^C/^Z/^\ would abort it at the tty; either lets the
    // rest run as typed input. Stripping a marker is not enough (a
    // nested one rebuilds it), so drop the controls themselves: this
    // only removes characters, so it cannot build a new sequence.
    const text = stripPasteControls(await navigator.clipboard.readText());
    if (text === '') {
      return false;
    }
    if (paster) {
      paster(text);
    } else {
      sendBytes(encoder.encode(text));
    }
    return true;
  } catch {
    return false;
  }
}

// The strip keys call send() with a control sequence as a plain string
// of code units 0-255 ('\x03', '\x1b[A').
export { sendText as send };

// --- Terminal fullscreen + font size (mobile) ---
// The shell folds its chrome while a terminal owns the phone screen.
// A pane asks for that on mount; the strip's ☰ asks to leave it.
type FsListener = (on: boolean) => void;
const fsListeners = new Set<FsListener>();
export function onFsRequest(fn: FsListener): () => void {
  fsListeners.add(fn);
  return () => fsListeners.delete(fn);
}
export function requestFs(on: boolean): void {
  fsListeners.forEach((fn) => fn(on));
}

// Terminal font size is per browser: phones want it smaller than
// desktops, and people's eyes differ. A−/A+ on the strip step it.
export const FONT_MIN = 9;
export const FONT_MAX = 22;
const FONT_KEY = 'devcules.termFont';
const fontListeners = new Set<(px: number) => void>();
export function termFontSize(): number {
  try {
    const v = Number(window.localStorage.getItem(FONT_KEY));
    if (v >= FONT_MIN && v <= FONT_MAX) {
      return v;
    }
  } catch {
    // storage blocked: fall through to the default
  }
  // The phone's short edge, so the default does not depend on the
  // orientation the pane happened to open in.
  return typeof window !== 'undefined' && Math.min(window.innerWidth, window.innerHeight) <= 480 ? 12 : 14;
}
export function stepTermFont(delta: number): number {
  const next = Math.min(FONT_MAX, Math.max(FONT_MIN, termFontSize() + delta));
  try {
    window.localStorage.setItem(FONT_KEY, String(next));
  } catch {
    // not persisted; still applied for this session
  }
  fontListeners.forEach((fn) => fn(next));
  return next;
}
export function onTermFont(fn: (px: number) => void): () => void {
  fontListeners.add(fn);
  return () => fontListeners.delete(fn);
}

// haptic is a tiny tick on Android (iOS Safari ignores vibrate).
export function haptic(): void {
  try {
    navigator.vibrate?.(6);
  } catch {
    // unsupported
  }
}
