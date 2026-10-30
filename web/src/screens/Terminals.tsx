import { useCallback, useEffect, useState } from 'react';
import {
  closeTerminal,
  listAgents,
  listTerminals,
  openTerminal,
  startAgent,
  type RunnerAgents,
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

// modeNote says what the runner's access mode means for an agent
// started now; a mode that asks as expected needs no note.
function modeNote(a: RunnerAgents): string | null {
  if (a.mode === '') {
    return `No access mode is set for ${a.runner}, so agents there can't run any tool yet. Pick one in Settings, Access modes.`;
  }
  if (a.mode === 'off') {
    return `${a.runner} is set to Off: agents there can't run any tool. Change it in Settings, Access modes.`;
  }
  if (a.mode === 'full') {
    return `${a.runner} has full access: agents there run every tool without asking you.`;
  }
  return null;
}

export default function Terminals({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [state, setState] = useState<State>({ view: 'loading' });
  const [confirmClose, setConfirmClose] = useState<string | null>(null);
  const [agents, setAgents] = useState<RunnerAgents | null>(null);
  const [starting, setStarting] = useState<string | null>(null);

  // Promise chains, not await: the session and list states resolve
  // off the effect's synchronous path, the way App.check does.
  const refresh = useCallback((): Promise<void> => {
    // The agent list is a side panel: a failure only hides it.
    void listAgents().then(
      (res) => setAgents(typeof res === 'object' ? res : null),
      () => setAgents(null),
    );
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

  async function start(agent: string): Promise<void> {
    setStarting(agent);
    const res = await startAgent(agent);
    setStarting(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'string') {
      setState({ view: 'list', terminals: state.view === 'list' ? state.terminals : [], offline: res === 'offline', notice: errorWords(res) });
      return;
    }
    if ('refused' in res) {
      setState({ view: 'list', terminals: state.view === 'list' ? state.terminals : [], offline: false, notice: `Couldn't start it: ${res.refused}.` });
      return;
    }
    setState({ view: 'open', id: res.id });
  }

  // label names a terminal by its agent, when it runs one.
  function label(t: TerminalSummary): string {
    const short = t.term.slice(0, 8);
    if (t.name) {
      return `${t.name} · ${short}`;
    }
    if (!t.agent) {
      return short;
    }
    const name = agents?.agents.find((a) => a.id === t.agent)?.name ?? t.agent;
    return `${name} · ${short}`;
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
                aria-label={`Open terminal ${label(t)}`}
                className="min-h-11 min-w-0 flex-1 truncate text-left font-mono text-body text-text"
              >
                {label(t)}
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
      {!state.offline && agents !== null && agents.agents.length > 0 && (
        <section aria-label="Start an agent" className="flex flex-col gap-2">
          <h3 className="text-body font-semibold text-text">Start an agent on {agents.runner}</h3>
          {modeNote(agents) !== null && (
            <p role="note" className="text-body text-muted">
              {modeNote(agents)}
            </p>
          )}
          <ul className="flex flex-col gap-2">
            {agents.agents.map((a) => (
              <li key={a.id} className="flex flex-col gap-1 rounded-md bg-surface px-3 py-2">
                <div className="flex min-h-11 items-center justify-between gap-2">
                  <span className="min-w-0 flex-1 text-body text-text">
                    <span className="block">{a.name}</span>
                    <span className="block text-muted">
                      {a.installed ? (a.version ? `Version ${a.version}` : 'Installed') : `Not installed on ${agents.runner}`}
                    </span>
                  </span>
                  {a.installed && (
                    <button
                      type="button"
                      onClick={() => void start(a.id)}
                      disabled={starting !== null}
                      aria-label={`Start ${a.name}`}
                      className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
                    >
                      {starting === a.id ? 'Starting…' : 'Start'}
                    </button>
                  )}
                </div>
                {!a.installed && a.install && (
                  <p className="text-body text-muted">
                    Install it there with{' '}
                    <code className="break-all rounded bg-base px-1 font-mono text-text">{a.install}</code>
                  </p>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
