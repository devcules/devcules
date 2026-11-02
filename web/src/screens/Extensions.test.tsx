import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import Extensions from './Extensions';

type Reply = { status: number; body?: unknown } | 'reject';

// serve answers fetches by method and path, one reply per call; the last
// reply repeats. Matches the Activity screen's harness.
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

// The mount always fires the same four fetches (two fleet + two installed);
// baseReplies seeds the quiet "nothing here" version.
const baseReplies: Record<string, Reply[]> = {
  'GET /api/mcp/fleet': [{ status: 200, body: [] }],
  'GET /api/skills/fleet': [{ status: 200, body: [] }],
  'GET /api/registry/mcp/installed': [{ status: 200, body: [] }],
  'GET /api/registry/skill/installed': [{ status: 200, body: [] }],
};

describe('Extensions', () => {
  it('renders the three panels with the on-this-machine fleet first', async () => {
    serve({
      ...baseReplies,
      'GET /api/mcp/fleet': [{ status: 200, body: [{ name: 'github', status: 'ok', agents: ['claude-code', 'codex'] }] }],
      'GET /api/skills/fleet': [{ status: 200, body: [{ agent: 'claude-code', skills: [{ name: 'pdf', source: 'local', status: 'ok', agents: ['claude-code'] }] }] }],
    });
    render(<Extensions onSessionExpired={() => undefined} />);

    expect(screen.getByText('Fleet')).toBeTruthy();
    expect(screen.getByText('MCP registry')).toBeTruthy();
    expect(screen.getByText('Skills registry')).toBeTruthy();

    await waitFor(() => expect(screen.getByText('github')).toBeTruthy());
    expect(screen.getByText('pdf')).toBeTruthy();
    expect(screen.getAllByText('Claude Code').length).toBeGreaterThan(0);
  });

  it('searches the MCP registry and installs the picked server', async () => {
    const user = userEvent.setup();
    const fetchMock = serve({
      ...baseReplies,
      'GET /api/registry/mcp/search?q=github': [{ status: 200, body: [mcpEntry] }],
      'POST /api/registry/mcp/inspect': [{ status: 200, body: { tools: [{ name: 'create_issue' }] } }],
      'POST /api/registry/mcp/install': [{ status: 200, body: { ok: true } }],
    });
    render(<Extensions onSessionExpired={() => undefined} />);

    // The MCP search box is the one before the skills one.
    const searchBoxes = screen.getAllByRole('textbox');
    await user.type(searchBoxes[0], 'github');
    await user.click(screen.getAllByRole('button', { name: /^search$/ })[0]);

    await waitFor(() => expect(screen.getByText('x/y')).toBeTruthy());

    // expand → inspect runs, tools show, install offered
    await user.click(screen.getByRole('button', { name: 'x/y' }));
    await waitFor(() => expect(screen.getByText('create_issue')).toBeTruthy());

    await user.click(screen.getByRole('button', { name: /install into Claude Code/ }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => `${(c[1] as RequestInit)?.method ?? 'GET'} ${c[0].toString()}`);
      expect(calls).toContain('POST /api/registry/mcp/install');
    });
  });

  it('searches skills.sh and shows the SKILL.md on inspect', async () => {
    const user = userEvent.setup();
    const skillEntry = {
      id: 'skills-sh:anthropics/skills/pdf', kind: 'skill', name: 'pdf', description: 'PDF skill',
      source_id: 'skills-sh', source_name: 'skills.sh', install: { repo: 'anthropics/skills', skill: 'pdf' },
    };
    serve({
      ...baseReplies,
      'GET /api/registry/skill/search?q=pdf': [{ status: 200, body: [skillEntry] }],
      'POST /api/registry/skill/inspect': [{ status: 200, body: { skill_md: '# pdf\nDo PDF things.' } }],
    });
    render(<Extensions onSessionExpired={() => undefined} />);

    const searchBoxes = screen.getAllByRole('textbox');
    await user.type(searchBoxes[1], 'pdf');
    await user.click(screen.getAllByRole('button', { name: /^search$/ })[1]);

    await waitFor(() => expect(screen.getByText('pdf')).toBeTruthy());
    await user.click(screen.getByRole('button', { name: 'pdf' }));
    await waitFor(() => expect(screen.getByText(/Do PDF things/)).toBeTruthy());
    expect(screen.getByRole('button', { name: /install \(all agents\)/ })).toBeTruthy();
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
    serve({
      ...baseReplies,
      'GET /api/registry/mcp/search?q=zzz': [{ status: 502, body: { error: { code: 'registry_unreachable', message: 'registry.modelcontextprotocol.io is down' } } }],
    });
    render(<Extensions onSessionExpired={() => undefined} />);

    const searchBoxes = screen.getAllByRole('textbox');
    await user.type(searchBoxes[0], 'zzz');
    await user.click(screen.getAllByRole('button', { name: /^search$/ })[0]);

    await waitFor(() => expect(screen.getByText('registry.modelcontextprotocol.io is down')).toBeTruthy());
  });

  it('removes an installed skill after confirmation', async () => {
    const user = userEvent.setup();
    const fetchMock = serve({
      ...baseReplies,
      'GET /api/registry/skill/installed': [
        { status: 200, body: [{ name: 'pdf', kind: 'skill', source: 'claude-code' }] },
        { status: 200, body: [] },
      ],
      'DELETE /api/registry/skill/installed/pdf': [{ status: 200, body: { ok: true } }],
    });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<Extensions onSessionExpired={() => undefined} />);

    await waitFor(() => expect(screen.getByText('pdf')).toBeTruthy());
    await user.click(screen.getByRole('button', { name: 'remove' }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => `${(c[1] as RequestInit)?.method ?? 'GET'} ${c[0].toString()}`);
      expect(calls).toContain('DELETE /api/registry/skill/installed/pdf');
    });
  });

  it('offers an env field per server hint and sends the values on install', async () => {
    const user = userEvent.setup();
    const withEnv = {
      ...mcpEntry,
      id: 'mcp-official:x/db', name: 'x/db',
      install: { type: 'stdio', command: 'npx', args: ['-y', 'x/db'], env_vars: [{ name: 'DB_TOKEN', secret: true, description: 'db token' }] },
    };
    const fetchMock = serve({
      ...baseReplies,
      'GET /api/registry/mcp/search?q=db': [{ status: 200, body: [withEnv] }],
      'POST /api/registry/mcp/inspect': [{ status: 200, body: { tools: [] } }],
      'POST /api/registry/mcp/install': [{ status: 200, body: { ok: true } }],
    });
    render(<Extensions onSessionExpired={() => undefined} />);

    const searchBoxes = screen.getAllByRole('textbox');
    await user.type(searchBoxes[0], 'db');
    await user.click(screen.getAllByRole('button', { name: /^search$/ })[0]);
    await waitFor(() => expect(screen.getByText('x/db')).toBeTruthy());

    await user.click(screen.getByRole('button', { name: 'x/db' }));
    const envInput = await screen.findByLabelText('DB_TOKEN');
    await user.type(envInput, 's3cr3t');

    await user.click(screen.getByRole('button', { name: /install into Claude Code/ }));
    await waitFor(() => {
      const installCall = fetchMock.mock.calls.find((c) => c[0].toString() === '/api/registry/mcp/install');
      expect(installCall).toBeTruthy();
      expect(JSON.parse(String((installCall![1] as RequestInit).body)).params).toEqual({ DB_TOKEN: 's3cr3t' });
    });
  });

  it('lets you rename the install and removes an installed MCP server', async () => {
    const user = userEvent.setup();
    const fetchMock = serve({
      ...baseReplies,
      'GET /api/registry/mcp/installed': [
        { status: 200, body: [{ name: 'github', kind: 'mcp', source: 'claude-code' }] },
        { status: 200, body: [] },
      ],
      'GET /api/registry/mcp/search?q=github': [{ status: 200, body: [mcpEntry] }],
      'POST /api/registry/mcp/inspect': [{ status: 200, body: { tools: [] } }],
      'POST /api/registry/mcp/install': [{ status: 200, body: { ok: true } }],
      'DELETE /api/registry/mcp/installed/github': [{ status: 200, body: { ok: true } }],
    });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<Extensions onSessionExpired={() => undefined} />);

    // installed row shows, and its remove calls DELETE on the MCP kind.
    await waitFor(() => expect(screen.getByText('github')).toBeTruthy());
    await user.click(screen.getByRole('button', { name: 'remove' }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => `${(c[1] as RequestInit)?.method ?? 'GET'} ${c[0].toString()}`);
      expect(calls).toContain('DELETE /api/registry/mcp/installed/github');
    });

    // search → expand → rename via the "install name" box → install sends it.
    const searchBoxes = screen.getAllByRole('textbox');
    await user.type(searchBoxes[0], 'github');
    await user.click(screen.getAllByRole('button', { name: /^search$/ })[0]);
    await waitFor(() => expect(screen.getByText('x/y')).toBeTruthy());
    await user.click(screen.getByRole('button', { name: 'x/y' }));
    const nameBox = await screen.findByLabelText('install name');
    await user.clear(nameBox);
    await user.type(nameBox, 'github-main');
    await user.click(screen.getByRole('button', { name: /install into Claude Code/ }));
    await waitFor(() => {
      const installCall = fetchMock.mock.calls.find((c) => c[0].toString() === '/api/registry/mcp/install');
      expect(installCall).toBeTruthy();
      expect(JSON.parse(String((installCall![1] as RequestInit).body)).name).toBe('github-main');
    });
  });

  it('installs a skill to all agents from the skills registry', async () => {
    const user = userEvent.setup();
    const skillEntry = {
      id: 'skills-sh:anthropics/skills/pdf', kind: 'skill', name: 'pdf', description: 'PDF skill',
      source_id: 'skills-sh', source_name: 'skills.sh', install: { repo: 'anthropics/skills', skill: 'pdf' },
    };
    const fetchMock = serve({
      ...baseReplies,
      'GET /api/registry/skill/search?q=pdf': [{ status: 200, body: [skillEntry] }],
      'POST /api/registry/skill/inspect': [{ status: 200, body: { skill_md: '# pdf' } }],
      'POST /api/registry/skill/install': [{ status: 200, body: { ok: true } }],
      'GET /api/registry/skill/installed': [
        { status: 200, body: [] },
        { status: 200, body: [{ name: 'pdf', kind: 'skill', source: 'claude-code' }] },
      ],
    });
    render(<Extensions onSessionExpired={() => undefined} />);

    const searchBoxes = screen.getAllByRole('textbox');
    await user.type(searchBoxes[1], 'pdf');
    await user.click(screen.getAllByRole('button', { name: /^search$/ })[1]);
    await waitFor(() => expect(screen.getByText('pdf')).toBeTruthy());
    await user.click(screen.getByRole('button', { name: 'pdf' }));
    await user.click(await screen.findByRole('button', { name: /install \(all agents\)/ }));

    await waitFor(() => {
      const calls = fetchMock.mock.calls.map((c) => `${(c[1] as RequestInit)?.method ?? 'GET'} ${c[0].toString()}`);
      expect(calls).toContain('POST /api/registry/skill/install');
    });
    // after install the row lands in the skills installed list
    await waitFor(() => expect(screen.getAllByText('pdf').length).toBeGreaterThan(0));
  });

  it('switches the MCP install target between Claude Code and Codex', async () => {
    const user = userEvent.setup();
    serve(baseReplies);
    render(<Extensions onSessionExpired={() => undefined} />);
    const codex = screen.getByRole('button', { name: 'Codex' });
    await user.click(codex);
    // the picker retitles the install affordance in the panel header
    expect(screen.getByText(/install into: Codex/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Claude Code' }));
    expect(screen.getByText(/install into: Claude Code/)).toBeTruthy();
  });
});
