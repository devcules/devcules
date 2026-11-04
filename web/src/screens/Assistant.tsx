import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  closeTerminal,
  listAgents,
  listTerminals,
  openTerminal,
  startAgent,
  type AgentInfo,
  type TerminalError,
  type TerminalSummary,
} from '../api';
import TerminalView from './TerminalView';
import { useRunners } from './useRunners';
import { Empty } from './Empty';
import Renameable from './Renameable';
import { useAlive } from './pulse';
import { renameTerminal } from '../api';

const AUTO = 'auto';

// Assistant is a tmux session per assistant you start, one tab strip
// across the top the way the machine's terminals live. Everything you
// see runs on the runner: you open it, chat or command in the pane,
// and Stop takes the tmux session down. The Assistants sub-list is
// the CLIs the runner has (Hermes, OpenClaw, anything detected on the
// machine); each Start launches that assistant inside a fresh named
// terminal.
export default function Assistant({
  onSessionExpired,
  fs = false,
  onEnterFs,
  onExitFs,
}: {
  onSessionExpired: () => void;
  // fs = the shell's mobile fullscreen: the page renders only this
  // section, sized to the visible height. The tab strip folds away
  // (the ☰ key on the custom keyboard, or this pane's back, shows it).
  fs?: boolean;
  onEnterFs?: () => void;
  onExitFs?: () => void;
}) {
  const [terms, setTerms] = useState<TerminalSummary[] | 'loading' | 'unreachable'>('loading');
  const [agents, setAgents] = useState<AgentInfo[] | 'loading' | 'unreachable'>('loading');
  const [active, setActive] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [showNew, setShowNew] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Which runner this screen's terminals live on. Empty while unknown;
  // with two runners linked an untargeted call is a 400, so this must
  // be resolved before anything is listed or opened.
  const runners = useRunners(onSessionExpired);
  // Memoised so a fresh array identity each render doesn't churn the
  // callbacks below.
  const runnerList = useMemo(
    () => (runners.state === 'ready' ? runners.runners : []),
    [runners],
  );
  const [picked, setPicked] = useState('');
  // chosen is derived, not stored: a pick survives while that runner is
  // still linked, else the first live one wins ('' when none).
  // useRunners puts the host first, so the default is the Mac serve runs
  // on rather than whichever name sorts first.
  const chosen = runnerList.includes(picked) ? picked : runnerList[0] ?? '';
  const where = chosen === '' ? undefined : { runner: chosen };

  const refresh = useCallback(() => {
    if (chosen === '') {
      // No runner resolved yet: listing now would be the untargeted 400
      // this screen used to misreport as "can't reach the server".
      return;
    }
    void listTerminals({ runner: chosen }).then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (typeof res === 'string') {
        setTerms('unreachable');
        return;
      }
      // Project terminals belong to a project; they are not
      // assistants and must not appear here (a ✕ on the strip then
      // can't kill a project's shell by mistake).
      setTerms(res.terminals.filter((t) => t.project === undefined));
    });
    void listAgents({ runner: chosen }).then((res) => {
      if (res === 'unauthorized' || res === 'unreachable' || res === 'offline') {
        setAgents('unreachable');
        return;
      }
      setAgents(res.agents.filter((a) => a.direct));
    });
  }, [onSessionExpired, chosen]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Poll every 5 s: a terminal that ended elsewhere drops off, a newly
  // started assistant appears.
  useEffect(() => {
    const t = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        refresh();
      }
    }, 5000);
    return () => window.clearInterval(t);
  }, [refresh]);

  const list = Array.isArray(terms) ? terms : [];
  const active_t = list.find((t) => t.term === active);

  async function spawn(agent: AgentInfo | null): Promise<void> {
    setError(null);
    const key = agent?.id ?? AUTO;
    setBusy(key);
    const res = agent
      ? await startAgent(agent.id, { name: agent.id, where })
      : await openTerminal(where, 'assistant');
    setBusy(null);
    setShowNew(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'string') {
      setError(words(res));
      return;
    }
    if ('refused' in res) {
      setError(res.refused);
      return;
    }
    setActive(res.id);
    setShowNew(false);
    refresh();
  }

  async function drop(id: string): Promise<void> {
    setError(null);
    const res = await closeTerminal(id, where);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (active === id) {
      setActive(null);
    }
    refresh();
  }

  const agentsList = Array.isArray(agents) ? agents : [];

  return (
    <section aria-label="Assistant" className="flex h-full min-h-0 flex-col gap-2">
      {/* The tab strip: every live assistant terminal, one tap to
          focus its pane. The newest opens selected. */}
      <div className={`flex flex-wrap items-center gap-1 ${fs ? 'hidden' : ''}`}>
        <button
          type="button"
          onClick={() => setActive(null)}
          aria-pressed={active === null}
          data-selected={active === null ? 'true' : undefined}
          className={`i-row flex min-h-11 items-center gap-1.5 px-3 text-body ${active === null ? 'text-text' : 'text-muted'}`}
        >
          ◈ Assistants
          <span className="text-body opacity-70">{list.length}</span>
        </button>
        {list.map((t) => (
          <AssistantTab
            key={t.term}
            t={t}
            selected={active === t.term}
            onSelect={() => {
              setActive(t.term);
              // Mobile: focusing a pane pulls the whole view to it —
              // the shell folds and the pane takes the viewport. ☰ or
              // the pane's back puts the tabs back.
              onEnterFs?.();
            }}
            onRename={async (next) => {
              const ok = await renameTerminal(t.term, next, where);
              if (ok === 'unauthorized') {
                onSessionExpired();
                return false;
              }
              if (ok !== true) {
                return false;
              }
              refresh();
              return true;
            }}
            onClose={() => void drop(t.term)}
          />
        ))}
        <button
          type="button"
          onClick={() => {
            setActive(null);
            setShowNew((s) => !s);
          }}
          aria-expanded={showNew}
          aria-label="New assistant terminal"
          className="i-control min-h-11 px-3 text-body text-text"
        >
          +
        </button>
        {active_t && (
          <div className="ml-auto flex items-center gap-2">
            <button
              type="button"
              onClick={() => void drop(active_t.term)}
              aria-label={`Stop ${active_t.name ?? active_t.term}`}
              className="i-control min-h-11 px-3 text-body text-muted"
            >
              Stop
            </button>
          </div>
        )}
      </div>

      {error !== null && (
        <p role="alert" className="text-body text-text">
          {error}
        </p>
      )}
      {runners.state === 'ready' && runnerList.length > 1 && (
        <label className="flex flex-wrap items-center gap-2 text-tab text-muted">
          assistant terminals run on
          <select
            value={chosen}
            onChange={(e) => setPicked(e.target.value)}
            className="min-h-11 rounded bg-base px-2 text-tab text-text"
            aria-label="Assistant runner"
          >
            {runnerList.map((r) => (
              <option key={r} value={r}>{r === runners.host ? `${r} (this machine)` : r}</option>
            ))}
          </select>
          <span>({runnerList.length} linked)</span>
        </label>
      )}
      {runners.state === 'ready' && runnerList.length === 0 && (
        <p role="alert" className="text-body text-text">
          No runner is linked. Start the runner and this screen fills in.
        </p>
      )}
      {terms === 'unreachable' && chosen !== '' && (
        <p role="alert" className="text-body text-text">
          Couldn't reach the server. Try again.
        </p>
      )}

      {/* The pane: nothing open opens a new terminal on its own; an
          active tab streams it full-screen. */}
      {/* The pane: an active tab streams it full-screen. The new
          card shows on the overview tab — the only place it makes
          sense to start from — which the + button lands back on. */}
      {active !== null && active_t && (
        <div className="flex min-h-0 flex-1 flex-col">
          <TerminalView
            id={active_t.term}
            where={where}
            onShowTabs={onExitFs}
            onBack={() => {
              setActive(null);
              onExitFs?.();
            }}
            onSessionExpired={onSessionExpired}
          />
        </div>
      )}
      {active === null && (
        <div className="flex flex-col gap-3">
          {list.length === 0 && terms !== 'loading' && (
            <Empty
              title="No assistants running."
              body="Open one below: a plain shell, or one of the assistant CLIs the runner has."
            />
          )}
          <NewCard
            agents={agentsList}
            busy={busy}
            onSpawn={(a) => void spawn(a)}
            agentsState={agents}
          />
        </div>
      )}
    </section>
  );
}

// AssistantTab is one live terminal in the strip: a live-dot that
// pulses while its pane emits, a right-click-to-rename label, and a
// close control. The label shrinks a step so a long name truncates
// instead of shoving the strip wide.
function AssistantTab({
  t,
  selected,
  onSelect,
  onRename,
  onClose,
}: {
  t: TerminalSummary;
  selected: boolean;
  onSelect: () => void;
  onRename: (next: string) => Promise<boolean>;
  onClose: () => void;
}) {
  const label = t.name ?? t.term.slice(0, 8);
  // The dot pulses only while this tab's pane is the one streaming.
  const alive = useAlive(t.term, selected);
  return (
    <span className="flex items-center">
      <button
        type="button"
        onClick={onSelect}
        aria-pressed={selected}
        title={t.name ?? t.term}
        data-selected={selected ? 'true' : undefined}
        className="i-row flex min-h-11 items-center gap-1.5 px-3 text-body text-muted data-[selected=true]:text-text"
      >
        <span aria-hidden="true" className="i-live" data-on={alive ? 'true' : 'false'} />
        <Renameable
          value={label}
          onCommit={onRename}
          className="max-w-28 text-tab"
          inputClassName="text-tab"
        />
      </button>
      <button
        type="button"
        onClick={onClose}
        aria-label={`Close ${label}`}
        title={`Close ${label}`}
        className="i-control min-h-11 min-w-9 px-1 text-body opacity-70"
      >
        ✕
      </button>
    </span>
  );
}

function NewCard({
  agents,
  busy,
  onSpawn,
  agentsState,
}: {
  agents: AgentInfo[];
  busy: string | null;
  onSpawn: (a: AgentInfo | null) => void;
  agentsState: AgentInfo[] | 'loading' | 'unreachable';
}) {
  return (
    <section aria-label="Start an assistant" className="flex flex-col gap-2 rounded-md bg-surface p-3">
      <h3 className="text-body font-semibold text-text">Start an assistant</h3>
      <p className="text-body text-muted">
        A shell is always there; the assistants below are the CLIs the runner has on hand. Tools
        stay with the CLI you pick.
      </p>
      <ul className="flex flex-col gap-2">
        <li className="flex min-h-11 items-center justify-between gap-2 rounded-md bg-base px-3 py-1">
          <span className="min-w-0 flex-1 text-left">
            <span className="block text-body font-semibold text-text">Shell</span>
            <span className="block text-body text-muted">A plain tmux terminal on the runner</span>
          </span>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => onSpawn(null)}
            aria-label="Start a shell"
            className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
          >
            {busy === AUTO ? 'Starting…' : 'Start'}
          </button>
        </li>
        {agents.map((a) => {
          const self = a.id;
          return (
            <li key={self} className="flex min-h-11 items-center justify-between gap-2 rounded-md bg-base px-3 py-1">
              <span className="min-w-0 flex-1 text-left">
                <span className="block text-body font-semibold text-text">{a.name}</span>
                <span className="block text-body text-muted">
                  {a.installed ? (a.version ? `v${a.version}` : 'Installed') : 'Not installed — show install command'}
                </span>
              </span>
              <button
                type="button"
                disabled={busy !== null || !a.installed}
                onClick={() => onSpawn(a)}
                aria-label={`Start ${a.name}`}
                className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
              >
                {busy === self ? 'Starting…' : 'Start'}
              </button>
            </li>
          );
        })}
        {agentsState === 'loading' && <li className="text-body text-muted">Checking which assistants are installed…</li>}
        {agentsState === 'unreachable' && (
          <li className="text-body text-muted">The runner did not answer; only the shell is offered.</li>
        )}
      </ul>
      {agents.some((a) => !a.installed && a.install) && (
        <p className="text-body text-muted">
          Not-installed assistants list their install lives with the runner where they belong.
        </p>
      )}
    </section>
  );
}

function words(err: TerminalError | 'busy'): string {
  switch (err) {
    case 'busy':
      return 'Another terminal is opening. Try in a moment.';
    case 'offline':
      return 'The runner is offline. Start assistants once it is back.';
    case 'unreachable':
      return "Couldn't reach the server. Try again.";
    default:
      return 'Something went wrong. Try again.';
  }
}
