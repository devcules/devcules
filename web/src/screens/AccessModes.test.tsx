import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import type { ApprovalSettings } from '../api';
import AccessModes from './AccessModes';

type Reply = { status: number; body?: unknown } | 'reject';

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

const settings: ApprovalSettings = {
  targets: [
    { target: 'unbound', runner: '', linked: false, mode: '' },
    { target: 'runner:laptop', runner: 'laptop', linked: true, mode: 'critical' },
    { target: 'runner:old', runner: 'old', linked: false, mode: 'full' },
  ],
  modes: ['off', 'critical', 'all', 'full'],
  rules: [
    { id: 'force-push', name: 'Force push', pattern: 'p1', enabled: true, builtin: true },
    { id: 'prod', name: 'Prod deploys', pattern: 'kubectl .*prod', enabled: true, builtin: false },
  ],
  timeoutSeconds: 600,
};

function body(fn: Mock, call: number): unknown {
  const [, init] = fn.mock.calls[call] as [string, RequestInit];
  return JSON.parse(init.body as string);
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AccessModes', () => {
  it('shows a mode per runner, unset ones blocking', async () => {
    serve({ 'GET /api/approvals/settings': [{ status: 200, body: settings }] });
    render(<AccessModes onSessionExpired={() => {}} />);
    expect(await screen.findByText(/denied after 10 minutes/)).toBeTruthy();
    const unbound = screen.getByLabelText('Agents without a runner access mode') as HTMLSelectElement;
    expect(unbound.value).toBe('');
    expect(unbound.selectedOptions[0].textContent).toBe('Not set: blocks every call');
    expect((screen.getByLabelText('laptop access mode') as HTMLSelectElement).value).toBe('critical');
    expect((screen.getByLabelText('old (not linked) access mode') as HTMLSelectElement).value).toBe('full');
  });

  it('saves a mode', async () => {
    const saved = { ...settings, targets: settings.targets.map((t) => (t.runner === 'laptop' ? { ...t, mode: 'all' } : t)) };
    const fn = serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'PUT /api/approvals/modes': [{ status: 200, body: saved }],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.selectOptions(await screen.findByLabelText('laptop access mode'), 'all');
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2));
    expect(body(fn, 1)).toEqual({ target: 'runner:laptop', mode: 'all' });
    expect((screen.getByLabelText('laptop access mode') as HTMLSelectElement).value).toBe('all');
  });

  it('reverts a mode the server refused, with its reason', async () => {
    serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'PUT /api/approvals/modes': [{ status: 400, body: { error: 'target must be "unbound" or runner:<name>' } }],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.selectOptions(await screen.findByLabelText('laptop access mode'), 'off');
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Couldn\'t save that: target must be "unbound" or runner:<name>.',
    );
    expect((screen.getByLabelText('laptop access mode') as HTMLSelectElement).value).toBe('critical');
  });

  it('reverts when serve is unreachable', async () => {
    serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'PUT /api/approvals/modes': [{ status: 500 }],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.selectOptions(await screen.findByLabelText('laptop access mode'), 'off');
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't save that change. Try again.");
  });

  it('turns a built-in rule off and removes a custom one', async () => {
    const fn = serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'PUT /api/approvals/rules': [{ status: 200, body: settings }],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    const rules = within(await screen.findByRole('list', { name: 'Critical rules' }));
    expect(rules.getByText('kubectl .*prod')).toBeTruthy();
    await user.click(rules.getByRole('checkbox', { name: 'Force push' }));
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2));
    expect((body(fn, 1) as ApprovalSettings).rules[0].enabled).toBe(false);
    await user.click(rules.getByRole('button', { name: 'Remove rule Prod deploys' }));
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(3));
    expect((body(fn, 2) as ApprovalSettings).rules.map((r) => r.id)).toEqual(['force-push']);
  });

  it('adds a custom rule with an id from its name', async () => {
    const fn = serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'PUT /api/approvals/rules': [{ status: 200, body: settings }],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.type(await screen.findByLabelText('New rule name'), ' Drop the DB! ');
    await user.type(screen.getByLabelText('Pattern (a regular expression)'), 'dropdb');
    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2));
    expect((body(fn, 1) as ApprovalSettings).rules[2]).toEqual({
      id: 'drop-the-db',
      name: 'Drop the DB!',
      pattern: 'dropdb',
      enabled: true,
      builtin: false,
    });
    expect((screen.getByLabelText('New rule name') as HTMLInputElement).value).toBe('');
  });

  it('refuses a rule name another rule uses', async () => {
    const fn = serve({ 'GET /api/approvals/settings': [{ status: 200, body: settings }] });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.type(await screen.findByLabelText('New rule name'), 'Force push');
    await user.type(screen.getByLabelText('Pattern (a regular expression)'), 'x');
    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Pick a name that no other rule uses.');
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('checks a command against the rules', async () => {
    const fn = serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'POST /api/approvals/check': [
        { status: 200, body: { critical: true, rule: settings.rules[0] } },
        { status: 200, body: { critical: false } },
      ],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.type(await screen.findByLabelText('Try a command'), 'git push -f');
    await user.click(screen.getByRole('button', { name: 'Check' }));
    expect((await screen.findByRole('status')).textContent).toMatch(/^Critical: Force push\./);
    expect(body(fn, 1)).toEqual({ command: 'git push -f' });
    await user.type(screen.getByLabelText('Try a command'), 'x');
    await user.click(screen.getByRole('button', { name: 'Check' }));
    expect((await screen.findByRole('status')).textContent).toMatch(/^Not critical/);
  });

  it('says when a check fails', async () => {
    serve({
      'GET /api/approvals/settings': [{ status: 200, body: settings }],
      'POST /api/approvals/check': ['reject'],
    });
    const user = userEvent.setup();
    render(<AccessModes onSessionExpired={() => {}} />);
    await user.type(await screen.findByLabelText('Try a command'), 'ls');
    await user.click(screen.getByRole('button', { name: 'Check' }));
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't check that command. Try again.");
  });

  it('says when it cannot load', async () => {
    serve({ 'GET /api/approvals/settings': ['reject'] });
    render(<AccessModes onSessionExpired={() => {}} />);
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't load access modes. Try again.");
  });

  it('returns to sign-in on a 401', async () => {
    serve({ 'GET /api/approvals/settings': [{ status: 401 }] });
    const expired = vi.fn();
    render(<AccessModes onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
