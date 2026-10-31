import { useCallback, useEffect, useRef, useState } from 'react';
import { chatAssistant, listAssistants, type AssistantEvent, type AssistantSummary } from '../api';

// Assistant is the setup-helper chat: the picker row of assistants
// (today the built-in one) then the conversation against it. A turn
// is sent whole and answered with its events in order; a tool call
// shows its gate verdict and who approved it, so the change it made
// reads with its receipt.
export default function Assistant({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [list, setList] = useState<AssistantSummary[] | 'loading' | 'unreachable'>('loading');
  const [chosen, setChosen] = useState<AssistantSummary | null>(null);

  useEffect(() => {
    void listAssistants().then((res) => {
      if (typeof res === 'string') {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setList('unreachable');
        return;
      }
      setList(res.assistants);
    });
  }, [onSessionExpired]);

  if (chosen) {
    return (
      <Chat assistant={chosen} onBack={() => setChosen(null)} onSessionExpired={onSessionExpired} />
    );
  }

  return (
    <section aria-label="Assistants" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Assistant</h3>
      <p className="text-body text-muted">
        Ask about this Devcules setup or let the helper inspect and fix it. Every change is gated
        exactly like an agent's tool call.
      </p>
      {list === 'loading' && <p className="text-body text-muted">Loading…</p>}
      {list === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn't reach the server. Try again.
        </p>
      )}
      {Array.isArray(list) && list.length === 0 && (
        <p className="text-body text-muted">No assistant is wired on this desk yet.</p>
      )}
      {Array.isArray(list) && list.length > 0 && (
        <ul aria-label="Assistants" className="flex flex-col gap-2">
          {list.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                onClick={() => setChosen(a)}
                className="flex min-h-11 w-full flex-col items-start gap-0.5 rounded-md border border-muted px-3 py-1 text-left"
              >
                <span className="text-body font-semibold text-text">{a.name}</span>
                {a.state && <span className="text-body text-muted">{a.state}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

type Row = { kind: 'user' | 'assistant' | 'tool'; text: string; event?: AssistantEvent };

function Chat({
  assistant,
  onBack,
  onSessionExpired,
}: {
  assistant: AssistantSummary;
  onBack: () => void;
  onSessionExpired: () => void;
}) {
  const [session, setSession] = useState('');
  const [rows, setRows] = useState<Row[]>([]);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const endRef = useRef<HTMLLIElement>(null);

  const append = useCallback((rs: Row[]) => {
    setRows((prev) => [...prev, ...rs]);
  }, []);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'end' });
  }, [rows]);

  async function send(): Promise<void> {
    const message = draft.trim();
    if (!message || busy) {
      return;
    }
    setBusy(true);
    setNotice(null);
    append([{ kind: 'user', text: message }]);
    setDraft('');
    const res = await chatAssistant(assistant.id, message, session);
    if (typeof res === 'string') {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (res === 'blocked') {
        append([
          { kind: 'assistant', text: 'That message was blocked by the redaction policy, so it was never sent.' },
        ]);
      } else {
        setNotice("Couldn't reach the server. Your message was not answered; try again.");
      }
      setBusy(false);
      return;
    }
    setSession(res.session);
    append(
      res.events.map((e): Row => {
        if (e.type === 'assistant.tool' && e.tool) {
          return { kind: 'tool', text: e.tool.result, event: e };
        }
        return { kind: 'assistant', text: e.text ?? '', event: e };
      }),
    );
    setBusy(false);
  }

  return (
    <section aria-label={`Chat with ${assistant.name}`} className="flex h-full flex-col gap-3">
      <div className="flex items-center gap-2">
        <button type="button" onClick={onBack} className="min-h-11 rounded-md px-3 text-body text-muted">
          ← Assistants
        </button>
        <h3 className="text-body font-semibold text-text">{assistant.name}</h3>
      </div>
      {assistant.state && <p className="text-body text-muted">{assistant.state}.</p>}

      <ul aria-label="Conversation" className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
        {rows.length === 0 && (
          <li className="text-body text-muted">
            Ask about the setup, or something like “why did my launch get denied?”.
          </li>
        )}
        {rows.map((r, i) => (
          <ChatRow key={i} row={r} />
        ))}
        <li ref={endRef} aria-hidden="true" />
      </ul>

      {notice && (
        <p role="alert" className="text-body text-text">
          {notice}
        </p>
      )}

      <form
        className="flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <label className="flex-1">
          <span className="sr-only">Message</span>
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={2}
            placeholder="Ask the helper…"
            className="w-full rounded-md border border-muted bg-transparent px-3 py-2 text-body text-text"
          />
        </label>
        <button
          type="submit"
          disabled={busy || draft.trim() === ''}
          className="min-h-11 rounded-md bg-action px-4 text-body font-semibold text-background disabled:opacity-50"
        >
          {busy ? 'Sending…' : 'Send'}
        </button>
      </form>
    </section>
  );
}

function ChatRow({ row }: { row: Row }) {
  if (row.kind === 'user') {
    return <li className="self-end rounded-md bg-action px-3 py-2 text-body text-background">{row.text}</li>;
  }
  if (row.kind === 'assistant') {
    return <li className="rounded-md border border-muted px-3 py-2 text-body text-text">{row.text}</li>;
  }
  const t = row.event?.tool;
  return (
    <li className="rounded-md border border-dashed border-muted px-3 py-2 text-body text-muted">
      <span className="font-semibold text-text">{t?.name ?? 'tool'}</span>
      {t && (
        <span aria-label={decisionLabel(t.decision)} title={decisionLabel(t.decision)}>
          {' '}
          · {decisionBadge(t.decision)}
        </span>
      )}
      {t?.approvedBy && <span> · by {t.approvedBy}</span>}
      <br />
      <span className={row.text ? '' : 'italic'}>{row.text || '(no result text)'}</span>
    </li>
  );
}

function decisionBadge(decision: string): string {
  switch (decision) {
    case 'allow':
      return '✓ ran';
    case 'ask':
      return '✓ approved';
    case 'deny':
      return '✗ declined';
    default:
      return decision;
  }
}

function decisionLabel(decision: string): string {
  switch (decision) {
    case 'allow':
      return 'the policy allowed this call';
    case 'ask':
      return 'the owner approved this call';
    case 'deny':
      return 'this call was declined';
    default:
      return decision;
  }
}
