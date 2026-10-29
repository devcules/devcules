import { useCallback, useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
// Without xterm's own stylesheet its measuring span and input textarea
// show up as stray glyphs above the prompt. Vite bundles it into our
// stylesheet, so the CSP needs nothing new for it.
import '@xterm/xterm/css/xterm.css';
import KeyStrip, { ctrlLetter, stripBytes, type StripKey } from './KeyStrip';
import { streamURL, type Where } from '../api';

type Status = 'connecting' | 'open' | 'reconnecting' | 'offline' | 'gone';

// reconnectDelay backs off per attempt (1s, 2s, 4s… capped at 10s)
// with a little jitter so two tabs never march together.
export function reconnectDelay(attempt: number): number {
  const capped = Math.min(1000 * 2 ** attempt, 10000);
  return capped + Math.random() * 500;
}

export default function TerminalView({
  id,
  where,
  onBack,
  onSessionExpired,
}: {
  id: string;
  // where is the link the terminal lives on: a machine's shell
  // streams with ?machine=, a runner's with nothing.
  where?: Where;
  onBack: () => void;
  onSessionExpired?: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const userClosedRef = useRef(false);
  const attemptRef = useRef(0);
  const timerRef = useRef<number | null>(null);
  const connectRef = useRef<() => void>(() => undefined);
  const ctrlRef = useRef(false);
  // The machine name, not the object: a string keeps connect stable
  // across renders that pass a fresh {machine} literal.
  const machine = where?.machine;
  const [ctrlArmed, setCtrlArmed] = useState(false);
  const [status, setStatus] = useState<Status>('connecting');

  const send = useCallback((bytes: ArrayLike<number>) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(bytes instanceof Uint8Array ? bytes : Uint8Array.from(bytes));
    }
  }, []);

  // connect opens one browser stream. Every close the user did not
  // ask for schedules a reattach: tmux redraws on attach, so nothing
  // is lost. 1012 means the runner is gone (wait longer); 1013 means
  // we were too slow (reattach at once); 1000 (the shell exited or
  // another device closed it) and 4404 mean the terminal itself is
  // gone (stop and say so). A socket that never opened may be an
  // expired session: a browser cannot see the 401, so ask /auth/me.
  const connect = useCallback(() => {
    const host = hostRef.current;
    const term = termRef.current;
    const fit = fitRef.current;
    if (!host || !term || !fit) {
      return;
    }
    let dims = { cols: 80, rows: 24 };
    try {
      const proposed = fit.proposeDimensions();
      if (proposed && proposed.cols > 0 && proposed.rows > 0) {
        dims = { cols: proposed.cols, rows: proposed.rows };
      }
    } catch {
      // proposeDimensions needs layout; fall back to 80x24.
    }
    const ws = new WebSocket(streamURL(id, dims.cols, dims.rows, machine ? { machine } : undefined));
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;
    ws.onopen = () => {
      attemptRef.current = 0;
      setStatus('open');
      try {
        fit.fit();
        const sized = fit.proposeDimensions();
        if (sized) {
          ws.send(JSON.stringify({ type: 'resize', cols: sized.cols, rows: sized.rows }));
        }
      } catch {
        // The first paint arrived at the query size already.
      }
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === 'string') {
        return;
      }
      const bytes = ev.data instanceof ArrayBuffer ? new Uint8Array(ev.data) : new Uint8Array(ev.data as ArrayBuffer);
      term.write(bytes);
    };
    ws.onclose = (ev: CloseEvent) => {
      wsRef.current = null;
      if (userClosedRef.current) {
        return;
      }
      if (ev.code === 4404 || ev.code === 1000) {
        setStatus('gone');
        return;
      }
      if (ev.code === 1006 && attemptRef.current > 0 && onSessionExpired) {
        // Only a 401 means signed out: a 5xx is serve restarting.
        void fetch('/auth/me').then(
          (res) => {
            if (res.status === 401 && !userClosedRef.current) {
              onSessionExpired();
            }
          },
          () => undefined,
        );
      }
      if (ev.code === 1012) {
        setStatus('offline');
      } else {
        setStatus('reconnecting');
      }
      const wait = ev.code === 1013 ? 0 : reconnectDelay(attemptRef.current);
      attemptRef.current += 1;
      // Through the ref: the timeout outlives this render's connect.
      timerRef.current = window.setTimeout(() => {
        if (!userClosedRef.current) {
          connectRef.current();
        }
      }, wait);
    };
  }, [id, machine, onSessionExpired]);

  useEffect(() => {
    connectRef.current = connect;
  }, [connect]);

  // One xterm per mount. onData forwards keystrokes as binary frames
  // (UTF-8 bytes, never text: the server reads text frames as resize
  // controls). An armed Ctrl turns the next typed letter into its
  // control code, then disarms.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) {
      return;
    }
    const term = new Terminal({ cursorBlink: true, fontSize: 14 });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    termRef.current = term;
    fitRef.current = fit;
    const disposeData = term.onData((data: string) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        return;
      }
      if (ctrlRef.current) {
        const code = ctrlLetter(data);
        ctrlRef.current = false;
        setCtrlArmed(false);
        if (code !== null) {
          ws.send(new Uint8Array([code]));
          return;
        }
      }
      ws.send(new TextEncoder().encode(data));
    });
    connect();
    const onVisible = () => {
      if (document.visibilityState === 'visible' && !userClosedRef.current && !wsRef.current) {
        connect();
      }
    };
    const onOnline = () => {
      if (!userClosedRef.current && !wsRef.current) {
        connect();
      }
    };
    // Locking the phone fires visibilitychange; coming back
    // reattaches at once instead of waiting out the backoff.
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('online', onOnline);
    return () => {
      userClosedRef.current = true;
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
      }
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('online', onOnline);
      try {
        wsRef.current?.close(1000, 'leaving');
      } catch {
        // Already gone.
      }
      disposeData.dispose();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [connect]);

  // Back to the terminal after every strip tap: the next typed key
  // must reach xterm, and on a phone a lost focus closes the keyboard.
  function toggleCtrl(): void {
    ctrlRef.current = !ctrlRef.current;
    setCtrlArmed(ctrlRef.current);
    termRef.current?.focus();
  }

  function press(key: StripKey): void {
    const { bytes } = stripBytes(key, ctrlRef.current);
    ctrlRef.current = false;
    setCtrlArmed(false);
    send(bytes);
    termRef.current?.focus();
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-11 items-center gap-2 px-4 py-2">
        <button
          type="button"
          aria-label="Back to terminals"
          onClick={onBack}
          className="min-h-11 min-w-11 rounded-md px-3 text-body text-text"
        >
          ‹
        </button>
        <span className="truncate font-mono text-body text-muted" aria-label="Terminal id">
          {id.slice(0, 8)}
        </span>
        {status !== 'open' && (
          <span role="status" className="text-body text-muted">
            {status === 'connecting' && 'Connecting…'}
            {status === 'reconnecting' && 'Reconnecting…'}
            {status === 'offline' && 'Runner offline. Waiting for it to come back…'}
            {status === 'gone' && 'This terminal is gone.'}
          </span>
        )}
      </div>
      <div ref={hostRef} className="min-h-0 flex-1 px-2" aria-label="Terminal" role="application" />
      <div className="h-16" aria-hidden="true" />
      <KeyStrip ctrlArmed={ctrlArmed} onToggleCtrl={toggleCtrl} onKey={press} />
    </div>
  );
}
