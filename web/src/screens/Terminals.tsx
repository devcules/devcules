import { useCallback, useEffect, useState } from 'react';
import {
  closeTerminal,
  listTerminals,
  openTerminal,
  type TerminalError,
  type TerminalSummary,
} from '../api';
import TerminalView from './TerminalView';
import { Empty } from './Empty';

type State =
  | { view: 'loading' }
  | { view: 'list'; terminals: TerminalSummary[]; offline: boolean; notice: string | null }
  | { view: 'open'; id: string };

// errorWords turns API outcomes into plain words. 'unauthorized'
// never lands here: callers route it to sign-in first.
function errorWords(err: TerminalError | 'busy'): string {
  if (err === 'offline') {
    return 'No runner is linked. Start the runner and try again.';
  }
  if (err === 'busy') {
    return 'The runner did not answer. Try again.';
  }
  return 'Something went wrong. Try again.';
}

export default function Terminals({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [state, setState] = useState<State>({ view: 'loading' });
  const [confirmClose, setConfirmClose] = useState<string | null>(null);

  // Promise chains, not await: the session and list states resolve
  // off the effect's synchronous path, the way App.check does.
  const refresh = useCallback((): Promise<void> => {
    return listTerminals().then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        if (res === 'offline') {
          setState({ view: 'list', terminals: [], offline: true, notice: null });
          return;
        }
        if (res === 'unreachable') {
          setState({ view: 'list', terminals: [], offline: false, notice: errorWords(res) });
          return;
        }
        setState({ view: 'list', terminals: res.terminals, offline: false, notice: null });
      },
      () => {
        setState({ view: 'list', terminals: [], offline: false, notice: errorWords('unreachable') });
      },
    );
  }, [onSessionExpired]);

  // Re-check on return: locking the phone pauses the socket, and the
  // list may have changed while we were away.
  useEffect(() => {
    void refresh();
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        void refresh();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('online', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('online', onVisible);
    };
  }, [refresh]);

  async function create(): Promise<void> {
    const res = await openTerminal();
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res !== 'object') {
      setState({ view: 'list', terminals: [], offline: res === 'offline', notice: errorWords(res) });
      return;
    }
    setState({ view: 'open', id: res.id });
  }

  async function remove(id: string): Promise<void> {
    const res = await closeTerminal(id);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    setConfirmClose(null);
    if (state.view === 'open') {
      setState({ view: 'loading' });
    }
    await refresh();
  }

  if (state.view === 'loading') {
    return <div className="flex flex-1 items-center justify-center" aria-busy="true" />;
  }

  if (state.view === 'open') {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <TerminalView id={state.id} onSessionExpired={onSessionExpired} onBack={() => { setState({ view: 'loading' }); void refresh(); }} />
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lead font-semibold text-text">Terminals</h2>
        <button
          type="button"
          onClick={() => void create()}
          className="min-h-11 rounded-md bg-accent px-4 text-body text-base"
        >
          New terminal
        </button>
      </div>
      {state.notice !== null && (
        <p role="alert" className="text-body text-text">
          {state.notice}
        </p>
      )}
      {state.offline && (
        <p role="status" className="text-body text-muted">
          No runner is linked. Terminals will appear when the runner connects.
        </p>
      )}
      {!state.offline && state.terminals.length === 0 && state.notice === null && (
        <Empty title="No terminals yet." body="Open your first terminal to get a shell on the runner." />
      )}
      {state.terminals.length > 0 && (
        <ul className="flex flex-col gap-2">
          {state.terminals.map((t) => (
            <li
              key={t.term}
              className="flex min-h-11 items-center justify-between gap-2 rounded-md bg-surface px-3 py-2"
            >
              <button
                type="button"
                onClick={() => setState({ view: 'open', id: t.term })}
                aria-label={`Open terminal ${t.term.slice(0, 8)}`}
                className="min-h-11 min-w-0 flex-1 truncate text-left font-mono text-body text-text"
              >
                {t.term.slice(0, 8)}
              </button>
              {confirmClose === t.term ? (
                <span className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => void remove(t.term)}
                    aria-label={`Confirm close terminal ${t.term.slice(0, 8)}`}
                    className="min-h-11 rounded-md bg-accent px-3 text-body text-base"
                  >
                    Close it
                  </button>
                  <button
                    type="button"
                    onClick={() => setConfirmClose(null)}
                    aria-label="Keep terminal"
                    className="min-h-11 rounded-md px-3 text-body text-text"
                  >
                    Keep
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => setConfirmClose(t.term)}
                  aria-label={`Close terminal ${t.term.slice(0, 8)}`}
                  className="min-h-11 min-w-11 rounded-md px-3 text-body text-muted"
                >
                  ✕
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
