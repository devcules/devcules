import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import Extensions from './Extensions';

type Reply = { status: number; body?: unknown } | 'reject';

// serve answers fetches by method and path, one reply per call; the last
// reply repeats. Matches the other screens' harness.
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

afterEach(() => vi.unstubAllGlobals());

const mcpEntry = {
  id: 'mcp-official:x/y', kind: 'mcp', name: 'x/y', description: 'demo server',
  source_id: 'mcp-official', source_name: 'Official MCP Registry',
  install: { type: 'stdio', command: 'npx', args: ['-y', 'x/y'], env_vars: [] },
};
const skillEntry = {
  id: 'skills-sh:anthropics/skills/pdf', kind: 'skill', name: 'pdf', description: 'PDF skill',
  source_id: 'skills-sh', source_name: 'skills.sh', install: { repo: 'anthropics/skills', skill: 'pdf' },
};

// Every mount fires these four (two fleet + one installed for the open tab).
function base(overrides: Record<string, Reply[]> = {}): Record<string, Reply[]> {
  return {
    'GET /api/mcp/fleet': [{ status: 200, body: [] }],
    'GET /api/skills/fleet': [{ status: 200, body: [] }],
    'GET /api/registry/mcp/installed': [{ status: 200, body: [] }],
    'GET /api/registry/skill/installed': [{ status: 200, body: [] }],
    ...overrides,
  };
}

const searchBox = () => screen.getByRole('textbox');

describe('Extensions', () => {
  it('shows the glance summary with counts and agents from the fleet', async () => {
    serve(base({
      'GET /api/mcp/fleet': [{ status: 200, body: [
        { name: 'github', status: 'ok', agents: ['claude-code', 'codex'] },
        { name: 'sentry', status: 'broken', agents: ['claude-code'] },
      ] }],
      'GET /api/skills/fleet': [{ status: 200, body: [
        { agent: 'claude-code', skills: [{ name: 'pdf', source: 'local', status: 'ok', agents: ['claude-code'] }] },
      ] }],
    }));
    render(<Extensions onSessionExpired={() => undefined} />);

    await waitFor(() => expect(screen.getByText('1 broken')).toBeTruthy());
    const glance = document.querySelector('[aria-label="At a glance"]')!;
    expect(glance?.textContent).toContain('2 MCP servers');
    expect(glance?.textContent).toContain('1 skill');
    expect(glance?.textContent).toContain('across Claude Code, Codex');
  });

  it('expands the glance into the per-agent detail', async () => {
    const user = userEvent.setup();
    serve(base({
      'GET /api/mcp/fleet': [{ status: 200, body: [{ name: 'github', status: 'ok', agents: ['claude-code'] }] }],
    }));
    render(<Extensions onSessionExpired={() => undefined} />);
    await waitFor(() => expect(document.querySelector('[aria-label="At a glance"]')?.textContent).toContain('1 MCP server'));
    await user.click(screen.getByRole('button', { name: 'details' }));
    expect(await screen.findByText('github')).toBeTruthy();
  });

  it('searches MCP, inspects to the tool list, and installs with a rename and env', async () => {
    const user = userEvent.setup();
    const withEnv = {
      ...mcpEntry, id: 'mcp-official:x/db', name: 'x/db',
      install: { type: 'stdio', command: 'npx', args: ['-y', 'x/db'], env_vars: [{ name: 'DB_TOKEN', secret: true }] },
    };
    const fetchMock = serve(base({
      'GET /api/registry/mcp/search?q=db': [{ status: 200, body: [withEnv] }],
      'POST /api/registry/mcp/inspect': [{ status: 200, body: { tools: [{ name: 'run_query' }] } }],
      'POST /api/registry/mcp/install': [{ status: 200, body: { ok: true } }],
    }));
    render(<Extensions onSessionExpired={() => undefined} />);

    await user.type(searchBox(), 'db');
    await user.click(screen.getByRole('button', { name: /^search$/ }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'x/db' })).toBeTruthy());

    await user.click(screen.getByRole('button', { name: 'x/db' }));
    await waitFor(() => expect(screen.getByText('run_query')).toBeTruthy());

    // env field from the spec, rename, install sends both
    await user.type(await screen.findByLabelText('DB_TOKEN'), 'tok');
    const nameBox = screen.getByLabelText('install name');
    await user.clear(nameBox);
    await user.type(nameBox, 'db-main');
    await user.click(screen.getByRole('button', { name: /install into Claude Code/ }));

    await waitFor(() => {
      const call = fetchMock.mock.calls.find((c) => c[0].toString() === '/api/registry/mcp/install');
      expect(call).toBeTruthy();
      const body = JSON.parse(String((call![1] as RequestInit).body));
      expect(body.name).toBe('db-main');
      expect(body.params).toEqual({ DB_TOKEN: 'tok' });
    });
  });

  it('switches to the Skills tab, reads the SKILL.md on inspect, installs to all agents', async () => {
    const user = userEvent.setup();
    const fetchMock = serve(base({
      'GET /api/registry/skill/search?q=pdf': [{ status: 200, body: [skillEntry] }],
      'POST /api/registry/skill/inspect': [{ status: 200, body: { skill_md: '# pdf\nDo PDF things.' } }],
      'POST /api/registry/skill/install': [{ status: 200, body: { ok: true } }],
    }));
    render(<Extensions onSessionExpired={() => undefined} />);

    await user.click(screen.getByRole('button', { name: 'Skills' }));
    await user.type(searchBox(), 'pdf');
    await user.click(screen.getByRole('button', { name: /^search$/ }));
    await waitFor(() => expect(screen.getByText('pdf')).toBeTruthy());

    await user.click(screen.getByRole('button', { name: 'pdf' }));
    await waitFor(() => expect(screen.getByText(/Do PDF things/)).toBeTruthy());

    await user.click(screen.getByRole('button', { name: /install \(all agents\)/ }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => `${(c[1] as RequestInit)?.method ?? 'GET'} ${c[0].toString()}`);
      expect(calls).toContain('POST /api/registry/skill/install');
    });
  });

  it('switches the MCP install target between Claude Code and Codex', async () => {
    const user = userEvent.setup();
    serve(base());
    render(<Extensions onSessionExpired={() => undefined} />);
    await user.click(screen.getByRole('button', { name: 'Codex' }));
    await user.type(searchBox(), 'q');
    await user.click(screen.getByRole('button', { name: /^search$/ }));
    // install button on an expanded entry would read "install into Codex";
    // assert the picker state flipped instead of a results-dependent check.
    expect(screen.getByRole('button', { name: 'Codex' }).getAttribute('aria-pressed')).toBe('true');
  });

  it('removes an installed skill after confirmation', async () => {
    const user = userEvent.setup();
    const fetchMock = serve(base({
      'GET /api/registry/skill/installed': [
        { status: 200, body: [{ name: 'pdf', kind: 'skill', source: 'claude-code' }] },
        { status: 200, body: [] },
      ],
      'DELETE /api/registry/skill/installed/pdf': [{ status: 200, body: { ok: true } }],
    }));
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<Extensions onSessionExpired={() => undefined} />);

    await user.click(screen.getByRole('button', { name: 'Skills' }));
    await waitFor(() => expect(screen.getByText('pdf')).toBeTruthy());
    await user.click(screen.getByRole('button', { name: 'remove' }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => `${(c[1] as RequestInit)?.method ?? 'GET'} ${c[0].toString()}`);
      expect(calls).toContain('DELETE /api/registry/skill/installed/pdf');
    });
  });

  it('bounces to sign-in when the session expires mid-load', async () => {
    const onExpired = vi.fn();
    serve({
      'GET /api/mcp/fleet': [{ status: 401 }],
      'GET /api/skills/fleet': [{ status: 401 }],
      'GET /api/registry/mcp/installed': [{ status: 401 }],
      'GET /api/registry/skill/installed': [{ status: 401 }],
    });
    render(<Extensions onSessionExpired={onExpired} />);
    await waitFor(() => expect(onExpired).toHaveBeenCalled());
  });

  it('shows the registry error message when a search is refused', async () => {
    const user = userEvent.setup();
    serve(base({
      'GET /api/registry/mcp/search?q=zzz': [{ status: 502, body: { error: { code: 'registry_unreachable', message: 'registry.modelcontextprotocol.io is down' } } }],
    }));
    render(<Extensions onSessionExpired={() => undefined} />);
    await user.type(searchBox(), 'zzz');
    await user.click(screen.getByRole('button', { name: /^search$/ }));
    await waitFor(() => expect(screen.getByText('registry.modelcontextprotocol.io is down')).toBeTruthy());
  });
});
