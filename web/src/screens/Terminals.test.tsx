import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Terminals from './Terminals';

// The xterm view is mocked out: its rendering belongs to e2e, and
// jsdom has no layout for the fitter. What matters here is the
// list/new/close-confirm flow around it.
vi.mock('./TerminalView', () => ({
  default: ({ id, onBack }: { id: string; onBack: () => void }) => (
    <div>
      <span>terminal-view {id}</span>
      <button type="button" onClick={onBack}>
        Back to terminals
      </button>
    </div>
  ),
}));

type Reply = { status: number; body?: unknown };

function reply(r: Reply): Response {
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

// agentReply answers /api/agents; get() answers everything else.
let agentReply: Reply = { status: 200, body: { runner: 'laptop', mode: 'critical', agents: [] } };

function get(status: number, body: unknown = null): void {
  const r: Reply = body === null ? { status } : { status, body };
  window.fetch = vi.fn(async (input: string | URL | Request) =>
    input.toString() === '/api/agents' ? reply(agentReply) : reply(r),
  ) as typeof fetch;
}

// routes answers by "METHOD path", for the agent flows.
function routes(table: Record<string, Reply>): ReturnType<typeof vi.fn> {
  const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const r = table[`${init?.method ?? 'GET'} ${input.toString()}`];
    if (!r) {
      throw new Error(`unexpected fetch ${input.toString()}`);
    }
    return reply(r);
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

const muse = { id: 'musecode', name: 'Muse Code', provider: 'meta', installed: true, version: '1.4.14' };
const absent = { id: 'musecode', name: 'Muse Code', provider: 'meta', installed: false, install: 'curl -fsSL https://dev.meta.ai/install.sh | sh' };

afterEach(() => {
  agentReply = { status: 200, body: { runner: 'laptop', mode: 'critical', agents: [] } };
});

const one = { terminals: [{ term: '0123456789abcdef', created: 1 }] };

describe('Terminals', () => {
  it('shows the calm empty state with no terminals', async () => {
    get(200, { terminals: [] });
    render(<Terminals onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
  });

  it('shows the offline state when no runner is linked', async () => {
    get(503);
    render(<Terminals onSessionExpired={vi.fn()} />);
    expect(await screen.findByText(/No runner is linked/)).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    get(401);
    const expired = vi.fn();
    render(<Terminals onSessionExpired={expired} />);
    await screen.findByText('No terminals yet.').catch(() => null);
    expect(expired).toHaveBeenCalled();
  });

  it('opens a new terminal and goes back to the list', async () => {
    const user = userEvent.setup();
    get(200, { terminals: [] });
    render(<Terminals onSessionExpired={vi.fn()} />);
    await screen.findByText('No terminals yet.');

    get(201, { id: '0123456789abcdef' });
    await user.click(screen.getByRole('button', { name: 'New terminal' }));
    expect(await screen.findByText('terminal-view 0123456789abcdef')).toBeDefined();

    get(200, { terminals: [] });
    await user.click(screen.getByRole('button', { name: 'Back to terminals' }));
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
  });

  it('closes a terminal only after confirming', async () => {
    const user = userEvent.setup();
    get(200, one);
    render(<Terminals onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Open terminal 01234567' }));

    // Opening is reversible from the list; closing asks first.
    get(200, one);
    await user.click(screen.getByRole('button', { name: 'Back to terminals' }));
    await user.click(await screen.findByRole('button', { name: 'Close terminal 01234567' }));
    expect(screen.getByRole('button', { name: 'Confirm close terminal 01234567' })).toBeDefined();

    get(204);
    get(200, { terminals: [] });
    await user.click(screen.getByRole('button', { name: 'Confirm close terminal 01234567' }));
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
  });

  it('keeps the terminal when the close is cancelled', async () => {
    const user = userEvent.setup();
    get(200, one);
    render(<Terminals onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Open terminal 01234567' }));
    get(200, one);
    await user.click(screen.getByRole('button', { name: 'Back to terminals' }));
    await user.click(await screen.findByRole('button', { name: 'Close terminal 01234567' }));
    await user.click(screen.getByRole('button', { name: 'Keep terminal' }));
    expect(
      within(screen.getByRole('list')).getByRole('button', { name: 'Open terminal 01234567' }),
    ).toBeDefined();
  });

  it('starts an installed agent in its own terminal', async () => {
    const user = userEvent.setup();
    const fn = routes({
      'GET /api/terminals': { status: 200, body: { terminals: [] } },
      'GET /api/agents': { status: 200, body: { runner: 'laptop', mode: 'critical', agents: [muse] } },
      'POST /api/agents': { status: 201, body: { id: '0123456789abcdef', launch: {} } },
    });
    render(<Terminals onSessionExpired={vi.fn()} />);
    const section = within(await screen.findByRole('region', { name: 'Start an agent' }));
    expect(section.getByText('Version 1.4.14')).toBeDefined();
    expect(screen.queryByRole('note')).toBeNull();
    await user.click(section.getByRole('button', { name: 'Start Muse Code' }));
    expect(await screen.findByText('terminal-view 0123456789abcdef')).toBeDefined();
    const [, init] = fn.mock.calls.find(([, i]) => (i as RequestInit | undefined)?.method === 'POST') as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ agent: 'musecode' });
  });

  it('shows how to install a missing agent and warns about the access mode', async () => {
    routes({
      'GET /api/terminals': { status: 200, body: { terminals: [] } },
      'GET /api/agents': { status: 200, body: { runner: 'laptop', mode: '', agents: [absent] } },
    });
    render(<Terminals onSessionExpired={vi.fn()} />);
    const section = within(await screen.findByRole('region', { name: 'Start an agent' }));
    expect(section.getByText('Not installed on laptop')).toBeDefined();
    expect(section.getByText(absent.install)).toBeDefined();
    expect(section.queryByRole('button', { name: 'Start Muse Code' })).toBeNull();
    expect(section.getByRole('note').textContent).toMatch(/No access mode is set for laptop/);
  });

  it('notes off and full access modes', async () => {
    for (const [mode, want] of [
      ['off', /set to Off/],
      ['full', /full access/],
    ] as const) {
      routes({
        'GET /api/terminals': { status: 200, body: { terminals: [] } },
        'GET /api/agents': { status: 200, body: { runner: 'laptop', mode, agents: [muse] } },
      });
      const { unmount } = render(<Terminals onSessionExpired={vi.fn()} />);
      expect((await screen.findByRole('note')).textContent).toMatch(want);
      unmount();
    }
  });

  it('says why an agent did not start', async () => {
    const user = userEvent.setup();
    routes({
      'GET /api/terminals': { status: 200, body: { terminals: [] } },
      'GET /api/agents': { status: 200, body: { runner: 'laptop', mode: 'critical', agents: [muse] } },
      'POST /api/agents': { status: 409, body: { error: 'Muse Code needs the meta provider, which the router does not serve' } },
    });
    render(<Terminals onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Start Muse Code' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      "Couldn't start it: Muse Code needs the meta provider, which the router does not serve.",
    );
    routes({
      'GET /api/terminals': { status: 200, body: { terminals: [] } },
      'GET /api/agents': { status: 200, body: { runner: 'laptop', mode: 'critical', agents: [muse] } },
      'POST /api/agents': { status: 504 },
    });
    await user.click(screen.getByRole('button', { name: 'Start Muse Code' }));
    expect((await screen.findByRole('alert')).textContent).toBe('The runner did not answer. Try again.');
  });

  it('names agent terminals in the list', async () => {
    routes({
      'GET /api/terminals': {
        status: 200,
        body: { terminals: [{ term: '0123456789abcdef', created: 1, agent: 'musecode' }, { term: 'fedcba9876543210', created: 2, agent: 'gone' }] },
      },
      'GET /api/agents': { status: 200, body: { runner: 'laptop', mode: 'critical', agents: [muse] } },
    });
    render(<Terminals onSessionExpired={vi.fn()} />);
    expect(await screen.findByRole('button', { name: 'Open terminal Muse Code · 01234567' })).toBeDefined();
    expect(screen.getByRole('button', { name: 'Open terminal gone · fedcba98' })).toBeDefined();
  });

  it('hides the agent panel when the list fails', async () => {
    agentReply = { status: 500 };
    get(200, { terminals: [] });
    render(<Terminals onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
    expect(screen.queryByRole('region', { name: 'Start an agent' })).toBeNull();
  });
});
