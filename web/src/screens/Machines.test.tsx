import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Machines from './Machines';

// The terminal itself needs a real canvas; here it only has to show
// which terminal it was opened for.
vi.mock('./TerminalView', () => ({
  default: (p: { id: string; where?: { machine?: string } }) => (
    <div>
      terminal {p.id} on {p.where?.machine}
    </div>
  ),
}));

type Reply = { status: number; body?: unknown };

function reply(r: Reply): Response {
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

type Handler = (key: string, init?: RequestInit) => Reply | undefined;

// serve answers by "METHOD path"; an unexpected call fails the test.
function serve(handler: Handler): ReturnType<typeof vi.fn> {
  const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const r = handler(key, init);
    if (!r) {
      throw new Error(`unexpected fetch ${key}`);
    }
    return reply(r);
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

const two = {
  machines: [
    { name: 'kitchen-mac', online: true, enrolledAt: '2026-10-29T10:00:00Z', lastSeen: '2026-10-29T10:00:00Z' },
    { name: 'old-laptop', online: false, enrolledAt: '2026-10-20T10:00:00Z' },
  ],
  budget: 40,
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Machines', () => {
  it('lists online and offline machines with actions on the online one', async () => {
    serve((key) => {
      if (key === 'GET /api/machines') return { status: 200, body: two };
      if (key === 'GET /api/terminals?machine=kitchen-mac') {
        return { status: 200, body: { terminals: [{ term: 'aaaaaaaa00000000', created: 1, dir: '/Users/k/shop' }] } };
      }
      return undefined;
    });
    render(<Machines onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('kitchen-mac')).toBeDefined();
    expect(screen.getByText('Online')).toBeDefined();
    expect(screen.getByText('Offline, never linked')).toBeDefined();
    expect(screen.getByRole('button', { name: 'Open a terminal on kitchen-mac' })).toBeDefined();
    expect(screen.getByRole('button', { name: 'See the screen of kitchen-mac' })).toBeDefined();
    expect(screen.queryByRole('button', { name: 'Open a terminal on old-laptop' })).toBeNull();
    expect(await screen.findByText('aaaaaaaa · /Users/k/shop')).toBeDefined();
  });

  it('opens a terminal on a machine', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/machines') return { status: 200, body: two };
      if (key === 'GET /api/terminals?machine=kitchen-mac') return { status: 200, body: { terminals: [] } };
      if (key === 'POST /api/terminals?machine=kitchen-mac') return { status: 201, body: { id: 'bbbbbbbb00000000' } };
      return undefined;
    });
    render(<Machines onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Open a terminal on kitchen-mac' }));
    expect(await screen.findByText('terminal bbbbbbbb00000000 on kitchen-mac')).toBeDefined();
  });

  it('adds a computer with a one-time code and sees it link', async () => {
    const user = userEvent.setup();
    let enrolled = false;
    let body = '';
    serve((key, init) => {
      if (key === 'GET /api/machines') {
        return {
          status: 200,
          body: { machines: enrolled ? [{ name: 'studio', online: true }] : [], budget: 40 },
        };
      }
      if (key === 'GET /api/terminals?machine=studio') return { status: 200, body: { terminals: [] } };
      if (key === 'POST /api/machines/enrol-code') {
        body = String(init?.body);
        return { status: 201, body: { code: 'ABCD-EFGH-JKMN', expiresIn: 900 } };
      }
      return undefined;
    });
    render(<Machines onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('No computers yet.')).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Add a computer' }));
    await user.type(screen.getByLabelText('Which computer is it'), 'studio Mac');
    await user.click(screen.getByRole('button', { name: 'Get a code' }));
    expect(await screen.findByText('ABCD-EFGH-JKMN')).toBeDefined();
    expect(body).toBe('{"label":"studio Mac"}');
    expect(screen.getByText(`DEVCULES_SERVE_URL=${window.location.origin} devcules machine enrol ABCD-EFGH-JKMN`)).toBeDefined();
    expect(screen.getByText('devcules machine')).toBeDefined();
    enrolled = true;
    expect(await screen.findByText(/studio is linked/, {}, { timeout: 7000 })).toBeDefined();
  }, 10000);

  it('removes a machine after a confirm', async () => {
    const user = userEvent.setup();
    let removed = false;
    serve((key) => {
      if (key === 'GET /api/machines') return { status: 200, body: removed ? { machines: [], budget: 40 } : two };
      if (key === 'GET /api/terminals?machine=kitchen-mac') return { status: 200, body: { terminals: [] } };
      if (key === 'DELETE /api/machines/old-laptop') {
        removed = true;
        return { status: 204 };
      }
      return undefined;
    });
    render(<Machines onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Remove old-laptop' }));
    expect(screen.getByText(/revokes its key/)).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Confirm remove old-laptop' }));
    await waitFor(() => expect(removed).toBe(true));
    expect(await screen.findByText('No computers yet.')).toBeDefined();
  });

  it('shows the empty state with machines not configured', async () => {
    serve((key) => (key === 'GET /api/machines' ? { status: 503 } : undefined));
    render(<Machines onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('No computers yet.')).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    serve((key) => (key === 'GET /api/machines' ? { status: 401 } : undefined));
    const expired = vi.fn();
    render(<Machines onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
