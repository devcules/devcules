import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import TerminalView, { clampDims } from './TerminalView';
import { ctrlState, ctrlTap, onFsRequest, sendClipboard } from './keybus';

// xterm needs layout, which jsdom does not do: stand in a terminal
// that records writes and replays scripted input.
const writes: Uint8Array[] = [];
let dataHandler: ((data: string) => void) | null = null;
// What the strip's Paste handed to term.paste.
const pasted: string[] = [];
// What the fit addon proposes; a test can widen it.
let proposed = { cols: 80, rows: 24 };

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    options: Record<string, unknown> = {};
    scrollToBottom() {}
    loadAddon() {}
    onData(fn: (data: string) => void) {
      dataHandler = fn;
      return { dispose: vi.fn() };
    }
    open() {}
    write(data: Uint8Array) {
      writes.push(data);
    }
    focus() {}
    // Real xterm brackets the text (when the app enabled it) and turns
    // \n into \r, then emits it through onData.
    paste(text: string) {
      pasted.push(text);
      dataHandler?.(`\x1b[200~${text.replace(/\n/g, '\r')}\x1b[201~`);
    }
    dispose() {}
  },
}));

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
    proposeDimensions() {
      return proposed;
    }
    loadAddon() {}
  },
}));

// A scripted WebSocket: tests drive open/message/close by hand.
type Handler = ((ev: unknown) => void) | null;
class FakeSocket {
  static instances: FakeSocket[] = [];
  static OPEN = 1;
  url: string;
  binaryType = '';
  sent: Array<string | Uint8Array> = [];
  onopen: Handler = null;
  onmessage: Handler = null;
  onclose: Handler = null;
  readyState = 1;
  closedWith: number | null = null;
  constructor(url: string) {
    this.url = url;
    FakeSocket.instances.push(this);
  }
  send(data: string | Uint8Array) {
    this.sent.push(data);
  }
  close(code?: number) {
    this.readyState = 3;
    this.closedWith = code ?? 1005;
  }
}

beforeEach(() => {
  writes.length = 0;
  pasted.length = 0;
  dataHandler = null;
  proposed = { cols: 80, rows: 24 };
  FakeSocket.instances = [];
  vi.stubGlobal('WebSocket', FakeSocket);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.classList.remove('devcules-mobile');
});

function bytes(d: unknown): string {
  return Buffer.from(d as Uint8Array).toString();
}

function socket(): FakeSocket {
  const s = FakeSocket.instances[0];
  if (!s) {
    throw new Error('no socket opened');
  }
  return s;
}

describe('TerminalView', () => {
  it('opens a stream and writes output to the terminal', async () => {
    const user = userEvent.setup();
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    expect(s.url).toContain('/api/terminals/0123456789abcdef/stream?cols=80&rows=24');
    expect(s.binaryType).toBe('arraybuffer');

    s.onopen?.({});
    s.onmessage?.({ data: new TextEncoder().encode('hi-42') });
    expect(Buffer.from(writes[0]).toString()).toBe('hi-42');

    // Text frames are controls, never output.
    s.onmessage?.({ data: '{"type":"resize"}' });
    expect(writes).toHaveLength(1);

    // Keystrokes leave as binary frames, never text. (isView, not
    // instanceof: the encoder's realm differs from the test's.)
    dataHandler?.('echo hi\n');
    const last = s.sent[s.sent.length - 1];
    expect(ArrayBuffer.isView(last)).toBe(true);
    expect(Array.from(last as unknown as number[])).toEqual(Array.from(new TextEncoder().encode('echo hi\n')));

    // The custom strip: Tab lands as a control byte, not a focus move.
    await user.click(screen.getByRole('button', { name: 'Tab' }));
    expect(Array.from(s.sent[s.sent.length - 1] as unknown as number[])).toEqual([0x09]);
  });

  it('turns an armed Ctrl plus a letter into a control code', async () => {
    const user = userEvent.setup();
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    s.onopen?.({});
    await user.click(screen.getByRole('button', { name: 'Control (tap for one, hold to keep)' }));
    dataHandler?.('c');
    expect(Array.from(s.sent[s.sent.length - 1] as unknown as number[])).toEqual([0x03]);
  });

  it('shows runner-offline on 1012 and gone on 4404', async () => {
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    socket().onclose?.({ code: 1012 });
    expect(await screen.findByText(/Runner offline/)).toBeDefined();
  });

  it('reports a gone terminal on 4404', async () => {
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    socket().onclose?.({ code: 4404 });
    expect(await screen.findByText('This terminal is gone.')).toBeDefined();
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it('stops on a normal close: the shell exited or another device closed it', async () => {
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    socket().onclose?.({ code: 1000 });
    expect(await screen.findByText('This terminal is gone.')).toBeDefined();
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it('returns to sign-in when a failed reattach finds the session expired', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const expired = vi.fn();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(null, { status: 401 }))));
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} onSessionExpired={expired} />);
    socket().onclose?.({ code: 1006 });
    await vi.runOnlyPendingTimersAsync();
    const again = FakeSocket.instances[1];
    if (!again) {
      throw new Error('no reattach');
    }
    again.onclose?.({ code: 1006 });
    await vi.waitFor(() => expect(expired).toHaveBeenCalled());
    vi.useRealTimers();
  });
});

describe('TerminalView socket lifecycle', () => {
  it('keeps an idle pane open while text pings arrive, and still closes a silent one', () => {
    vi.useFakeTimers();
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    act(() => s.onopen?.({}));
    // Serve's application heartbeat: a text frame every 15 s on an idle
    // shell. Protocol pings never reach JS, so these are all it sees.
    for (let t = 0; t < 4; t++) {
      act(() => {
        vi.advanceTimersByTime(15000);
        s.onmessage?.({ data: '{"type":"ping"}' });
      });
    }
    expect(s.closedWith).toBe(null);
    expect(writes).toHaveLength(0);
    // A link that goes truly silent is still caught.
    act(() => vi.advanceTimersByTime(61000));
    expect(s.closedWith).toBe(4000);
  });

  it('opens one live socket when online fires while a reattach is pending', () => {
    vi.useFakeTimers();
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s0 = socket();
    act(() => s0.onopen?.({}));
    act(() => s0.onclose?.({ code: 1006 }));
    act(() => {
      window.dispatchEvent(new Event('online'));
    });
    act(() => vi.advanceTimersByTime(12000));
    const live = FakeSocket.instances.slice(1).filter((s) => s.readyState === 1);
    for (const s of live) {
      act(() => s.onopen?.({}));
    }
    act(() => {
      for (const s of live) {
        s.onmessage?.({ data: new TextEncoder().encode('X') });
      }
    });
    expect(live).toHaveLength(1);
    expect(writes).toHaveLength(1);
  });

  it('a late close from the previous pane leaves the new pane able to send', () => {
    const { rerender } = render(<TerminalView id="aaaaaaaaaaaaaaaa" onBack={vi.fn()} />);
    const a = socket();
    act(() => a.onopen?.({}));
    // Another tab: the same instance re-runs its effect for pane B.
    rerender(<TerminalView id="bbbbbbbbbbbbbbbb" onBack={vi.fn()} />);
    const b = FakeSocket.instances[1];
    expect(b.url).toContain('/api/terminals/bbbbbbbbbbbbbbbb/stream');
    expect(a.closedWith).toBe(1000);
    act(() => b.onopen?.({}));
    // A's close event lands after B is live, as a browser delivers it.
    act(() => a.onclose?.({ code: 1000 }));
    act(() => a.onmessage?.({ data: new TextEncoder().encode('from-a') }));
    dataHandler?.('x');
    expect(bytes(b.sent[b.sent.length - 1])).toBe('x');
    expect(writes).toHaveLength(0);
    expect(screen.queryByText('This terminal is gone.')).toBeNull();
    // And B still reconnects on its own drop: the pane is not user-closed.
    vi.useFakeTimers();
    act(() => b.onclose?.({ code: 1006 }));
    act(() => vi.advanceTimersByTime(12000));
    expect(FakeSocket.instances).toHaveLength(3);
    expect(FakeSocket.instances[2].url).toContain('bbbbbbbbbbbbbbbb');
  });

  it('a new onSessionExpired identity neither reconnects nor re-requests fullscreen', () => {
    document.body.classList.add('devcules-mobile');
    const fs: boolean[] = [];
    const off = onFsRequest((on) => fs.push(on));
    const { rerender } = render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} onSessionExpired={() => undefined} />);
    const s = socket();
    act(() => s.onopen?.({}));
    expect(fs).toEqual([true]);
    rerender(<TerminalView id="0123456789abcdef" onBack={vi.fn()} onSessionExpired={() => undefined} />);
    rerender(<TerminalView id="0123456789abcdef" onBack={vi.fn()} onSessionExpired={() => undefined} />);
    expect(FakeSocket.instances).toHaveLength(1);
    expect(s.closedWith).toBe(null);
    expect(fs).toEqual([true]);
    off();
  });

  it('calls the latest onSessionExpired after a re-render', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const first = vi.fn();
    const latest = vi.fn();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(null, { status: 401 }))));
    const { rerender } = render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} onSessionExpired={first} />);
    rerender(<TerminalView id="0123456789abcdef" onBack={vi.fn()} onSessionExpired={latest} />);
    act(() => socket().onclose?.({ code: 1006 }));
    await act(() => vi.runOnlyPendingTimersAsync());
    const again = FakeSocket.instances[1];
    act(() => again.onclose?.({ code: 1006 }));
    await vi.waitFor(() => expect(latest).toHaveBeenCalled());
    expect(first).not.toHaveBeenCalled();
  });

  it('clamps an oversized fit to the 500-col limit on open and on resize', () => {
    proposed = { cols: 900, rows: 700 };
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    expect(s.url).toContain('cols=500&rows=500');
    act(() => s.onopen?.({}));
    expect(JSON.parse(s.sent[0] as string)).toEqual({ type: 'resize', cols: 500, rows: 500 });
    expect(clampDims(0, 24.7)).toEqual({ cols: 1, rows: 24 });
  });
});

describe('TerminalView keybus registration (B9)', () => {
  it('the strip Paste goes through term.paste, so it arrives bracketed (MOB-05)', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText: () => Promise.resolve('echo pasted\nrm -rf x') } });
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    s.onopen?.({});
    await act(async () => {
      await sendClipboard();
    });
    expect(pasted).toEqual(['echo pasted\nrm -rf x']);
    expect(bytes(s.sent[s.sent.length - 1])).toBe('\x1b[200~echo pasted\rrm -rf x\x1b[201~');
  });

  it('a desktop paste is sanitised like the strip Paste before xterm sees it (check 3)', () => {
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    s.onopen?.({});
    const host = screen.getByLabelText('Terminal');
    // xterm's own listener sits on its textarea, inside the host.
    const inner = document.createElement('textarea');
    host.appendChild(inner);
    const xtermSaw = vi.fn();
    inner.addEventListener('paste', xtermSaw);
    const ev = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'clipboardData', {
      value: { getData: (t: string) => (t === 'text/plain' ? 'echo b\x03\necho X\x1b[201~\x1a' : '') },
    });
    inner.dispatchEvent(ev);
    expect(xtermSaw).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(true);
    expect(pasted).toEqual(['echo b\necho X[201~']);
    expect(bytes(s.sent[s.sent.length - 1])).toBe('\x1b[200~echo b\recho X[201~\x1b[201~');
  });

  it('closing the pane drops an armed Ctrl, so the next pane starts clean (MOB-03)', () => {
    const { unmount } = render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    socket().onopen?.({});
    ctrlTap();
    expect(ctrlState()).toBe('armed');
    unmount();
    expect(ctrlState()).toBe('off');
  });
});
