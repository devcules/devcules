import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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

type Seen = { key: string; body: string };

// serve answers by "METHOD path" and records every call with its body.
function serve(handler: (key: string, body: string) => Reply | undefined): Seen[] {
  const seen: Seen[] = [];
  window.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const body = typeof init?.body === 'string' ? init.body : '';
    seen.push({ key, body });
    const r = handler(key, body);
    if (!r) {
      throw new Error(`unexpected fetch ${key}`);
    }
    return reply(r);
  }) as typeof fetch;
  return seen;
}

const shop = { id: 'p_shop', name: 'shop', kind: 'machine', target: 'studio', dir: '/Users/k/shop', git: true, createdAt: '2026-10-29T10:00:00Z' };
const api = { id: 'p_api', name: 'api', kind: 'runner', target: 'box', dir: '/srv/api', createdAt: '2026-10-29T10:00:00Z' };
const listed = { projects: [api, shop], runners: ['box'], machines: ['studio'], host: '', others: ['box'] };
const agents = {
  runner: 'box',
  mode: 'critical',
  agents: [
    { id: 'musecode', name: 'Muse Code', provider: 'meta', providers: ['meta'], installed: true },
    { id: 'claudecode', name: 'Claude Code', provider: 'anthropic', providers: ['anthropic', 'meta'], installed: true },
    { id: 'codex', name: 'Codex', provider: 'openai', providers: ['openai', 'meta'], installed: false },
  ],
};
// Only Meta has a key: Claude Code's own provider is off.
const providers = { providers: [{ id: 'anthropic', ready: false }, { id: 'meta', ready: true }, { id: 'openai', ready: false }] };

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Projects', () => {
  it('lists each project with its own terminals and opens one', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/approvals/settings') {
        return { status: 200, body: { targets: [{ target: 'runner:box', runner: 'box', linked: true, mode: 'critical' }], modes: [], rules: [], timeoutSeconds: 60 } };
      }
      if (key === 'GET /api/projects') return { status: 200, body: listed };
      if (key === 'GET /api/terminals?runner=box') return { status: 200, body: { terminals: [{ term: 'aaaaaaaa00000000', created: 1, project: 'p_api', name: 'server' }] } };
      if (key === 'GET /api/terminals?machine=studio') {
        return {
          status: 200,
          body: { terminals: [{ term: 'cccccccc00000000', created: 1, project: 'p_shop', agent: 'claudecode' }, { term: 'dddddddd00000000', created: 1 }] },
        };
      }
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    const shopTerms = within(await screen.findByRole('list', { name: 'Terminals in shop' }));
    expect(await shopTerms.findByRole('button', { name: /Open terminal claudecode/ })).toBeDefined();
    // A terminal outside any project is not filed under one.
    expect(screen.queryByRole('button', { name: /dddddddd/ })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Open terminal server in api' }));
    expect(await screen.findByText('terminal aaaaaaaa00000000 on runner')).toBeDefined();
  });

  it('opens a named shell from a project +', async () => {
    const user = userEvent.setup();
    const seen = serve((key) => {
      if (key === 'GET /api/approvals/settings') {
        return { status: 200, body: { targets: [{ target: 'runner:box', runner: 'box', linked: true, mode: 'critical' }], modes: [], rules: [], timeoutSeconds: 60 } };
      }
      if (key === 'GET /api/projects') return { status: 200, body: listed };
      if (key.startsWith('GET /api/terminals')) return { status: 200, body: { terminals: [] } };
      if (key === 'POST /api/projects/p_shop/terminals') return { status: 201, body: { id: 'eeeeeeee00000000' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'New terminal in shop' }));
    // On a machine, only a shell runs.
    expect(screen.getByText(/on a machine a terminal is a shell/)).toBeDefined();
    await user.type(screen.getByLabelText('Name (optional)'), 'build');
    await user.click(screen.getByRole('button', { name: 'Open shell' }));
    expect(await screen.findByText('terminal eeeeeeee00000000 on studio')).toBeDefined();
    expect(seen.find((s) => s.key === 'POST /api/projects/p_shop/terminals')?.body).toBe('{"name":"build"}');
  });

  it('starts Claude Code on Meta with a model from the list', async () => {
    const user = userEvent.setup();
    const seen = serve((key) => {
      if (key === 'GET /api/approvals/settings') {
        return { status: 200, body: { targets: [{ target: 'runner:box', runner: 'box', linked: true, mode: 'critical' }], modes: [], rules: [], timeoutSeconds: 60 } };
      }
      if (key === 'GET /api/projects') return { status: 200, body: listed };
      if (key.startsWith('GET /api/terminals')) return { status: 200, body: { terminals: [] } };
      if (key === 'GET /api/agents?runner=box') return { status: 200, body: agents };
      if (key === 'GET /api/providers') return { status: 200, body: providers };
      if (key === 'GET /api/providers/meta/models') return { status: 200, body: { models: ['muse-lite', 'muse-spark-1.3'] } };
      if (key === 'POST /api/agents?runner=box') return { status: 201, body: { id: 'ffffffff00000000' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'New terminal in api' }));
    const runs = await screen.findByLabelText('Runs');
    await waitFor(() => expect(within(runs).getByRole('option', { name: 'Claude Code' })).toBeDefined());
    // Codex is not installed: it is not offered.
    expect(within(runs).queryByRole('option', { name: 'Codex' })).toBeNull();
    await user.selectOptions(runs, 'claudecode');
    // Anthropic has no key, so Meta is picked and Anthropic is off.
    expect(screen.getByRole('button', { name: /Anthropic \(its own\) · no key/ }).hasAttribute('disabled')).toBe(true);
    expect(screen.getByRole('button', { name: 'Meta' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByText(/add its key in Settings/)).toBeDefined();
    const model = await screen.findByLabelText('Model');
    // Off its own provider a model is required.
    await user.click(screen.getByRole('button', { name: 'Start Claude Code' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/Pick a model: Claude Code on Meta/);
    await user.selectOptions(model, 'muse-spark-1.3');
    await user.type(screen.getByLabelText('Name (optional)'), 'review');
    await user.click(screen.getByRole('button', { name: 'Start Claude Code' }));
    expect(await screen.findByText('terminal ffffffff00000000 on runner')).toBeDefined();
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/agents?runner=box')?.body ?? '{}')).toEqual({
      agent: 'claudecode',
      project: 'p_api',
      provider: 'meta',
      model: 'muse-spark-1.3',
      name: 'review',
    });
  });

  it('starts Muse on its own login when no profile has a key', async () => {
    const user = userEvent.setup();
    const seen = serve((key) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [api], runners: ['box'], machines: [], host: '', others: ['box'] } };
      if (key.startsWith('GET /api/terminals')) return { status: 200, body: { terminals: [] } };
      if (key === 'GET /api/agents?runner=box') {
        return {
          status: 200,
          body: { runner: 'box', mode: 'critical', agents: [{ id: 'musecode', name: 'Muse Code', provider: 'meta', providers: ['meta'], selfAuth: true, installed: true }] },
        };
      }
      if (key === 'GET /api/providers') return { status: 200, body: { providers: [{ id: 'meta', ready: false }] } };
      if (key === 'POST /api/agents?runner=box') return { status: 201, body: { id: 'ffffffff00000000' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'New terminal in api' }));
    await user.selectOptions(screen.getByLabelText('Runs'), 'musecode');
    // The own-login choice is offered, preselected, and Start works —
    // the picker used to dead-end on a disabled, keyless provider.
    expect(screen.getByRole('button', { name: /Muse Code's own login/ }).getAttribute('aria-pressed')).toBe('true');
    const start = screen.getByRole('button', { name: 'Start Muse Code' });
    expect(start.hasAttribute('disabled')).toBe(false);
    await user.click(start);
    expect(await screen.findByText('terminal ffffffff00000000 on runner')).toBeDefined();
    // No provider is sent (empty fields are dropped), which is what
    // makes the server take the own-login path.
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/agents?runner=box')?.body ?? '{}')).toEqual({
      agent: 'musecode',
      project: 'p_api',
    });
  });

  it('starts Codex on a saved profile with its default model, no model picked', async () => {
    const user = userEvent.setup();
    const seen = serve((key) => {
      if (key === 'GET /api/approvals/settings') {
        return { status: 200, body: { targets: [{ target: 'runner:box', runner: 'box', linked: true, mode: 'critical' }], modes: [], rules: [], timeoutSeconds: 60 } };
      }
      if (key === 'GET /api/projects') return { status: 200, body: listed };
      if (key.startsWith('GET /api/terminals')) return { status: 200, body: { terminals: [] } };
      if (key === 'GET /api/agents?runner=box') {
        return {
          status: 200,
          body: { ...agents, agents: [{ id: 'codex', name: 'Codex', provider: 'openai', providers: ['nebius', 'meta'], installed: true }] },
        };
      }
      if (key === 'GET /api/providers') {
        return {
          status: 200,
          body: {
            providers: [
              { id: 'meta', name: 'Meta', format: 'openai', ready: true },
              { id: 'nebius', name: 'Nebius', format: 'openai', model: 'gpt-oss-120b', default: true, ready: true },
            ],
          },
        };
      }
      if (key === 'GET /api/providers/nebius/models') return { status: 200, body: { models: ['gpt-oss-120b'] } };
      if (key === 'POST /api/agents?runner=box') return { status: 201, body: { id: 'abababab00000000' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'New terminal in api' }));
    const runs = await screen.findByLabelText('Runs');
    await waitFor(() => expect(within(runs).getByRole('option', { name: 'Codex' })).toBeDefined());
    await user.selectOptions(runs, 'codex');
    // Profiles show by name with their format; the default is marked.
    const nebius = screen.getByRole('button', { name: /Nebius · default/ });
    expect(nebius.getAttribute('aria-pressed')).toBe('true');
    expect(within(nebius).getByText('OpenAI-compatible')).toBeDefined();
    expect(await screen.findByLabelText('Model (optional)')).toBeDefined();
    expect(screen.getByRole('option', { name: "gpt-oss-120b (the provider's default)" })).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Start Codex' }));
    expect(await screen.findByText('terminal abababab00000000 on runner')).toBeDefined();
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/agents?runner=box')?.body ?? '{}')).toEqual({
      agent: 'codex',
      project: 'p_api',
      provider: 'nebius',
    });
  });

  it('adds a project by picking a git folder, then asks what to run', async () => {
    const user = userEvent.setup();
    const seen = serve((key) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [], runners: ['box'], machines: [], host: '', others: ['box'] } };
      if (key === 'GET /api/browse?runner=box') {
        return { status: 200, body: { path: '/srv', parent: '/', workspace: '/srv', entries: [{ name: 'notes' }, { name: 'shop', git: true }, { name: '.cache', hidden: true }] } };
      }
      if (key === 'GET /api/browse?runner=box&path=%2Fsrv%2Fshop') {
        return { status: 200, body: { path: '/srv/shop', parent: '/srv', workspace: '/srv', git: true, entries: [] } };
      }
      if (key === 'POST /api/projects') {
        return { status: 201, body: { project: { ...api, id: 'p_new', name: 'shop', dir: '/srv/shop', git: true }, terminal: '' } };
      }
      if (key === 'GET /api/agents?runner=box') return { status: 200, body: agents };
      if (key === 'GET /api/providers') return { status: 200, body: providers };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('No projects yet.')).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Add a project' }));
    expect(await screen.findByRole('button', { name: 'Select shop, a git repo' })).toBeDefined();
    // Hidden folders stay folded away until asked for.
    expect(screen.queryByRole('button', { name: /Select \.cache/ })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Open shop' }));
    expect(await screen.findByRole('button', { name: 'Use this folder (git repo)' })).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Use this folder (git repo)' }));
    await user.click(screen.getByRole('button', { name: 'Add it' }));
    expect(await screen.findByRole('form', { name: 'New terminal' })).toBeDefined();
    expect(screen.getByText('New terminal in shop')).toBeDefined();
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/projects')?.body ?? '{}')).toEqual({
      kind: 'runner',
      target: 'box',
      dir: '/srv/shop',
      git: true,
    });
  });

  it('clones a repo and shows the clone terminal', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [], runners: ['box'], machines: ['studio'], host: '', others: ['box'] } };
      if (key.startsWith('GET /api/browse')) return { status: 200, body: { path: '/srv', workspace: '/srv', entries: [] } };
      if (key === 'POST /api/projects') return { status: 201, body: { project: shop, terminal: '1111111100000000' } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    await user.click(await screen.findByRole('button', { name: 'Clone a repo' }));
    await user.type(screen.getByLabelText('Git URL'), 'git@github.com:acme/shop.git');
    await user.click(screen.getByRole('button', { name: 'Clone it' }));
    expect(await screen.findByText('terminal 1111111100000000 on studio')).toBeDefined();
  });

  it('forgets a project after a confirm', async () => {
    const user = userEvent.setup();
    let gone = false;
    serve((key) => {
      if (key === 'GET /api/projects') return { status: 200, body: gone ? { projects: [], runners: ['box'], machines: [], host: '', others: ['box'] } : listed };
      if (key.startsWith('GET /api/terminals')) return { status: 200, body: { terminals: [] } };
      if (key === 'DELETE /api/projects/p_api') {
        gone = true;
        return { status: 204 };
      }
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Forget api' }));
    expect(screen.getByText(/Files and terminals stay/)).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Forget' }));
    await waitFor(() => expect(gone).toBe(true));
  });

  it('offers the host first, then machines, then other runners', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/projects') {
        return { status: 200, body: { projects: [], runners: ['sandbox', 'host (box)'], machines: ['studio'], host: 'host (box)', others: ['sandbox'] } };
      }
      if (key.startsWith('GET /api/browse')) return { status: 200, body: { path: '/srv', workspace: '/srv', entries: [] } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    const options = within(screen.getByLabelText('On')).getAllByRole('option') as HTMLOptionElement[];
    expect(options.map((o) => o.textContent)).toEqual([
      'host (box) — this computer',
      'studio (machine)',
      'sandbox (runner)',
    ]);
  });

  it('browses the runner it chose, so a second linked runner is not a dead list', async () => {
    const user = userEvent.setup();
    const seen: string[] = [];
    serve((key) => {
      seen.push(key);
      if (key === 'GET /api/projects') {
        return { status: 200, body: { projects: [], runners: ['sandbox', 'host (box)'], machines: [], host: 'host (box)', others: ['sandbox'] } };
      }
      if (key.startsWith('GET /api/browse')) return { status: 200, body: { path: '/home/k', workspace: '/home/k', entries: [] } };
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    await user.selectOptions(screen.getByLabelText('On'), '1');
    await waitFor(() => expect(seen).toContain('GET /api/browse?runner=sandbox'));
    // Every browse names a runner: the host is never fetched roster-wide.
    expect(seen.some((k) => k === 'GET /api/browse')).toBe(false);
  });

  it('browses a typed path without submitting the whole add form', async () => {
    const user = userEvent.setup();
    const seen: string[] = [];
    serve((key) => {
      seen.push(key);
      if (key === 'GET /api/projects') return { status: 200, body: { projects: [], runners: ['box'], machines: [], host: '', others: ['box'] } };
      if (key.startsWith('GET /api/browse')) {
        return { status: 200, body: { path: '/srv', workspace: '/srv', entries: [{ name: 'deep' }] } };
      }
      return undefined;
    });
    render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add a project' }));
    await user.type(screen.getByLabelText('Go to a folder'), '/srv');
    await user.click(screen.getByRole('button', { name: 'Go' }));
    await waitFor(() => expect(seen).toContain('GET /api/browse?runner=box&path=%2Fsrv'));
    // A nested form would have submitted the outer one instead: the add
    // form is still up, and no project was posted.
    expect(screen.getByRole('form', { name: 'Add a project' })).toBeDefined();
    expect(seen.some((k) => k === 'POST /api/projects')).toBe(false);
  });

  it('keeps a folded project folded across renders', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/approvals/settings') {
        return { status: 200, body: { targets: [{ target: 'runner:box', runner: 'box', linked: true, mode: 'critical' }], modes: [], rules: [], timeoutSeconds: 60 } };
      }
      if (key === 'GET /api/projects') return { status: 200, body: listed };
      if (key.startsWith('GET /api/terminals')) return { status: 200, body: { terminals: [] } };
      return undefined;
    });
    const { unmount } = render(<Projects onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Fold api' }));
    expect(screen.queryByRole('list', { name: 'Terminals in api' })).toBeNull();
    unmount();
    render(<Projects onSessionExpired={vi.fn()} />);
    expect(await screen.findByRole('button', { name: 'Unfold api' })).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    serve((key) => (key === 'GET /api/projects' ? { status: 401 } : undefined));
    const expired = vi.fn();
    render(<Projects onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
