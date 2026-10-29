import { useEffect, useRef, useState } from 'react';
import { answerApproval, listApprovals, type Approval } from '../api';
import { Empty } from './Empty';

export type PendingState = Approval[] | 'loading' | 'unreachable';

const statusLabels: Record<string, string> = {
  pending: 'Waiting',
  allowed: 'Allowed',
  denied: 'Denied',
  expired: 'Expired',
};

function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

// left says how long an approval still waits, in whole minutes.
function left(iso: string, now: number): string {
  const ms = new Date(iso).getTime() - now;
  if (Number.isNaN(ms)) {
    return '';
  }
  if (ms <= 0) {
    return 'Expiring now';
  }
  const min = Math.ceil(ms / 60_000);
  return min === 1 ? 'Denied in under a minute if unanswered' : `Denied in ${min} minutes if unanswered`;
}

function what(a: Approval): string {
  if (a.ruleName) {
    return a.ruleName;
  }
  return a.irreversible ? 'An irreversible call' : 'A tool call';
}

function who(a: Approval): string {
  const agent = a.agent || 'An agent';
  return a.runner ? `${agent} on ${a.runner}` : agent;
}

// Inbox is Home: what needs the owner now. Pending approvals come
// from the shell's poll (it also drives the tab badge); answering one
// refreshes that poll. focus is an approval a notice linked to.
export function Inbox({
  pending,
  focus,
  onAnswered,
  onSessionExpired,
}: {
  pending: PendingState;
  focus: string | null;
  onAnswered: () => void;
  onSessionExpired: () => void;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [linked, setLinked] = useState<Approval | null>(null);
  const focused = useRef<HTMLLIElement | null>(null);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15_000);
    return () => window.clearInterval(timer);
  }, []);

  // A notice may link to an approval that is no longer pending: say
  // how it ended instead of showing nothing.
  const missing = focus !== null && Array.isArray(pending) && !pending.some((a) => a.id === focus);
  useEffect(() => {
    if (!missing || focus === null) {
      return;
    }
    let live = true;
    void listApprovals('', 200).then(
      (res) => {
        if (!live || typeof res === 'string') {
          return;
        }
        setLinked(res.approvals.find((a) => a.id === focus) ?? null);
      },
      () => {},
    );
    return () => {
      live = false;
    };
  }, [missing, focus]);

  useEffect(() => {
    focused.current?.scrollIntoView?.({ block: 'center' });
  }, [focus, pending]);

  async function answer(a: Approval, allow: boolean): Promise<void> {
    if (busy) {
      return;
    }
    setBusy(a.id);
    setNotice(null);
    try {
      const res = await answerApproval(a.id, allow);
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (res === 'gone') {
        setNotice('That approval was already answered or expired.');
      } else if (res === 'unreachable') {
        setNotice("Couldn't send your answer. Try again.");
        return;
      }
      onAnswered();
    } finally {
      setBusy(null);
    }
  }

  const linkedNote =
    missing && linked && linked.status !== 'pending' ? (
      <p role="status" className="text-body text-muted">
        {what(linked)} from {who(linked)}: {statusLabels[linked.status]?.toLowerCase() ?? linked.status}
        {linked.answeredBy ? ` by ${linked.answeredBy}` : ''}
        {linked.answered ? ` at ${when(linked.answered)}` : ''}.
      </p>
    ) : null;

  if (!Array.isArray(pending) || pending.length === 0) {
    return (
      <div className="flex flex-1 flex-col">
        {pending === 'unreachable' ? (
          <Empty title="Can't reach Devcules." body="Approvals will show here once it answers again." />
        ) : (
          <Empty title="Nothing needs you." body="Approvals and agent updates will land here." />
        )}
        {(linkedNote || notice) && (
          <div className="flex flex-col gap-2 px-6 pb-6">
            {linkedNote}
            {notice && (
              <p role="alert" className="text-body text-text">
                {notice}
              </p>
            )}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col gap-4 px-6 pt-8 pb-6">
      <h2 className="text-lead font-semibold text-text">
        {pending.length === 1 ? '1 approval needs you' : `${pending.length} approvals need you`}
      </h2>
      {linkedNote}
      <ul aria-label="Waiting approvals" className="flex flex-col gap-3">
        {pending.map((a) => (
          <li
            key={a.id}
            ref={a.id === focus ? focused : undefined}
            aria-label={`${what(a)} from ${who(a)}`}
            className={`flex flex-col gap-2 rounded-md border bg-surface px-3 py-3 ${
              a.id === focus ? 'border-2 border-accent' : 'border-accent'
            }`}
          >
            <span className="text-body font-semibold text-text">{what(a)}</span>
            <span className="text-body text-muted">
              {who(a)} wants to run{a.tool ? ` (${a.tool})` : ''}:
            </span>
            <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md border border-muted px-2 py-1 font-mono text-body text-text">
              {a.command || '(no command text)'}
            </pre>
            <span className="text-body text-muted">{left(a.expires, now)}</span>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => void answer(a, false)}
                disabled={busy !== null}
                className="min-h-11 flex-1 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150 disabled:opacity-60"
              >
                Deny
              </button>
              <button
                type="button"
                onClick={() => void answer(a, true)}
                disabled={busy !== null}
                className="min-h-11 flex-1 rounded-md bg-accent px-3 text-body font-semibold text-on-accent transition-colors duration-150 disabled:opacity-60"
              >
                Allow once
              </button>
            </div>
          </li>
        ))}
      </ul>
      {notice && (
        <p role="alert" className="text-body text-text">
          {notice}
        </p>
      )}
    </div>
  );
}

// RecentApprovals lists answered and expired approvals for Activity.
export function RecentApprovals({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [list, setList] = useState<Approval[] | 'loading' | 'unreachable'>('loading');

  useEffect(() => {
    let live = true;
    void listApprovals('', 50).then(
      (res) => {
        if (!live) {
          return;
        }
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setList(res === 'unreachable' ? res : res.approvals.filter((a) => a.status !== 'pending'));
      },
      () => {
        if (live) {
          setList('unreachable');
        }
      },
    );
    return () => {
      live = false;
    };
  }, [onSessionExpired]);

  return (
    <section aria-label="Approvals" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Approvals</h3>
      {list === 'loading' && <p className="text-body text-muted">Loading approvals…</p>}
      {list === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t load approvals. Try again.
        </p>
      )}
      {Array.isArray(list) && list.length === 0 && (
        <p className="text-body text-muted">No answered approvals yet.</p>
      )}
      {Array.isArray(list) && list.length > 0 && (
        <ul className="flex flex-col gap-2">
          {list.map((a) => (
            <li key={a.id} className="flex min-h-11 flex-col justify-center rounded-md border border-muted px-3 py-1">
              <span className="text-body text-text">
                {statusLabels[a.status] ?? a.status} · {what(a)}
              </span>
              <span className="truncate font-mono text-body text-muted">{a.command}</span>
              <span className="text-body text-muted">
                {who(a)} · {when(a.created)}
                {a.answeredBy ? ` · ${a.answeredBy}` : ''}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
