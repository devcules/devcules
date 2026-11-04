import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  FONT_MAX,
  FONT_MIN,
  afterReflowNow,
  composeCtrl,
  ctrlHoldEnd,
  ctrlHoldStart,
  ctrlState,
  ctrlTap,
  focusFocused,
  haptic,
  hasInput,
  onCtrlChange,
  onFsRequest,
  onInputChange,
  onTermFont,
  registerInput,
  requestFs,
  sendClipboard,
  sendCtrl,
  sendKey,
  sendText,
  sendTyped,
  setCtrl,
  stepTermFont,
  termFontSize,
} from './keybus';

// keybus is module state: every test registers its own pane and
// unregisters it (which also drops the Ctrl latch) afterwards.
let sent: number[][] = [];
let unregister: (() => void) | null = null;
const sender = (b: Uint8Array) => {
  sent.push(Array.from(b));
};

function codes(s: string): number[] {
  return Array.from(s, (c) => c.charCodeAt(0));
}

function setViewport(w: number, h: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: w });
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: h });
}

beforeEach(() => {
  sent = [];
  window.localStorage.clear();
  unregister = registerInput(sender, vi.fn());
});

afterEach(() => {
  unregister?.();
  unregister = null;
  setCtrl('off');
  setViewport(1024, 768);
  vi.unstubAllGlobals();
});

describe('termFontSize / stepTermFont', () => {
  it('defaults by the short edge, so both phone orientations get 12 (MOB-14)', () => {
    setViewport(390, 844);
    expect(termFontSize()).toBe(12);
    setViewport(844, 390);
    expect(termFontSize()).toBe(12);
    setViewport(1280, 800);
    expect(termFontSize()).toBe(14);
  });

  it('ignores a stored value outside 9..22 and clamps steps to the range', () => {
    setViewport(1280, 800);
    window.localStorage.setItem('devcules.termFont', '30');
    expect(termFontSize()).toBe(14);
    window.localStorage.setItem('devcules.termFont', String(FONT_MAX));
    expect(stepTermFont(1)).toBe(FONT_MAX);
    window.localStorage.setItem('devcules.termFont', String(FONT_MIN));
    expect(stepTermFont(-1)).toBe(FONT_MIN);
  });

  it('persists a step and tells the listeners', () => {
    setViewport(390, 844);
    const seen: number[] = [];
    const off = onTermFont((px) => seen.push(px));
    expect(stepTermFont(1)).toBe(13);
    expect(window.localStorage.getItem('devcules.termFont')).toBe('13');
    expect(termFontSize()).toBe(13);
    off();
    stepTermFont(1);
    expect(seen).toEqual([13]);
  });

  it('still applies a step when storage is blocked', () => {
    setViewport(390, 844);
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(termFontSize()).toBe(12);
    expect(stepTermFont(1)).toBe(13);
    get.mockRestore();
    set.mockRestore();
  });
});

describe('composeCtrl', () => {
  it.each([
    ['a', 0x01],
    ['c', 0x03],
    ['z', 0x1a],
    ['C', 0x03],
    ['L', 0x0c],
    ['@', 0x00],
    ['[', 0x1b],
    ['\\', 0x1c],
    [']', 0x1d],
    ['^', 0x1e],
    ['_', 0x1f],
    [' ', 0x00],
    ['/', 0x1f],
  ])('maps %j to code %i', (ch, code) => {
    expect(composeCtrl(ch)).toBe(String.fromCharCode(code));
  });

  it('leaves other chars and multi-char strings alone', () => {
    expect(composeCtrl('1')).toBe('1');
    expect(composeCtrl('|')).toBe('|');
    expect(composeCtrl('é')).toBe('é');
    expect(composeCtrl('ab')).toBe('ab');
  });
});

describe('Ctrl latch', () => {
  it('a one-shot latch transforms one typed char, then clears', () => {
    ctrlTap();
    expect(ctrlState()).toBe('armed');
    sendTyped('c');
    expect(ctrlState()).toBe('off');
    sendTyped('c');
    expect(sent).toEqual([[0x03], codes('c')]);
  });

  it('a held Ctrl transforms every char until released', () => {
    ctrlHoldStart();
    sendTyped('a');
    sendTyped('b');
    expect(ctrlState()).toBe('held');
    ctrlHoldEnd();
    sendTyped('a');
    expect(sent).toEqual([[0x01], [0x02], codes('a')]);
  });

  it('tapping again cancels an armed latch; hold-end leaves an armed one', () => {
    const changes = vi.fn();
    const off = onCtrlChange(changes);
    ctrlTap();
    ctrlHoldEnd();
    expect(ctrlState()).toBe('armed');
    ctrlTap();
    expect(ctrlState()).toBe('off');
    setCtrl('off'); // no-op: no change event
    off();
    expect(changes).toHaveBeenCalledTimes(2);
  });

  it('sendCtrl applies Ctrl without touching the latch', () => {
    sendCtrl('d');
    expect(sent).toEqual([[0x04]]);
    expect(ctrlState()).toBe('off');
  });
});

describe('sendKey (strip keys, MOB-09)', () => {
  it('sends a key unchanged when Ctrl is off', () => {
    expect(sendKey('\x1b[A')).toBe('\x1b[A');
    expect(sendKey('/')).toBe('/');
    expect(sent).toEqual([codes('\x1b[A'), codes('/')]);
  });

  it('an armed Ctrl modifies an arrow and is used up', () => {
    ctrlTap();
    expect(sendKey('\x1b[D')).toBe('\x1b[1;5D');
    expect(ctrlState()).toBe('off');
    sendTyped('l');
    expect(sent).toEqual([codes('\x1b[1;5D'), codes('l')]);
  });

  it('an armed Ctrl + a strip char goes through the latch (Ctrl+/ → 0x1f)', () => {
    ctrlTap();
    expect(sendKey('/')).toBe('\x1f');
    expect(ctrlState()).toBe('off');
    expect(sent).toEqual([[0x1f]]);
  });

  it('modifies Home/End and PgUp/PgDn, and a held Ctrl stays held', () => {
    ctrlHoldStart();
    sendKey('\x1b[H');
    sendKey('\x1b[F');
    sendKey('\x1b[5~');
    sendKey('\x1b[6~');
    expect(ctrlState()).toBe('held');
    expect(sent).toEqual([codes('\x1b[1;5H'), codes('\x1b[1;5F'), codes('\x1b[5;5~'), codes('\x1b[6;5~')]);
  });

  it('clears an armed Ctrl on Esc, Tab, Enter and the ^ keys', () => {
    for (const seq of ['\x1b', '\t', '\r', '\x03']) {
      ctrlTap();
      expect(sendKey(seq)).toBe(seq);
      expect(ctrlState()).toBe('off');
    }
  });

  it('passes an unknown multi-byte sequence through and still clears the latch', () => {
    ctrlTap();
    expect(sendKey('\x1bOP')).toBe('\x1bOP');
    expect(ctrlState()).toBe('off');
  });
});

describe('registerInput', () => {
  it('unregister drops the pane and resets Ctrl (MOB-03)', () => {
    const changed = vi.fn();
    const off = onInputChange(changed);
    ctrlHoldStart();
    expect(hasInput()).toBe(true);
    unregister?.();
    unregister = null;
    expect(hasInput()).toBe(false);
    expect(ctrlState()).toBe('off');
    expect(changed).toHaveBeenCalled();
    off();
    sendText('x'); // no pane: a stale tap must not throw
    expect(sent).toEqual([]);
  });

  it('a stale unregister after another pane took over changes nothing', () => {
    const other: number[][] = [];
    const offOther = registerInput((b) => other.push(Array.from(b)), vi.fn());
    ctrlTap();
    unregister?.(); // the first pane's cleanup, late
    unregister = null;
    expect(hasInput()).toBe(true);
    expect(ctrlState()).toBe('armed');
    sendText('q');
    expect(other).toEqual([codes('q')]);
    offOther();
  });

  it('forwards focus and afterReflow to the live pane', () => {
    unregister?.();
    const focus = vi.fn();
    const after = vi.fn();
    unregister = registerInput(sender, focus, { afterReflow: after });
    focusFocused();
    afterReflowNow();
    expect(focus).toHaveBeenCalledTimes(1);
    expect(after).toHaveBeenCalledTimes(1);
  });
});

describe('sendClipboard (MOB-05)', () => {
  function clipboard(read: () => Promise<string>) {
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText: read } });
  }

  it('pastes through the pane paste hook, not as raw bytes', async () => {
    unregister?.();
    const paste = vi.fn();
    unregister = registerInput(sender, vi.fn(), { paste });
    clipboard(() => Promise.resolve('echo pasted\nrm -rf x'));
    await expect(sendClipboard()).resolves.toBe(true);
    expect(paste).toHaveBeenCalledWith('echo pasted\nrm -rf x');
    expect(sent).toEqual([]);
  });

  it('defuses an embedded end-of-paste marker so the bracket cannot be closed early', async () => {
    unregister?.();
    const paste = vi.fn();
    unregister = registerInput(sender, vi.fn(), { paste });
    // The ESC goes; the rest of the marker is inert text inside the bracket.
    clipboard(() => Promise.resolve('echo a\x1b[201~\nrm -rf x\x1b[201~'));
    await expect(sendClipboard()).resolves.toBe(true);
    expect(paste).toHaveBeenCalledWith('echo a[201~\nrm -rf x[201~');
    // Nothing but controls left: nothing to paste.
    clipboard(() => Promise.resolve('\x1b\x9b\x1b'));
    await expect(sendClipboard()).resolves.toBe(false);
    expect(paste).toHaveBeenCalledTimes(1);
  });

  it('a nested end-of-paste marker cannot rebuild itself (check 2)', async () => {
    unregister?.();
    const paste = vi.fn();
    unregister = registerInput(sender, vi.fn(), { paste });
    const cases: [string, string][] = [
      ['echo b\x1b[20\x1b[201~1~\nrm -rf x', 'echo b[20[201~1~\nrm -rf x'],
      ['x\x1b[2\x1b[201~01~y', 'x[2[201~01~y'],
      ['x\x1b\x1b[201~[201~y', 'x[201~[201~y'],
      // 8-bit CSI (C1 U+009B) spells the same marker without an ESC.
      ['echo c\x9b201~\nrm -rf x', 'echo c201~\nrm -rf x'],
      ['a\x9b\x1b[201~201~b\x85\x90\x9f\x80c', 'a[201~201~bc'],
      // C0 job control raises a signal at the tty, below the bracket,
      // and the rest would run as typed input (check 3): ^C, ^Z, ^\.
      ['echo b\x03\necho X', 'echo b\necho X'],
      ['echo b\x1a\necho X', 'echo b\necho X'],
      ['echo b\x1c\necho X', 'echo b\necho X'],
      // XOFF would freeze the pane's output under IXON.
      ['echo b\x13\necho X', 'echo b\necho X'],
      // The checker's probe: every C0 but \t \n \r, and DEL, go.
      ['echo b\x03\necho X\x1a\x1c\x13\x0f\x16\x00\x04\x08\x0b\x0c\x7f\ty\r\n', 'echo b\necho X\ty\r\n'],
    ];
    for (const [clip, want] of cases) {
      clipboard(() => Promise.resolve(clip));
      await expect(sendClipboard()).resolves.toBe(true);
      const got = paste.mock.calls.at(-1)![0] as string;
      expect(got).toBe(want);
      expect(got).not.toMatch(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/);
    }
    // Ordinary non-ASCII text (U+00A0 and up) is kept.
    clipboard(() => Promise.resolve('café é 日本 \u00a0'));
    await sendClipboard();
    expect(paste).toHaveBeenLastCalledWith('café é 日本 \u00a0');
  });

  it('strips ESC and C1 on the raw-bytes fallback too', async () => {
    clipboard(() => Promise.resolve('a\x1b[20\x1b[201~1~\x9bb'));
    await expect(sendClipboard()).resolves.toBe(true);
    expect(sent).toEqual([codes('a[20[201~1~b')]);
  });

  it('strips C0 job-control bytes on the raw-bytes fallback too (check 3)', async () => {
    clipboard(() => Promise.resolve('echo b\x03\necho X\x1a\x1c\x13\t'));
    await expect(sendClipboard()).resolves.toBe(true);
    expect(sent).toEqual([codes('echo b\necho X\t')]);
  });

  it('uses up an armed Ctrl (not applied) and leaves a held one held', async () => {
    clipboard(() => Promise.resolve('ls'));
    ctrlTap();
    const p = sendClipboard();
    expect(ctrlState()).toBe('off');
    await expect(p).resolves.toBe(true);
    expect(sent).toEqual([codes('ls')]);
    ctrlHoldStart();
    await sendClipboard();
    expect(ctrlState()).toBe('held');
    ctrlHoldEnd();
  });

  it('falls back to raw bytes only when the pane has no paste hook', async () => {
    clipboard(() => Promise.resolve('ls'));
    await expect(sendClipboard()).resolves.toBe(true);
    expect(sent).toEqual([codes('ls')]);
  });

  it('reports an empty or blocked clipboard', async () => {
    clipboard(() => Promise.resolve(''));
    await expect(sendClipboard()).resolves.toBe(false);
    clipboard(() => Promise.reject(new Error('denied')));
    await expect(sendClipboard()).resolves.toBe(false);
    expect(sent).toEqual([]);
  });
});

describe('fullscreen requests and haptics', () => {
  it('fans a request out to every listener until it unsubscribes', () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = onFsRequest(a);
    const offB = onFsRequest(b);
    requestFs(true);
    offA();
    requestFs(false);
    offB();
    expect(a.mock.calls).toEqual([[true]]);
    expect(b.mock.calls).toEqual([[true], [false]]);
  });

  it('haptic vibrates when it can and never throws', () => {
    const vibrate = vi.fn();
    vi.stubGlobal('navigator', { ...navigator, vibrate });
    haptic();
    expect(vibrate).toHaveBeenCalledWith(6);
    vi.stubGlobal('navigator', {
      ...navigator,
      vibrate: () => {
        throw new Error('nope');
      },
    });
    expect(() => haptic()).not.toThrow();
  });
});
