import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import type { Approval } from '../api';
import { Inbox, RecentApprovals } from './Approvals';

type Reply = { status: number; body?: unknown } | 'reject';

// serve answers fetches by method and path, one reply per call; the
// last reply repeats.
function serve(replies: Record<string, Reply[]>): Mock {
  const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const queue = replies[key];
    if (!queue || queue.length === 0) {
      throw new Error(`unexpected fetch ${key}`);
    }
    const r = queue.length > 1 ? queue.shift()! : queue[0];
    if (r === 'reject') {
      throw new TypeError('network down');
    }
    return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

function approval(over: Partial<Approval> = {}): Approval {
  return {
    id: 'a1',
    launchId: 'l1',
    runner: 'laptop',
    agent: 'Claude Code',
    tool: 'Bash',
    command: 'git push --force origin main',
    rule: 'force-push',
    ruleName: 'Force push',
    irreversible: false,
    status: 'pending',
    created: new Date().toISOString(),
    expires: new Date(Date.now() + 9.5 * 60_000).toISOString(),
    ...over,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Inbox', () => {
  it('is calm when nothing waits', () => {
    render(<Inbox pending={[]} focus={null} onAnswered={() => {}} onSessionExpired={() => {}} />);
    expect(screen.getByText('Nothing needs you.')).toBeTruthy();
  });

  it('says so when serve is unreachable', () => {
    render(<Inbox pending="unreachable" focus={null} onAnswered={() => {}} onSessionExpired={() => {}} />);
    expect(screen.getByText("Can't reach Devcules.")).toBeTruthy();
  });

  it('shows a waiting approval with its command and deadline', () => {
    render(<Inbox pending={[approval()]} focus={null} onAnswered={() => {}} onSessionExpired={() => {}} />);
    expect(screen.getByRole('heading', { name: '1 approval needs you' })).toBeTruthy();
    const card = screen.getByRole('listitem', { name: 'Force push from Claude Code on laptop' });
    expect(within(card).getByText('git push --force origin main')).toBeTruthy();
    expect(within(card).getByText('Claude Code on laptop wants to run (Bash):')).toBeTruthy();
    expect(within(card).getByText('Denied in 10 minutes if unanswered')).toBeTruthy();
  });

  it('names calls with no rule and counts several', () => {
    render(
      <Inbox
        pending={[
          approval({ id: 'a1', ruleName: undefined, irreversible: true, runner: '', agent: '' }),
          approval({ id: 'a2', ruleName: undefined, expires: new Date(Date.now() + 30_000).toISOString() }),
          approval({ id: 'a3', expires: new Date(Date.now() - 1000).toISOString(), command: '' }),
        ]}
        focus={null}
        onAnswered={() => {}}
        onSessionExpired={() => {}}
      />,
    );
    expect(screen.getByRole('heading', { name: '3 approvals need you' })).toBeTruthy();
    expect(screen.getByRole('listitem', { name: 'An irreversible call from An agent' })).toBeTruthy();
    expect(screen.getByRole('listitem', { name: 'A tool call from Claude Code on laptop' })).toBeTruthy();
    expect(screen.getByText('Denied in under a minute if unanswered')).toBeTruthy();
    expect(screen.getByText('Expiring now')).toBeTruthy();
    expect(screen.getByText('(no command text)')).toBeTruthy();
  });

  it('allows once and refreshes', async () => {
    const fn = serve({ 'POST /api/approvals/a1': [{ status: 200, body: approval({ status: 'allowed' }) }] });
    const answered = vi.fn();
    const user = userEvent.setup();
    render(<Inbox pending={[approval()]} focus={null} onAnswered={answered} onSessionExpired={() => {}} />);
    await user.click(screen.getByRole('button', { name: 'Allow once' }));
    await waitFor(() => expect(answered).toHaveBeenCalled());
    const [, init] = fn.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ decision: 'allow' });
  });

  it('denies, and says when another device answered first', async () => {
    const fn = serve({ 'POST /api/approvals/a1': [{ status: 409, body: { error: 'already answered' } }] });
    const answered = vi.fn();
    const user = userEvent.setup();
    render(<Inbox pending={[approval()]} focus={null} onAnswered={answered} onSessionExpired={() => {}} />);
    await user.click(screen.getByRole('button', { name: 'Deny' }));
    expect((await screen.findByRole('alert')).textContent).toBe('That approval was already answered or expired.');
    expect(answered).toHaveBeenCalled();
    const [, init] = fn.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ decision: 'deny' });
  });

  it('keeps the card when the answer did not land', async () => {
    serve({ 'POST /api/approvals/a1': ['reject'] });
    const answered = vi.fn();
    const user = userEvent.setup();
    render(<Inbox pending={[approval()]} focus={null} onAnswered={answered} onSessionExpired={() => {}} />);
    await user.click(screen.getByRole('button', { name: 'Allow once' }));
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't send your answer. Try again.");
    expect(answered).not.toHaveBeenCalled();
  });

  it('returns to sign-in on a 401', async () => {
    serve({ 'POST /api/approvals/a1': [{ status: 401 }] });
    const expired = vi.fn();
    const user = userEvent.setup();
    render(<Inbox pending={[approval()]} focus={null} onAnswered={() => {}} onSessionExpired={expired} />);
    await user.click(screen.getByRole('button', { name: 'Deny' }));
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });

  it('marks the approval a notice linked to', () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    render(
      <Inbox
        pending={[approval({ id: 'a1' }), approval({ id: 'a2', ruleName: 'Elevate' })]}
        focus="a2"
        onAnswered={() => {}}
        onSessionExpired={() => {}}
      />,
    );
    expect(screen.getByRole('listitem', { name: 'Elevate from Claude Code on laptop' }).className).toContain('border-2');
    expect(scroll).toHaveBeenCalled();
  });

  it('says how a linked approval ended', async () => {
    serve({
      'GET /api/approvals?limit=200': [
        {
          status: 200,
          body: {
            approvals: [approval({ id: 'old', status: 'allowed', answeredBy: 'admin', answered: '2026-10-29T08:00:00Z' })],
            pending: 0,
          },
        },
      ],
    });
    render(<Inbox pending={[]} focus="old" onAnswered={() => {}} onSessionExpired={() => {}} />);
    expect((await screen.findByRole('status')).textContent).toMatch(/^Force push from Claude Code on laptop: allowed by admin at /);
  });
});

describe('RecentApprovals', () => {
  it('lists answered approvals only', async () => {
    serve({
      'GET /api/approvals?limit=50': [
        {
          status: 200,
          body: {
            approvals: [
              approval({ id: 'p', command: 'waiting one' }),
              approval({ id: 'd', status: 'denied', answeredBy: 'admin', command: 'rm -rf /' }),
              approval({ id: 'e', status: 'expired', ruleName: undefined, command: 'shutdown now' }),
            ],
            pending: 1,
          },
        },
      ],
    });
    render(<RecentApprovals onSessionExpired={() => {}} />);
    expect(await screen.findByText('Denied · Force push')).toBeTruthy();
    expect(screen.getByText('Expired · A tool call')).toBeTruthy();
    expect(screen.queryByText('waiting one')).toBeNull();
  });

  it('says when there are none', async () => {
    serve({ 'GET /api/approvals?limit=50': [{ status: 200, body: { approvals: [], pending: 0 } }] });
    render(<RecentApprovals onSessionExpired={() => {}} />);
    expect(await screen.findByText('No answered approvals yet.')).toBeTruthy();
  });

  it('says when it cannot load', async () => {
    serve({ 'GET /api/approvals?limit=50': ['reject'] });
    render(<RecentApprovals onSessionExpired={() => {}} />);
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't load approvals. Try again.");
  });

  it('returns to sign-in on a 401', async () => {
    serve({ 'GET /api/approvals?limit=50': [{ status: 401 }] });
    const expired = vi.fn();
    render(<RecentApprovals onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
