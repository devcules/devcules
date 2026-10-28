import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import TerminalView from './TerminalView';

// xterm needs layout, which jsdom does not do: stand in a terminal
// that records writes and replays scripted input.
const writes: Uint8Array[] = [];
let dataHandler: ((data: string) => void) | null = null;

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
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
    dispose() {}
  },
}));

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
    proposeDimensions() {
      return { cols: 80, rows: 24 };
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
  constructor(url: string) {
    this.url = url;
    FakeSocket.instances.push(this);
  }
  send(data: string | Uint8Array) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
  }
}

beforeEach(() => {
  writes.length = 0;
  dataHandler = null;
  FakeSocket.instances = [];
  vi.stubGlobal('WebSocket', FakeSocket);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

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

    // The key strip sends Enter and comes back for more.
    await user.click(screen.getByRole('button', { name: 'Enter' }));
    expect(Array.from(s.sent[s.sent.length - 1] as unknown as number[])).toEqual([0x0d]);
  });

  it('turns an armed Ctrl plus a letter into a control code', async () => {
    const user = userEvent.setup();
    render(<TerminalView id="0123456789abcdef" onBack={vi.fn()} />);
    const s = socket();
    s.onopen?.({});
    await user.click(screen.getByRole('button', { name: 'Control (sticky modifier)' }));
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
