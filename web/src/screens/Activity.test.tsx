import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import Activity from './Activity';
import { checkAudit, getTrace, listAudit, listTraces } from '../api';

type Reply = { status: number; body?: unknown; text?: string } | 'reject';

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
    if (r.text !== undefined) {
      return new Response(r.text, { status: r.status });
    }
    return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

const session = {
  launchId: 'lt_1',
  label: 'musecode in t1',
  provider: 'meta',
  calls: 2,
  inputTokens: 30,
  outputTokens: 12,
  redacted: 1,
  first: '2026-10-29T04:00:00Z',
  last: '2026-10-29T04:01:00Z',
};

const call = {
  id: 1,
  at: '2026-10-29T04:00:00Z',
  provider: 'meta',
  path: 'v1/chat/completions',
  model: 'muse-spark-1.3',
  status: 200,
  stream: true,
  durationMs: 812,
  inputTokens: 30,
  outputTokens: 12,
  redacted: { SECRET: 1, EMAIL: 0 },
  question: 'deploy with REDACTED_SECRET_0123456789',
  reply: 'done',
  toolCalls: [{ name: 'bash', args: '{"cmd":"make"}' }],
};

function entry(seq: number, action: string, extra: object = {}) {
  return { seq, at: '2026-10-29T04:00:00Z', actor: 'admin', action, ...extra };
}

const emptyAudit = { 'GET /api/audit?limit=50': [{ status: 200, body: { entries: [] } }] };

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Activity traces', () => {
  it('says when there are no model calls', async () => {
    serve({ 'GET /api/traces': [{ status: 200, body: { sessions: [] } }], ...emptyAudit });
    render(<Activity onSessionExpired={() => {}} />);
    expect(await screen.findByText(/No model calls yet/)).toBeTruthy();
  });

  it('opens a launch and shows its calls with placeholders', async () => {
    serve({
      'GET /api/traces': [{ status: 200, body: { sessions: [session] } }],
      'GET /api/traces/lt_1': [{ status: 200, body: { calls: [call] } }],
      ...emptyAudit,
    });
    const user = userEvent.setup();
    render(<Activity onSessionExpired={() => {}} />);
    const open = await screen.findByRole('button', { name: /musecode in t1/ });
    expect(open.textContent).toContain('2 calls · 42 tokens · 1 redacted');
    await user.click(open);
    expect(await screen.findByText('deploy with REDACTED_SECRET_0123456789')).toBeTruthy();
    expect(screen.getByText('{"cmd":"make"}')).toBeTruthy();
    expect(screen.getByText('Tool bash')).toBeTruthy();
    expect(screen.getByText(/muse-spark-1.3 · 200 · 812 ms · 30 in \/ 12 out · redacted 1 SECRET/)).toBeTruthy();
    await user.click(open);
    expect(screen.queryByText('done')).toBeNull();
  });

  it('reports a trace that will not load', async () => {
    serve({
      'GET /api/traces': [{ status: 200, body: { sessions: [{ ...session, label: '' }] } }],
      'GET /api/traces/lt_1': [{ status: 500 }],
      ...emptyAudit,
    });
    const user = userEvent.setup();
    render(<Activity onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: /lt_1/ }));
    expect((await screen.findByRole('alert')).textContent).toContain("Couldn't load this trace");
  });

  it('reports traces that will not load, and expired sessions', async () => {
    serve({ 'GET /api/traces': ['reject'], ...emptyAudit });
    const { unmount } = render(<Activity onSessionExpired={() => {}} />);
    expect((await screen.findByRole('alert')).textContent).toContain("Couldn't load traces");
    unmount();
    const expired = vi.fn();
    serve({ 'GET /api/traces': [{ status: 401 }], 'GET /api/audit?limit=50': [{ status: 401 }] });
    render(<Activity onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});

describe('Activity audit log', () => {
  it('lists entries, pages back, and checks the chain', async () => {
    const page = Array.from({ length: 50 }, (_, i) => entry(60 - i, 'auth.sign_in', { ip: '192.0.2.1' }));
    serve({
      'GET /api/traces': [{ status: 200, body: { sessions: [] } }],
      'GET /api/audit?limit=50': [
        { status: 200, body: { entries: [entry(61, 'redact.policy', { detail: { EMAIL: 'off → mask' } }), ...page.slice(1)] } },
      ],
      'GET /api/audit?limit=50&before=11': [{ status: 200, body: { entries: [entry(10, 'launch.mint', { target: 'lt_9' }), entry(9, 'custom.thing')] } }],
      'GET /api/audit/export?verify=1': [
        { status: 200, body: { ok: true, checked: 61 } },
        { status: 200, body: { ok: false, checked: 4, brokenAt: 5, reason: 'its fields were changed after it was written' } },
      ],
    });
    const user = userEvent.setup();
    render(<Activity onSessionExpired={() => {}} />);
    const log = await screen.findByRole('region', { name: 'Audit log' });
    expect(await within(log).findByText('Changed redaction')).toBeTruthy();
    expect(within(log).getByText(/EMAIL off → mask/)).toBeTruthy();
    await user.click(within(log).getByRole('button', { name: 'Show older' }));
    expect(await within(log).findByText('lt_9', { exact: false })).toBeTruthy();
    expect(within(log).getByText('custom.thing')).toBeTruthy();
    expect(within(log).queryByRole('button', { name: 'Show older' })).toBeNull();
    expect(within(log).getByRole('link', { name: 'Export as JSON lines' }).getAttribute('href')).toBe('/api/audit/export');

    await user.click(within(log).getByRole('button', { name: 'Check the log' }));
    expect((await within(log).findByRole('status')).textContent).toBe('All 61 entries are intact.');
    await user.click(within(log).getByRole('button', { name: 'Check the log' }));
    await waitFor(() => expect(within(log).getByRole('status').textContent).toContain('Entry 5 its fields were changed'));
  });

  it('reports a log or a check that will not load', async () => {
    serve({
      'GET /api/traces': [{ status: 200, body: { sessions: [] } }],
      'GET /api/audit?limit=50': [{ status: 200, text: 'not json' }],
      'GET /api/audit/export?verify=1': [{ status: 500 }],
    });
    const user = userEvent.setup();
    render(<Activity onSessionExpired={() => {}} />);
    const log = await screen.findByRole('region', { name: 'Audit log' });
    expect((await within(log).findByRole('alert')).textContent).toContain("Couldn't load the audit log");
    await user.click(within(log).getByRole('button', { name: 'Check the log' }));
    await waitFor(() => expect(within(log).getAllByRole('alert')).toHaveLength(2));
  });
});

describe('activity api', () => {
  it('maps bad shapes, failures and 401s', async () => {
    serve({
      'GET /api/traces': [{ status: 200, body: { nope: 1 } }],
      'GET /api/traces/a%2Fb': [{ status: 401 }],
      'GET /api/audit?limit=50&before=3': ['reject'],
      'GET /api/audit/export?verify=1': [{ status: 200, body: [] }],
    });
    expect(await listTraces()).toBe('unreachable');
    expect(await getTrace('a/b')).toBe('unauthorized');
    expect(await listAudit(3)).toBe('unreachable');
    expect(await checkAudit()).toBe('unreachable');
  });
});
