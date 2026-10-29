import { useEffect, useState } from 'react';
import {
  getRedaction,
  setRedactionPolicy,
  testRedaction,
  type RedactMode,
  type RedactState,
  type RedactTestResult,
  type RedactType,
} from '../api';

const typeLabels: Record<RedactType, string> = {
  SECRET: 'Secrets and keys',
  EMAIL: 'Email addresses',
  PHONE: 'Phone numbers',
  CARD: 'Card numbers',
  IPV4: 'Public IP addresses',
};

const modeLabels: Record<RedactMode, string> = {
  off: 'Off',
  log: 'Count only',
  mask: 'Mask',
  block: 'Block',
};

// Redaction settings: one mode per type, the counters since serve
// started, and a test box that shows exactly what a model would get.
export default function Redaction({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [state, setState] = useState<RedactState | 'loading' | 'unreachable'>('loading');
  const [notice, setNotice] = useState<string | null>(null);
  const [text, setText] = useState('');
  const [result, setResult] = useState<RedactTestResult | null>(null);
  const [testing, setTesting] = useState(false);

  useEffect(() => {
    let live = true;
    void getRedaction().then(
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

  async function change(t: RedactType, mode: RedactMode): Promise<void> {
    if (typeof state === 'string') {
      return;
    }
    const before = state;
    const policy = { ...state.policy, [t]: mode };
    setState({ ...state, policy });
    setNotice(null);
    const res = await setRedactionPolicy(policy);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res !== true) {
      setState(before);
      setNotice("Couldn't save that change. Try again.");
    }
  }

  async function runTest(): Promise<void> {
    if (testing || text === '') {
      return;
    }
    setTesting(true);
    setNotice(null);
    try {
      const res = await testRedaction(text);
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (res === 'too-long') {
        setNotice('That text is too long to test (64 KiB at most).');
        return;
      }
      if (res === 'unreachable') {
        setNotice("Couldn't run the test. Try again.");
        return;
      }
      setResult(res);
    } finally {
      setTesting(false);
    }
  }

  return (
    <section aria-label="Redaction" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Redaction</h3>
      {state === 'loading' && <p className="text-body text-muted">Loading redaction settings…</p>}
      {state === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t load redaction settings. Try again.
        </p>
      )}
      {typeof state !== 'string' && (
        <>
          <p className="text-body text-muted">
            Every model call is scanned first. Masked values reach the model as placeholders and come back
            in the reply. {state.rules} secret rules are loaded.
          </p>
          <ul className="flex flex-col gap-2">
            {state.types.map((t) => {
              const c = state.stats[t] ?? { detected: 0, masked: 0, blocked: 0 };
              return (
                <li key={t} className="flex min-h-11 flex-wrap items-center gap-2 rounded-md border border-muted px-3 py-1">
                  <span className="min-w-0 flex-1">
                    <span className="block text-body text-text">{typeLabels[t] ?? t}</span>
                    <span className="block text-body text-muted">
                      {c.detected} found · {c.masked} masked · {c.blocked} blocked
                    </span>
                  </span>
                  <select
                    aria-label={`${typeLabels[t] ?? t} mode`}
                    value={state.policy[t]}
                    onChange={(e) => void change(t, e.target.value as RedactMode)}
                    className="min-h-11 rounded-md border border-muted bg-surface px-2 text-body text-text"
                  >
                    {state.modes.map((m) => (
                      <option key={m} value={m}>
                        {modeLabels[m] ?? m}
                      </option>
                    ))}
                  </select>
                </li>
              );
            })}
          </ul>
          <label htmlFor="redact-test" className="text-body text-text">
            Test text
          </label>
          <textarea
            id="redact-test"
            rows={3}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Paste text to see what a model would receive"
            className="min-h-11 rounded-md border border-muted bg-surface px-2 py-1 font-mono text-body text-text"
          />
          <button
            type="button"
            onClick={() => void runTest()}
            disabled={testing || text === ''}
            className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150 disabled:opacity-60"
          >
            {testing ? 'Testing…' : 'Test'}
          </button>
          {result && (
            <div aria-label="Test result" className="flex flex-col gap-1">
              {result.blocked.length > 0 ? (
                <p className="text-body text-text">
                  Blocked: this text holds {result.blocked.map((t) => typeLabels[t] ?? t).join(', ')}, which are set
                  to block. A model would not receive it.
                </p>
              ) : (
                <pre className="whitespace-pre-wrap break-all rounded-md border border-muted px-2 py-1 font-mono text-body text-text">
                  {result.text}
                </pre>
              )}
              <p className="text-body text-muted">
                {result.findings.length === 0
                  ? 'Nothing found.'
                  : `Found: ${result.findings.map((f) => `${typeLabels[f.type] ?? f.type} (${f.rule})`).join(', ')}`}
              </p>
            </div>
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
