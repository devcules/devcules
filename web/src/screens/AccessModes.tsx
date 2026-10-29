import { useEffect, useState } from 'react';
import {
  checkCommand,
  getApprovalSettings,
  setAccessMode,
  setCriticalRules,
  type AccessMode,
  type AccessTarget,
  type ApprovalSettings,
  type CriticalRule,
} from '../api';

const modeLabels: Record<AccessMode, string> = {
  off: 'Off: block every tool call',
  critical: 'Ask before critical calls',
  all: 'Ask before every call',
  full: 'Full access: never ask',
};

function targetName(t: AccessTarget): string {
  if (t.runner === '') {
    return 'Agents without a runner';
  }
  return t.linked ? t.runner : `${t.runner} (not linked)`;
}

// slug turns a rule name into the id the server wants.
function slug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40);
}

// AccessModes holds the approval settings: one access mode per
// runner, the critical command rules, and a box to try a command
// against them. Rules are a tripwire for agents that mean well, not a
// sandbox: the doc says so too.
export default function AccessModes({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [state, setState] = useState<ApprovalSettings | 'loading' | 'unreachable'>('loading');
  const [notice, setNotice] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [pattern, setPattern] = useState('');
  const [command, setCommand] = useState('');
  const [verdict, setVerdict] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    void getApprovalSettings().then(
      (res) => {
        if (!live) {
          return;
        }
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setState(res);
      },
      () => {
        if (live) {
          setState('unreachable');
        }
      },
    );
    return () => {
      live = false;
    };
  }, [onSessionExpired]);

  // save applies a write's answer: fresh settings, or a reason.
  function save(res: ApprovalSettings | { invalid: string } | 'unauthorized' | 'unreachable', before: ApprovalSettings): boolean {
    if (res === 'unauthorized') {
      onSessionExpired();
      return false;
    }
    if (res === 'unreachable') {
      setState(before);
      setNotice("Couldn't save that change. Try again.");
      return false;
    }
    if ('invalid' in res) {
      setState(before);
      setNotice(`Couldn't save that: ${res.invalid}.`);
      return false;
    }
    setState(res);
    return true;
  }

  async function changeMode(target: string, mode: AccessMode): Promise<void> {
    if (typeof state === 'string') {
      return;
    }
    const before = state;
    setState({ ...state, targets: state.targets.map((t) => (t.target === target ? { ...t, mode } : t)) });
    setNotice(null);
    save(await setAccessMode(target, mode), before);
  }

  async function changeRules(rules: CriticalRule[]): Promise<boolean> {
    if (typeof state === 'string') {
      return false;
    }
    const before = state;
    setState({ ...state, rules });
    setNotice(null);
    setVerdict(null);
    return save(await setCriticalRules(rules), before);
  }

  async function addRule(): Promise<void> {
    if (typeof state === 'string' || name.trim() === '' || pattern === '') {
      return;
    }
    const id = slug(name);
    if (id === '' || state.rules.some((r) => r.id === id)) {
      setNotice('Pick a name that no other rule uses.');
      return;
    }
    const added = await changeRules([...state.rules, { id, name: name.trim(), pattern, enabled: true, builtin: false }]);
    if (added) {
      setName('');
      setPattern('');
    }
  }

  async function check(): Promise<void> {
    if (command === '') {
      return;
    }
    setNotice(null);
    const res = await checkCommand(command);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res === 'unreachable' || 'invalid' in res) {
      setNotice("Couldn't check that command. Try again.");
      return;
    }
    setVerdict(
      res.critical && res.rule
        ? `Critical: ${res.rule.name}. Runners set to ask before critical calls will ask you first.`
        : 'Not critical: it runs without asking on runners set to ask before critical calls.',
    );
  }

  return (
    <section aria-label="Access modes" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Access modes</h3>
      {state === 'loading' && <p className="text-body text-muted">Loading access modes…</p>}
      {state === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t load access modes. Try again.
        </p>
      )}
      {typeof state !== 'string' && (
        <>
          <p className="text-body text-muted">
            What agents may run on each runner without asking you. A runner with no mode blocks every call until
            you pick one. Unanswered approvals are denied after {Math.round(state.timeoutSeconds / 60)} minutes.
          </p>
          <ul className="flex flex-col gap-2">
            {state.targets.map((t) => (
              <li key={t.target} className="flex min-h-11 flex-wrap items-center gap-2 rounded-md border border-muted px-3 py-1">
                <span className="min-w-40 flex-1 text-body break-words text-text">{targetName(t)}</span>
                <select
                  aria-label={`${targetName(t)} access mode`}
                  value={t.mode}
                  onChange={(e) => void changeMode(t.target, e.target.value as AccessMode)}
                  className="min-h-11 w-full rounded-md border border-muted bg-surface px-2 text-body text-text sm:w-auto"
                >
                  {t.mode === '' && (
                    <option value="" disabled>
                      Not set: blocks every call
                    </option>
                  )}
                  {state.modes.map((m) => (
                    <option key={m} value={m}>
                      {modeLabels[m] ?? m}
                    </option>
                  ))}
                </select>
              </li>
            ))}
          </ul>
          <h3 className="text-body font-semibold text-text">Critical commands</h3>
          <p className="text-body text-muted">
            A command matching an enabled rule counts as critical. These rules catch honest mistakes; they are
            not a sandbox.
          </p>
          <ul aria-label="Critical rules" className="flex flex-col gap-2">
            {state.rules.map((r) => (
              <li key={r.id} className="flex min-h-11 items-center gap-2 rounded-md border border-muted px-3 py-1">
                <label className="flex min-h-11 min-w-0 flex-1 items-center gap-2 text-body text-text">
                  <input
                    type="checkbox"
                    checked={r.enabled}
                    onChange={(e) =>
                      void changeRules(state.rules.map((x) => (x.id === r.id ? { ...x, enabled: e.target.checked } : x)))
                    }
                    className="size-5"
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block">{r.name}</span>
                    {!r.builtin && <span className="block truncate font-mono text-muted">{r.pattern}</span>}
                  </span>
                </label>
                {!r.builtin && (
                  <button
                    type="button"
                    aria-label={`Remove rule ${r.name}`}
                    onClick={() => void changeRules(state.rules.filter((x) => x.id !== r.id))}
                    className="min-h-11 px-2 text-body text-muted transition-colors duration-150"
                  >
                    Remove
                  </button>
                )}
              </li>
            ))}
          </ul>
          <div className="flex flex-col gap-2">
            <label htmlFor="rule-name" className="text-body text-text">
              New rule name
            </label>
            <input
              id="rule-name"
              value={name}
              maxLength={80}
              onChange={(e) => setName(e.target.value)}
              placeholder="Deploys to production"
              className="min-h-11 rounded-md border border-muted bg-surface px-2 text-body text-text"
            />
            <label htmlFor="rule-pattern" className="text-body text-text">
              Pattern (a regular expression)
            </label>
            <input
              id="rule-pattern"
              value={pattern}
              maxLength={500}
              onChange={(e) => setPattern(e.target.value)}
              placeholder="kubectl .*--context[= ]prod"
              className="min-h-11 rounded-md border border-muted bg-surface px-2 font-mono text-body text-text"
            />
            <button
              type="button"
              onClick={() => void addRule()}
              disabled={name.trim() === '' || pattern === ''}
              className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150 disabled:opacity-60"
            >
              Add rule
            </button>
          </div>
          <label htmlFor="rule-check" className="text-body text-text">
            Try a command
          </label>
          <textarea
            id="rule-check"
            rows={2}
            value={command}
            onChange={(e) => {
              setCommand(e.target.value);
              setVerdict(null);
            }}
            placeholder="git push --force origin main"
            className="min-h-11 rounded-md border border-muted bg-surface px-2 py-1 font-mono text-body text-text"
          />
          <button
            type="button"
            onClick={() => void check()}
            disabled={command === ''}
            className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150 disabled:opacity-60"
          >
            Check
          </button>
          {verdict && (
            <p role="status" className="text-body text-text">
              {verdict}
            </p>
          )}
        </>
      )}
      {notice && (
        <p role="alert" className="text-body text-text">
          {notice}
        </p>
      )}
    </section>
  );
}
