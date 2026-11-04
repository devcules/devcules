import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import TermKeyboard from './TermKeyboard';
import { ctrlState, onFsRequest, onTermFont, registerInput, setCtrl } from './keybus';

// The strip renders whenever a pane is registered on the keybus, so each
// test registers a recording pane and drives the keys through it.
let sent: string[] = [];
let unregister: (() => void) | null = null;
let focus = vi.fn();
let paste = vi.fn();

function str(b: Uint8Array): string {
  return String.fromCharCode(...Array.from(b));
}

beforeEach(() => {
  sent = [];
  focus = vi.fn();
  paste = vi.fn();
  window.localStorage.clear();
  unregister = registerInput((b) => sent.push(str(b)), focus, { paste });
});

afterEach(() => {
  unregister?.();
  unregister = null;
  setCtrl('off');
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// A quick tap: press and release without travel.
function tap(name: string) {
  const el = screen.getByRole('button', { name });
  fireEvent.pointerDown(el, { clientX: 5, clientY: 5 });
  fireEvent.pointerUp(el, { clientX: 5, clientY: 5 });
}

describe('TermKeyboard keys', () => {
  it.each([
    ['Escape', '\x1b'],
    ['Tab', '\t'],
    ['Left arrow', '\x1b[D'],
    ['Up arrow', '\x1b[A'],
    ['Down arrow', '\x1b[B'],
    ['Right arrow', '\x1b[C'],
    ['Ctrl-C', '\x03'],
    ['Ctrl-D (end of input)', '\x04'],
    ['Ctrl-R (history search)', '\x12'],
    ['Ctrl-Z (suspend)', '\x1a'],
    ['Ctrl-L (clear)', '\x0c'],
    ['Pipe', '|'],
    ['Slash', '/'],
    ['Tilde', '~'],
    ['Dash', '-'],
    ['Home', '\x1b[H'],
    ['End', '\x1b[F'],
    ['Page up', '\x1b[5~'],
    ['Page down', '\x1b[6~'],
  ])('%s sends its bytes', (name, seq) => {
    render(<TermKeyboard />);
    tap(name);
    expect(sent).toEqual([seq]);
  });

  it('Enter sends a carriage return', async () => {
    render(<TermKeyboard />);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Enter' }));
    expect(sent).toEqual(['\r']);
  });

  it('A− and A+ step the terminal font', () => {
    const seen: number[] = [];
    const off = onTermFont((px) => seen.push(px));
    window.localStorage.setItem('devcules.termFont', '14');
    render(<TermKeyboard />);
    tap('Larger terminal text');
    tap('Smaller terminal text');
    tap('Smaller terminal text');
    off();
    expect(seen).toEqual([15, 14, 13]);
    expect(sent).toEqual([]);
  });

  it('☰ asks to leave fullscreen, or calls onShowTabs when given', async () => {
    const user = userEvent.setup();
    const fs = vi.fn();
    const off = onFsRequest(fs);
    const { unmount } = render(<TermKeyboard />);
    await user.click(screen.getByRole('button', { name: 'Show tabs' }));
    expect(fs).toHaveBeenCalledWith(false);
    unmount();
    const onShowTabs = vi.fn();
    render(<TermKeyboard onShowTabs={onShowTabs} />);
    await user.click(screen.getByRole('button', { name: 'Show tabs' }));
    off();
    expect(onShowTabs).toHaveBeenCalledTimes(1);
    expect(fs).toHaveBeenCalledTimes(1);
  });

  it('⌨ raises the software keyboard on the live pane', async () => {
    render(<TermKeyboard />);
    await userEvent.setup().click(screen.getByRole('button', { name: 'Raise the software keyboard' }));
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it('Paste goes through the pane paste hook (MOB-05)', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText: () => Promise.resolve('echo a\nrm -rf x') } });
    render(<TermKeyboard />);
    tap('Paste from clipboard');
    await act(async () => {});
    expect(paste).toHaveBeenCalledWith('echo a\nrm -rf x');
    expect(sent).toEqual([]);
  });

  it('hides when no pane is registered (desktop/jsdom)', () => {
    unregister?.();
    unregister = null;
    const { container } = render(<TermKeyboard />);
    expect(container.innerHTML).toBe('');
  });
});

describe('TermKeyboard Ctrl (MOB-09)', () => {
  it('a tap arms Ctrl; an arrow then sends Ctrl+arrow and disarms', () => {
    render(<TermKeyboard />);
    const ctrl = screen.getByRole('button', { name: /^Control/ });
    tap(ctrl.getAttribute('aria-label')!);
    expect(ctrl.getAttribute('aria-pressed')).toBe('true');
    tap('Left arrow');
    expect(sent).toEqual(['\x1b[1;5D']);
    expect(ctrl.getAttribute('aria-pressed')).toBe('false');
  });

  it('armed Ctrl + Slash sends 0x1f and disarms; Enter also disarms', () => {
    render(<TermKeyboard />);
    const name = screen.getByRole('button', { name: /^Control/ }).getAttribute('aria-label')!;
    tap(name);
    tap('Slash');
    expect(ctrlState()).toBe('off');
    tap(name);
    fireEvent.click(screen.getByRole('button', { name: 'Enter' }));
    expect(ctrlState()).toBe('off');
    expect(sent).toEqual(['\x1f', '\r']);
  });

  it('a long press holds Ctrl until release', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const ctrl = screen.getByRole('button', { name: /^Control/ });
    fireEvent.pointerDown(ctrl, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(350);
    });
    expect(ctrlState()).toBe('held');
    fireEvent.pointerUp(ctrl, { clientX: 0, clientY: 0 });
    expect(ctrlState()).toBe('off');
  });

  it('a held arrow with an armed Ctrl repeats the Ctrl sequence', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    tap(screen.getByRole('button', { name: /^Control/ }).getAttribute('aria-label')!);
    const up = screen.getByRole('button', { name: 'Up arrow' });
    fireEvent.pointerDown(up, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(300 + 70 * 2);
    });
    fireEvent.pointerUp(up, { clientX: 0, clientY: 0 });
    expect(sent).toEqual(['\x1b[1;5A', '\x1b[1;5A', '\x1b[1;5A']);
    expect(ctrlState()).toBe('off');
  });
});

describe('TermKeyboard gestures and lifecycle', () => {
  it('a hold repeats, and release after a hold sends nothing extra', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const up = screen.getByRole('button', { name: 'Up arrow' });
    fireEvent.pointerDown(up, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(300 + 70 * 3);
    });
    fireEvent.pointerUp(up, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(sent).toEqual(['\x1b[A', '\x1b[A', '\x1b[A', '\x1b[A']);
  });

  it('a swipe that starts on a key scrolls and fires nothing', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const left = screen.getByRole('button', { name: 'Left arrow' });
    fireEvent.pointerDown(left, { clientX: 100, clientY: 5 });
    fireEvent.pointerMove(left, { clientX: 140, clientY: 5 });
    fireEvent.pointerUp(left, { clientX: 140, clientY: 5 });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(sent).toEqual([]);
  });

  it('a swipe or pointercancel releases a held Ctrl', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const ctrl = screen.getByRole('button', { name: /^Control/ });
    fireEvent.pointerDown(ctrl, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(350);
    });
    fireEvent.pointerMove(ctrl, { clientX: 40, clientY: 0 });
    expect(ctrlState()).toBe('off');
    fireEvent.pointerDown(ctrl, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(350);
    });
    expect(ctrlState()).toBe('held');
    fireEvent.pointerCancel(ctrl);
    expect(ctrlState()).toBe('off');
  });

  it('unmounting mid-hold stops the repeat (MOB-03)', () => {
    vi.useFakeTimers();
    const { unmount } = render(<TermKeyboard />);
    const up = screen.getByRole('button', { name: 'Up arrow' });
    fireEvent.pointerDown(up, { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(300 + 70 * 2);
    });
    const before = sent.length;
    expect(before).toBe(3);
    unmount();
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(sent.length).toBe(before);
  });

  it('unmounting before the hold starts cancels it (MOB-03)', () => {
    vi.useFakeTimers();
    const { unmount } = render(<TermKeyboard />);
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Down arrow' }), { clientX: 0, clientY: 0 });
    unmount();
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(sent).toEqual([]);
  });

  it('unmounting while Ctrl is held releases it (MOB-03)', () => {
    vi.useFakeTimers();
    const { unmount } = render(<TermKeyboard />);
    fireEvent.pointerDown(screen.getByRole('button', { name: /^Control/ }), { clientX: 0, clientY: 0 });
    act(() => {
      vi.advanceTimersByTime(350);
    });
    expect(ctrlState()).toBe('held');
    unmount();
    expect(ctrlState()).toBe('off');
  });

  it('keys cancel the mousedown so focus stays on the terminal (MOB-04)', () => {
    render(<TermKeyboard />);
    for (const name of ['Escape', 'Left arrow', 'Paste from clipboard', 'Hide keyboard strip', 'Raise the software keyboard']) {
      const el = screen.getByRole('button', { name });
      // fireEvent returns false when a handler called preventDefault.
      expect(fireEvent.mouseDown(el), name).toBe(false);
    }
    // The pinned Enter and ☰ cancel pointerdown (they never scroll),
    // which also suppresses the compatibility mousedown.
    expect(fireEvent.pointerDown(screen.getByRole('button', { name: 'Enter' }))).toBe(false);
    expect(fireEvent.pointerDown(screen.getByRole('button', { name: 'Show tabs' }))).toBe(false);
    // pointerdown is left alone on the scrolling keys (native pan).
    expect(fireEvent.pointerDown(screen.getByRole('button', { name: 'Escape' }))).toBe(true);
  });

  it('the collapsed row keeps ☰ and Enter, and expands again (MOB-10)', async () => {
    const user = userEvent.setup();
    const fs = vi.fn();
    const off = onFsRequest(fs);
    render(<TermKeyboard />);
    await user.click(screen.getByRole('button', { name: 'Hide keyboard strip' }));
    expect(screen.queryByRole('button', { name: 'Escape' })).toBeNull();
    const names = screen.getAllByRole('button').map((b) => b.getAttribute('aria-label'));
    expect(names).toEqual(['Show keyboard strip', 'Enter', 'Show tabs']);
    const chip = screen.getByRole('button', { name: 'Show keyboard strip' });
    expect(fireEvent.mouseDown(chip)).toBe(false);
    await user.click(screen.getByRole('button', { name: 'Enter' }));
    await user.click(screen.getByRole('button', { name: 'Show tabs' }));
    expect(sent).toEqual(['\r']);
    expect(fs).toHaveBeenCalledWith(false);
    await user.click(chip);
    expect(screen.getByRole('button', { name: 'Escape' })).toBeTruthy();
    off();
  });
});

// Ported from the round-1 checker probes (chk-b9/probe-*.test.tsx):
// a second pointer or a lost release must not orphan a hold timer.
describe('TermKeyboard multi-pointer and lost release (MOB-03, check 1)', () => {
  it('two pointers on one arrow: the repeat stops after both lift and unmount', () => {
    vi.useFakeTimers();
    const { unmount } = render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Left arrow' });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 5, clientY: 5 });
    act(() => vi.advanceTimersByTime(100));
    fireEvent.pointerDown(el, { pointerId: 2, clientX: 6, clientY: 5 });
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerUp(el, { pointerId: 1, clientX: 5, clientY: 5 });
    fireEvent.pointerUp(el, { pointerId: 2, clientX: 6, clientY: 5 });
    const n = sent.length;
    act(() => vi.advanceTimersByTime(1000));
    expect(sent.length - n).toBe(0);
    unmount();
    act(() => vi.advanceTimersByTime(1000));
    expect(sent.length - n).toBe(0);
  });

  it('two pointers on Ctrl: with no finger down, Ctrl is not held', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: /^Control/ });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 5, clientY: 5 });
    act(() => vi.advanceTimersByTime(100));
    fireEvent.pointerDown(el, { pointerId: 2, clientX: 6, clientY: 5 });
    act(() => vi.advanceTimersByTime(50));
    fireEvent.pointerUp(el, { pointerId: 1, clientX: 5, clientY: 5 });
    fireEvent.pointerUp(el, { pointerId: 2, clientX: 6, clientY: 5 });
    act(() => vi.advanceTimersByTime(1000));
    expect(ctrlState()).not.toBe('held');
  });

  it('a press whose release was lost is cleared by the next press', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Up arrow' });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    act(() => vi.advanceTimersByTime(500)); // repeating; its up never arrives
    fireEvent.pointerDown(el, { pointerId: 2, clientX: 0, clientY: 0 });
    fireEvent.pointerUp(el, { pointerId: 2, clientX: 0, clientY: 0 });
    const n = sent.length;
    act(() => vi.advanceTimersByTime(1000));
    expect(sent.length).toBe(n);
  });

  it.each(['pointerLeave', 'lostPointerCapture', 'pointerCancel'] as const)(
    '%s stops a repeat and fires nothing more',
    (ev) => {
      vi.useFakeTimers();
      render(<TermKeyboard />);
      const el = screen.getByRole('button', { name: 'Left arrow' });
      fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
      act(() => vi.advanceTimersByTime(300 + 70));
      expect(sent).toEqual(['\x1b[D', '\x1b[D']);
      fireEvent[ev](el, { pointerId: 1 });
      // The release may never reach the key (a mouse let go elsewhere).
      act(() => vi.advanceTimersByTime(500));
      expect(sent).toEqual(['\x1b[D', '\x1b[D']);
      fireEvent.pointerUp(el, { pointerId: 1, clientX: 0, clientY: 0 });
      act(() => vi.advanceTimersByTime(1000));
      expect(sent).toEqual(['\x1b[D', '\x1b[D']);
    },
  );

  it.each(['pointerLeave', 'lostPointerCapture'] as const)('%s releases a held Ctrl and cancels a pending hold', (ev) => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: /^Control/ });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    act(() => vi.advanceTimersByTime(350));
    expect(ctrlState()).toBe('held');
    fireEvent[ev](el, { pointerId: 1 });
    expect(ctrlState()).toBe('off');
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    fireEvent[ev](el, { pointerId: 1 });
    act(() => vi.advanceTimersByTime(1000));
    expect(ctrlState()).toBe('off');
  });

  it('a pointerleave from another pointer leaves the tracked press alone', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Left arrow' });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    fireEvent.pointerLeave(el, { pointerId: 7 });
    fireEvent.pointerUp(el, { pointerId: 1, clientX: 0, clientY: 0 });
    expect(sent).toEqual(['\x1b[D']);
  });
});

describe('TermKeyboard non-pointer activation (a11y, check 1)', () => {
  it('a click with no pointer press (screen reader) fires the key', () => {
    render(<TermKeyboard />);
    fireEvent.click(screen.getByRole('button', { name: 'Escape' }));
    fireEvent.click(screen.getByRole('button', { name: 'Left arrow' }));
    expect(sent).toEqual(['\x1b', '\x1b[D']);
  });

  it('keyboard Enter and Space activate a focused strip key', async () => {
    const user = userEvent.setup();
    render(<TermKeyboard />);
    screen.getByRole('button', { name: 'Tab' }).focus();
    await user.keyboard('{Enter}');
    screen.getByRole('button', { name: 'Pipe' }).focus();
    await user.keyboard(' ');
    expect(sent).toEqual(['\t', '|']);
  });

  it('keyboard activation drives Ctrl, font and Paste too', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText: () => Promise.resolve('x') } });
    const seen: number[] = [];
    const off = onTermFont((px) => seen.push(px));
    window.localStorage.setItem('devcules.termFont', '14');
    render(<TermKeyboard />);
    fireEvent.click(screen.getByRole('button', { name: /^Control/ }));
    expect(ctrlState()).toBe('armed');
    fireEvent.click(screen.getByRole('button', { name: 'Larger terminal text' }));
    expect(seen).toEqual([15]);
    fireEvent.click(screen.getByRole('button', { name: 'Paste from clipboard' }));
    await act(async () => {});
    expect(paste).toHaveBeenCalledWith('x');
    off();
  });

  it('a pointer tap and its trailing click fire once', async () => {
    const user = userEvent.setup();
    render(<TermKeyboard />);
    await user.click(screen.getByRole('button', { name: 'Escape' }));
    const el = screen.getByRole('button', { name: 'Down arrow' });
    fireEvent.pointerDown(el, { clientX: 0, clientY: 0 });
    fireEvent.pointerUp(el, { clientX: 0, clientY: 0 });
    fireEvent.click(el, { detail: 1 });
    // Ctrl too: a tap plus its click must arm, not arm-then-cancel.
    await user.click(screen.getByRole('button', { name: /^Control/ }));
    expect(sent).toEqual(['\x1b', '\x1b[B']);
    expect(ctrlState()).toBe('armed');
  });

  it('the click after a hold-repeat release sends nothing extra', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Up arrow' });
    fireEvent.pointerDown(el, { clientX: 0, clientY: 0 });
    act(() => vi.advanceTimersByTime(300 + 70));
    fireEvent.pointerUp(el, { clientX: 0, clientY: 0 });
    fireEvent.click(el, { detail: 1 });
    expect(sent).toEqual(['\x1b[A', '\x1b[A']);
  });
});

describe('TermKeyboard owed click (check 2)', () => {
  it('a trailing click after a stall over 1 s still fires once', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Escape' });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    fireEvent.pointerUp(el, { pointerId: 1, clientX: 0, clientY: 0 });
    act(() => vi.advanceTimersByTime(5000));
    fireEvent.click(el, { detail: 1 });
    expect(sent).toEqual(['\x1b']);
  });

  it('a keyboard click (detail 0) right after a pointer tap is not dropped', () => {
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Tab' });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    fireEvent.pointerUp(el, { pointerId: 1, clientX: 0, clientY: 0 });
    fireEvent.click(el, { detail: 1 }); // the tap's own click
    fireEvent.click(el, { detail: 0 }); // keyboard Enter straight after
    expect(sent).toEqual(['\t', '\t']);
  });

  it('a press with no click (swipe) does not swallow later activation', () => {
    render(<TermKeyboard />);
    const el = screen.getByRole('button', { name: 'Pipe' });
    fireEvent.pointerDown(el, { pointerId: 1, clientX: 0, clientY: 0 });
    fireEvent.pointerCancel(el, { pointerId: 1 });
    fireEvent.click(el, { detail: 0 });
    expect(sent).toEqual(['|']);
    // The next tap still fires once with its click.
    fireEvent.pointerDown(el, { pointerId: 2, clientX: 0, clientY: 0 });
    fireEvent.pointerUp(el, { pointerId: 2, clientX: 0, clientY: 0 });
    fireEvent.click(el, { detail: 1 });
    expect(sent).toEqual(['|', '|']);
  });
});

describe('TermKeyboard Ctrl edge cases (check 1)', () => {
  it('releasing a held Ctrl mid-repeat switches the repeat to the plain arrow', () => {
    vi.useFakeTimers();
    render(<TermKeyboard />);
    const ctrl = screen.getByRole('button', { name: /^Control/ });
    const left = screen.getByRole('button', { name: 'Left arrow' });
    fireEvent.pointerDown(ctrl, { pointerId: 1, clientX: 0, clientY: 0 });
    act(() => vi.advanceTimersByTime(350));
    expect(ctrlState()).toBe('held');
    fireEvent.pointerDown(left, { pointerId: 2, clientX: 0, clientY: 0 });
    act(() => vi.advanceTimersByTime(300 + 70));
    expect(sent).toEqual(['\x1b[1;5D', '\x1b[1;5D']);
    fireEvent.pointerUp(ctrl, { pointerId: 1, clientX: 0, clientY: 0 });
    expect(ctrlState()).toBe('off');
    act(() => vi.advanceTimersByTime(70 * 2));
    fireEvent.pointerUp(left, { pointerId: 2, clientX: 0, clientY: 0 });
    expect(sent).toEqual(['\x1b[1;5D', '\x1b[1;5D', '\x1b[D', '\x1b[D']);
  });

  it('Paste uses up an armed Ctrl without applying it', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText: () => Promise.resolve('ls') } });
    render(<TermKeyboard />);
    tap(screen.getByRole('button', { name: /^Control/ }).getAttribute('aria-label')!);
    expect(ctrlState()).toBe('armed');
    tap('Paste from clipboard');
    expect(ctrlState()).toBe('off');
    await act(async () => {});
    expect(paste).toHaveBeenCalledWith('ls');
  });
});
