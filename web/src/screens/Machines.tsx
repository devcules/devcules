import { useCallback, useEffect, useState } from 'react';
import {
  closeTerminal,
  listMachines,
  listTerminals,
  mintEnrolCode,
  openTerminal,
  removeMachine,
  type MachineError,
  type MachineRow,
  type TerminalSummary,
} from '../api';
import MachineScreen from './MachineScreen';
import TerminalView from './TerminalView';
import { Empty } from './Empty';

type View =
  | { view: 'loading' }
  | { view: 'list' }
  | { view: 'terminal'; machine: string; id: string }
  | { view: 'screen'; machine: string };

// errorWords turns API outcomes into plain words. 'unauthorized'
// never lands here: callers route it to sign-in first.
function errorWords(err: MachineError | 'busy'): string {
  if (err === 'offline') {
    return 'That machine is offline. Start devcules machine on it and try again.';
  }
  if (err === 'busy') {
    return 'The machine did not answer. Try again.';
  }
  return 'Something went wrong. Try again.';
}

// seen says when an offline machine was last linked, in plain words.
function seen(m: MachineRow): string {
  if (m.online) {
    return 'Online';
  }
  if (!m.lastSeen) {
    return 'Offline, never linked';
  }
  const at = new Date(m.lastSeen);
  return Number.isNaN(at.getTime()) ? 'Offline' : `Offline, last seen ${at.toLocaleString()}`;
}

export default function Machines({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<View>({ view: 'loading' });
  const [machines, setMachines] = useState<MachineRow[]>([]);
  const [budget, setBudget] = useState(0);
  const [terms, setTerms] = useState<Record<string, TerminalSummary[]>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [opening, setOpening] = useState<string | null>(null);

  // refresh reloads the machines and, for the online ones, their
  // terminals. Promise chains, not await: the states resolve off the
  // effect's synchronous path, the way Terminals.refresh does.
  const refresh = useCallback((): Promise<void> => {
    return listMachines().then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setView((v) => (v.view === 'loading' ? { view: 'list' } : v));
        if (typeof res !== 'object') {
          setMachines([]);
          setNotice(res === 'offline' ? null : errorWords(res));
          return;
        }
        setNotice(null);
        setMachines(res.machines);
        setBudget(res.budget);
        for (const m of res.machines.filter((x) => x.online)) {
          void listTerminals({ machine: m.name }).then((t) => {
            if (typeof t === 'object') {
              setTerms((all) => ({ ...all, [m.name]: t.terminals }));
            }
          });
        }
      },
      () => {
        setView((v) => (v.view === 'loading' ? { view: 'list' } : v));
        setNotice(errorWords('unreachable'));
      },
    );
  }, [onSessionExpired]);

  // The list re-checks every 5 s while visible, so a machine that
  // just enrolled or dropped shows up without a reload.
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        void refresh();
      }
    }, 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  async function terminal(machine: string): Promise<void> {
    setOpening(machine);
    const res = await openTerminal({ machine });
    setOpening(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res !== 'object') {
      setNotice(errorWords(res));
      return;
    }
    setView({ view: 'terminal', machine, id: res.id });
  }

  async function closeTerm(machine: string, id: string): Promise<void> {
    if ((await closeTerminal(id, { machine })) === 'unauthorized') {
      onSessionExpired();
      return;
    }
    await refresh();
  }

  async function remove(name: string): Promise<void> {
    const res = await removeMachine(name);
    setConfirmRemove(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res !== true) {
      setNotice(errorWords(res));
    }
    await refresh();
  }

  if (view.view === 'loading') {
    return <div className="flex flex-1 items-center justify-center" aria-busy="true" />;
  }
  if (view.view === 'terminal') {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <TerminalView
          id={view.id}
          where={{ machine: view.machine }}
          onSessionExpired={onSessionExpired}
          onBack={() => {
            setView({ view: 'list' });
            void refresh();
          }}
        />
      </div>
    );
  }
  if (view.view === 'screen') {
    return (
      <MachineScreen
        machine={view.machine}
        budget={budget}
        onSessionExpired={onSessionExpired}
        onBack={() => {
          setView({ view: 'list' });
          void refresh();
        }}
      />
    );
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lead font-semibold text-text">Machines</h2>
        {!adding && (
          <button type="button" onClick={() => setAdding(true)} className="min-h-11 rounded-md bg-accent px-4 text-body text-base">
            Add a computer
          </button>
        )}
      </div>
      {notice !== null && (
        <p role="alert" className="text-body text-text">
          {notice}
        </p>
      )}
      {adding && (
        <AddComputer
          known={machines.map((m) => m.name)}
          onSessionExpired={onSessionExpired}
          onDone={() => {
            setAdding(false);
            void refresh();
          }}
        />
      )}
      {machines.length === 0 && !adding && notice === null && (
        <Empty
          title="No computers yet."
          body="Add one and you can open a terminal on it from here, see its screen and let agents work on it."
        />
      )}
      {machines.length > 0 && (
        <ul className="flex flex-col gap-3">
          {machines.map((m) => (
            <li key={m.name} className="flex flex-col gap-2 rounded-md bg-surface px-3 py-3">
              <div className="flex min-h-11 flex-wrap items-center gap-2">
                <span
                  aria-hidden="true"
                  className={`h-2.5 w-2.5 shrink-0 rounded-full ${m.online ? 'bg-text' : 'border border-muted'}`}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono text-body text-text">{m.name}</span>
                  <span className="block text-body text-muted">{seen(m)}</span>
                </span>
                {m.online && (
                  <>
                    <button
                      type="button"
                      onClick={() => void terminal(m.name)}
                      disabled={opening !== null}
                      aria-label={`Open a terminal on ${m.name}`}
                      className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
                    >
                      {opening === m.name ? 'Opening…' : 'Terminal'}
                    </button>
                    <button
                      type="button"
                      onClick={() => setView({ view: 'screen', machine: m.name })}
                      aria-label={`See the screen of ${m.name}`}
                      className="min-h-11 rounded-md bg-base px-4 text-body text-text"
                    >
                      Screen
                    </button>
                  </>
                )}
                {confirmRemove === m.name ? (
                  <span className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => void remove(m.name)}
                      aria-label={`Confirm remove ${m.name}`}
                      className="min-h-11 rounded-md bg-accent px-3 text-body text-base"
                    >
                      Remove it
                    </button>
                    <button type="button" onClick={() => setConfirmRemove(null)} className="min-h-11 rounded-md px-3 text-body text-text">
                      Keep
                    </button>
                  </span>
                ) : (
                  <button
                    type="button"
                    onClick={() => setConfirmRemove(m.name)}
                    aria-label={`Remove ${m.name}`}
                    className="min-h-11 min-w-11 rounded-md px-3 text-body text-muted"
                  >
                    ✕
                  </button>
                )}
              </div>
              {confirmRemove === m.name && (
                <p className="text-body text-muted">
                  Removing revokes its key and cuts its link. To bring it back, add it again with a new code.
                </p>
              )}
              {m.online && (terms[m.name]?.length ?? 0) > 0 && (
                <ul aria-label={`Terminals on ${m.name}`} className="flex flex-col gap-1">
                  {terms[m.name].map((t) => (
                    <li key={t.term} className="flex min-h-11 items-center gap-2 rounded-md bg-base px-3">
                      <button
                        type="button"
                        onClick={() => setView({ view: 'terminal', machine: m.name, id: t.term })}
                        aria-label={`Open terminal ${t.term.slice(0, 8)} on ${m.name}`}
                        className="min-h-11 min-w-0 flex-1 truncate text-left font-mono text-body text-text"
                      >
                        {t.dir ? `${t.term.slice(0, 8)} · ${t.dir}` : t.term.slice(0, 8)}
                      </button>
                      <button
                        type="button"
                        onClick={() => void closeTerm(m.name, t.term)}
                        aria-label={`Close terminal ${t.term.slice(0, 8)} on ${m.name}`}
                        className="min-h-11 min-w-11 rounded-md px-3 text-body text-muted"
                      >
                        ✕
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// AddComputer mints a one-time code and shows the two commands to run
// on the computer. It watches the list, so the new machine is named
// the moment it links.
function AddComputer({
  known,
  onSessionExpired,
  onDone,
}: {
  known: string[];
  onSessionExpired: () => void;
  onDone: () => void;
}) {
  const [label, setLabel] = useState('');
  const [code, setCode] = useState<{ code: string; until: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [before] = useState(() => new Set(known));
  const [copied, setCopied] = useState<string | null>(null);

  useEffect(() => {
    if (code === null) {
      return;
    }
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [code]);

  const joined = known.find((n) => !before.has(n));

  async function mint(): Promise<void> {
    setBusy(true);
    setError(null);
    const res = await mintEnrolCode(label.trim());
    setBusy(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res === 'unreachable') {
      setError('Something went wrong. Try again.');
      return;
    }
    if ('refused' in res) {
      setError(res.refused);
      return;
    }
    const at = Date.now();
    setNow(at);
    setCode({ code: res.code, until: at + res.expiresIn * 1000 });
  }

  const serve = window.location.origin;
  const enrol = code ? `DEVCULES_SERVE_URL=${serve} devcules machine enrol ${code.code}` : '';
  const run = 'devcules machine';
  const left = code ? Math.max(0, Math.round((code.until - now) / 1000)) : 0;

  function copy(text: string): void {
    void navigator.clipboard?.writeText(text).then(
      () => setCopied(text),
      () => setCopied(null),
    );
  }

  return (
    <section aria-label="Add a computer" className="flex flex-col gap-3 rounded-md bg-surface px-3 py-3">
      <h3 className="text-body font-semibold text-text">Add a computer</h3>
      {joined !== undefined ? (
        <>
          <p role="status" className="text-body text-text">
            {joined} is linked. Open a terminal on it from the list.
          </p>
          <button type="button" onClick={onDone} className="min-h-11 self-start rounded-md bg-accent px-4 text-body text-base">
            Done
          </button>
        </>
      ) : code === null ? (
        <>
          <p className="text-body text-muted">
            You get a one-time code to type on that computer. It needs the devcules binary, and tmux for the terminal. Nothing on
            it listens for connections: it dials this server.
          </p>
          <form
            className="flex flex-col gap-2 sm:flex-row"
            onSubmit={(e) => {
              e.preventDefault();
              void mint();
            }}
          >
            <label className="sr-only" htmlFor="enrol-label">
              Which computer is it
            </label>
            <input
              id="enrol-label"
              type="text"
              maxLength={64}
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="Which computer, like studio Mac (optional)"
              className="min-h-11 flex-1 rounded-md bg-base px-3 text-body text-text"
            />
            <button type="submit" disabled={busy} className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60">
              {busy ? 'Getting a code…' : 'Get a code'}
            </button>
            <button type="button" onClick={onDone} className="min-h-11 rounded-md px-4 text-body text-text">
              Cancel
            </button>
          </form>
        </>
      ) : (
        <>
          <p className="text-body text-text">
            Code <span className="font-mono text-lead tracking-wider">{code.code}</span>{' '}
            <span className="text-muted">
              {left > 0 ? `· works once, for ${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}` : '· expired, get a new one'}
            </span>
          </p>
          <p className="text-body text-muted">On that computer, run:</p>
          {[enrol, run].map((cmd) => (
            <div key={cmd} className="flex items-start gap-2">
              <code className="min-w-0 flex-1 break-all rounded bg-base px-2 py-2 font-mono text-body text-text">{cmd}</code>
              <button
                type="button"
                onClick={() => copy(cmd)}
                aria-label={`Copy ${cmd === run ? 'the run command' : 'the enrol command'}`}
                className="min-h-11 rounded-md px-3 text-body text-text"
              >
                {copied === cmd ? 'Copied' : 'Copy'}
              </button>
            </div>
          ))}
          <p className="text-body text-muted">
            Use an address that computer can reach in DEVCULES_SERVE_URL (your Tailscale name, for one). Waiting for it to link…
          </p>
          <div className="flex gap-2">
            <button type="button" onClick={() => setCode(null)} className="min-h-11 rounded-md px-4 text-body text-text">
              New code
            </button>
            <button type="button" onClick={onDone} className="min-h-11 rounded-md px-4 text-body text-text">
              Close
            </button>
          </div>
        </>
      )}
      {error !== null && (
        <p role="alert" className="text-body text-text">
          {error}
        </p>
      )}
    </section>
  );
}
