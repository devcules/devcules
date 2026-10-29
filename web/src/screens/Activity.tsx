import { useCallback, useEffect, useState } from 'react';
import {
  auditExportURL,
  checkAudit,
  getTrace,
  listAudit,
  listTraces,
  type AuditCheck,
  type AuditEntry,
  type TraceCall,
  type TraceSummary,
} from '../api';
import { RecentApprovals } from './Approvals';

const actionLabels: Record<string, string> = {
  'auth.sign_in': 'Signed in',
  'auth.sign_in_failed': 'Sign-in failed',
  'auth.sign_out': 'Signed out',
  'passkey.add': 'Added a passkey',
  'passkey.rename': 'Renamed a passkey',
  'passkey.remove': 'Removed a passkey',
  'terminal.open': 'Opened a terminal',
  'terminal.close': 'Closed a terminal',
  'redact.policy': 'Changed redaction',
  'launch.mint': 'Issued a launch token',
  'launch.revoke': 'Revoked a launch token',
  'trace.read': 'Read a trace',
  'audit.export': 'Exported the audit log',
  'vault.rotate': 'Rotated the vault key',
  'mcp.call': 'An agent called a Devcules tool',
  'approval.ask': 'An agent asked for approval',
  'approval.allow': 'Allowed a tool call',
  'approval.deny': 'Denied a tool call',
  'approval.expire': 'An approval expired',
  'approval.blocked': 'Blocked a tool call',
  'approval.mode': 'Changed an access mode',
  'approval.rules': 'Changed critical rules',
  'push.subscribe': 'Turned on notifications',
  'push.remove': 'Removed a notification device',
};

function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

// Activity: what agents asked models (traces, one per launch) and who
// did what (the audit log). Masked values show as placeholders here:
// traces keep what the provider saw.
export default function Activity({ onSessionExpired }: { onSessionExpired: () => void }) {
  return (
    <div className="flex flex-1 flex-col gap-6 px-6 pt-8 pb-6">
      <h2 className="text-lead font-semibold text-text">Activity</h2>
      <Traces onSessionExpired={onSessionExpired} />
      <RecentApprovals onSessionExpired={onSessionExpired} />
      <Audit onSessionExpired={onSessionExpired} />
    </div>
  );
}

function Traces({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [sessions, setSessions] = useState<TraceSummary[] | 'loading' | 'unreachable'>('loading');
  const [open, setOpen] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    void listTraces().then(
      (res) => {
        if (!live) {
          return;
        }
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setSessions(res === 'unreachable' ? res : res.sessions);
      },
      () => {
        if (live) {
          setSessions('unreachable');
        }
      },
    );
    return () => {
      live = false;
    };
  }, [onSessionExpired]);

  return (
    <section aria-label="Traces" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Model calls</h3>
      {sessions === 'loading' && <p className="text-body text-muted">Loading traces…</p>}
      {sessions === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t load traces. Try again.
        </p>
      )}
      {Array.isArray(sessions) && sessions.length === 0 && (
        <p className="text-body text-muted">No model calls yet. Agents you start will show up here.</p>
      )}
      {Array.isArray(sessions) && sessions.length > 0 && (
        <ul className="flex flex-col gap-2">
          {sessions.map((s) => (
            <li key={s.launchId} className="rounded-md border border-muted">
              <button
                type="button"
                aria-expanded={open === s.launchId}
                onClick={() => setOpen(open === s.launchId ? null : s.launchId)}
                className="flex min-h-11 w-full flex-col items-start px-3 py-1 text-left"
              >
                <span className="text-body text-text">{s.label || s.launchId}</span>
                <span className="text-body text-muted">
                  {s.calls} {s.calls === 1 ? 'call' : 'calls'} · {s.inputTokens + s.outputTokens} tokens ·{' '}
                  {s.redacted} redacted · {when(s.last)}
                </span>
              </button>
              {open === s.launchId && <TraceCalls launchId={s.launchId} onSessionExpired={onSessionExpired} />}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function TraceCalls({ launchId, onSessionExpired }: { launchId: string; onSessionExpired: () => void }) {
  const [calls, setCalls] = useState<TraceCall[] | 'loading' | 'unreachable'>('loading');

  useEffect(() => {
    let live = true;
    void getTrace(launchId).then(
      (res) => {
        if (!live) {
          return;
        }
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setCalls(res === 'unreachable' ? res : res.calls);
      },
      () => {
        if (live) {
          setCalls('unreachable');
        }
      },
    );
    return () => {
      live = false;
    };
  }, [launchId, onSessionExpired]);

  if (calls === 'loading') {
    return <p className="px-3 pb-2 text-body text-muted">Loading calls…</p>;
  }
  if (calls === 'unreachable') {
    return (
      <p role="alert" className="px-3 pb-2 text-body text-text">
        Couldn&apos;t load this trace. Try again.
      </p>
    );
  }
  return (
    <ol className="flex flex-col gap-3 border-t border-muted px-3 py-2">
      {calls.map((c) => {
        const redacted = Object.entries(c.redacted)
          .filter(([, n]) => n > 0)
          .map(([t, n]) => `${n} ${t}`)
          .join(', ');
        return (
          <li key={c.id} className="flex flex-col gap-1">
            <span className="text-body text-muted">
              {when(c.at)} · {c.model || c.path} · {c.status} · {c.durationMs} ms
              {c.inputTokens + c.outputTokens > 0 && ` · ${c.inputTokens} in / ${c.outputTokens} out`}
              {redacted && ` · redacted ${redacted}`}
            </span>
            {c.question && <TraceText label="Asked" text={c.question} />}
            {c.reply && <TraceText label="Reply" text={c.reply} />}
            {c.toolCalls.map((t, i) => (
              <TraceText key={i} label={`Tool ${t.name}`} text={t.args} />
            ))}
          </li>
        );
      })}
    </ol>
  );
}

function TraceText({ label, text }: { label: string; text: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-body text-muted">{label}</span>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-surface p-2 font-mono text-body text-text">
        {text}
      </pre>
    </div>
  );
}

function Audit({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [entries, setEntries] = useState<AuditEntry[] | 'loading' | 'unreachable'>('loading');
  const [more, setMore] = useState(false);
  const [check, setCheck] = useState<AuditCheck | 'checking' | 'unreachable' | null>(null);

  const load = useCallback(
    (before: number): Promise<void> =>
      listAudit(before).then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        if (res === 'unreachable') {
          if (before === 0) {
            setEntries('unreachable');
          }
          return;
        }
        setEntries((prev) => (before > 0 && Array.isArray(prev) ? [...prev, ...res.entries] : res.entries));
        setMore(res.entries.length === 50);
      },
      () => {
        if (before === 0) {
          setEntries('unreachable');
        }
      },
    ),
    [onSessionExpired],
  );

  useEffect(() => {
    void load(0);
  }, [load]);

  async function runCheck(): Promise<void> {
    setCheck('checking');
    const res = await checkAudit();
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    setCheck(res);
  }

  const last = Array.isArray(entries) && entries.length > 0 ? entries[entries.length - 1].seq : 0;
  return (
    <section aria-label="Audit log" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Audit log</h3>
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => void runCheck()}
          disabled={check === 'checking'}
          className="min-h-11 rounded-md border border-muted px-3 text-body text-text"
        >
          Check the log
        </button>
        <a href={auditExportURL} download className="flex min-h-11 items-center rounded-md border border-muted px-3 text-body text-text">
          Export as JSON lines
        </a>
      </div>
      {check === 'checking' && <p className="text-body text-muted">Checking every entry…</p>}
      {check === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t check the log. Try again.
        </p>
      )}
      {check !== null && typeof check === 'object' && (
        <p role="status" className="text-body text-text">
          {check.ok
            ? `All ${check.checked} entries are intact.`
            : `Entry ${check.brokenAt} ${check.reason}. The log was changed outside Devcules.`}
        </p>
      )}
      {entries === 'loading' && <p className="text-body text-muted">Loading the audit log…</p>}
      {entries === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t load the audit log. Try again.
        </p>
      )}
      {Array.isArray(entries) && (
        <ul className="flex flex-col">
          {entries.map((e) => (
            <li key={e.seq} className="flex flex-col border-b border-muted py-2">
              <span className="text-body text-text">
                {actionLabels[e.action] ?? e.action}
                {e.target && <span className="text-muted"> · {e.target}</span>}
              </span>
              <span className="text-body text-muted">
                {e.actor} · {when(e.at)}
                {e.ip && ` · ${e.ip}`}
                {e.detail &&
                  Object.entries(e.detail)
                    .map(([k, v]) => ` · ${k} ${v}`)
                    .join('')}
              </span>
            </li>
          ))}
        </ul>
      )}
      {more && (
        <button
          type="button"
          onClick={() => void load(last)}
          className="min-h-11 self-start rounded-md border border-muted px-3 text-body text-text"
        >
          Show older
        </button>
      )}
    </section>
  );
}
