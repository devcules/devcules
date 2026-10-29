import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Projects from './Projects';

vi.mock('./TerminalView', () => ({
  default: (p: { id: string; where?: { machine?: string } }) => (
    <div>
      terminal {p.id} on {p.where?.machine ?? 'runner'}
    </div>
  ),
}));

type Reply = { status: number; body?: unknown };

function reply(r: Reply): Response {
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

function serve(handler: (key: string, init?: RequestInit) => Reply | undefined): void {
  window.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const r = handler(key, init);
    if (!r) {
      throw new Error(`unexpected fetch ${key}`);
    }
    return reply(r);
  }) as typeof fetch;
}

const shop = { id: 'p_shop', name: 'shop', kind: 'machine', target: 'studio', dir: '/Users/k/shop', createdAt: '2026-10-29T10:00:00Z' };
const api = {
  id: 'p_api',
  name: 'api',
  kind: 'runner',
  target: 'box',
  dir: 'api',
  repo: 'https://github.com/acme/api.git',
  createdAt: '2026-10-29T10:00:00Z',
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Projects', () => {
  it('lists projects and opens a terminal inside one', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [api, shop], runners: ['box'], machines: ['studio'] } };
      if (key === 'GET /api/terminals?machine=studio') {
        return {
          status: 200,
          body: { terminals: [{ term: 'cccccccc00000000', created: 1, project: 'p_shop' }, { term: 'dddddddd00000000', created: 1 }] },
        };
      }
      if (key === 'POST /api/projects/p_shop/terminals') return { status: 201, body: { id: 'eeeeeeee00000000' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('shop')).toBeDefined();
    expect(screen.getByText(/machine studio/)).toBeDefined();
    await user.click(screen.getByRole('button', { name: /shop/ }));
    // Only the project's own terminals are listed under it.
    expect(await screen.findByRole('button', { name: 'Open terminal cccccccc' })).toBeDefined();
    expect(screen.queryByRole('button', { name: 'Open terminal dddddddd' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'New terminal' }));
    expect(await screen.findByText('terminal eeeeeeee00000000 on studio')).toBeDefined();
  });

  it('starts an agent in a runner project', async () => {
    const user = userEvent.setup();
    let started = '';
    serve((key, init) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [api], runners: ['box'], machines: [] } };
      if (key === 'GET /api/terminals') return { status: 200, body: { terminals: [] } };
      if (key === 'GET /api/agents') {
        return { status: 200, body: { runner: 'box', mode: 'critical', agents: [{ id: 'musecode', name: 'Muse Code', provider: 'meta', installed: true }] } };
      }
      if (key === 'POST /api/agents') {
        started = String(init?.body);
        return { status: 201, body: { id: 'ffffffff00000000' } };
      }
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: /api/ }));
    await user.click(await screen.findByRole('button', { name: 'Start Muse Code in api' }));
    expect(await screen.findByText('terminal ffffffff00000000 on runner')).toBeDefined();
    expect(started).toBe('{"agent":"musecode","project":"p_api"}');
  });

  it('clones a repo and shows the clone terminal', async () => {
    const user = userEvent.setup();
    let posted = '';
    serve((key, init) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [], runners: ['box'], machines: ['studio'] } };
      if (key === 'POST /api/projects') {
        posted = String(init?.body);
        return { status: 201, body: { project: { ...shop, repo: 'git@github.com:acme/shop.git' }, terminal: '1111111100000000' } };
      }
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    await user.selectOptions(screen.getByLabelText('On'), 'studio (machine)');
    await user.type(screen.getByLabelText('Git URL'), 'git@github.com:acme/shop.git');
    await user.click(screen.getByRole('button', { name: 'Clone it' }));
    expect(await screen.findByText('terminal 1111111100000000 on studio')).toBeDefined();
    expect(JSON.parse(posted)).toEqual({ kind: 'machine', target: 'studio', repo: 'git@github.com:acme/shop.git' });
  });

  it('shows the server reason when adding fails', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [], runners: ['box'], machines: [] } };
      if (key === 'POST /api/projects') return { status: 409, body: { error: 'a project with this name exists' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    await user.click(screen.getByRole('button', { name: 'A folder already there' }));
    await user.type(screen.getByLabelText('Folder'), '/srv/app');
    await user.click(screen.getByRole('button', { name: 'Add it' }));
    expect(await screen.findByText('a project with this name exists')).toBeDefined();
  });

  it('asks for a runner or machine when none is online', async () => {
    const user = userEvent.setup();
    serve((key) => (key === 'GET /api/projects' ? { status: 200, body: { projects: [], runners: [], machines: [] } } : undefined));
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    expect(screen.getByText(/No runner or machine is online/)).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    serve((key) => (key === 'GET /api/projects' ? { status: 401 } : undefined));
    const expired = vi.fn();
    render(<Projects onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
