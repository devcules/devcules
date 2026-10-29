import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import Redaction from './Redaction';

const state = {
  policy: { SECRET: 'mask', EMAIL: 'off', PHONE: 'off', CARD: 'off', IPV4: 'off' },
  types: ['SECRET', 'EMAIL', 'PHONE', 'CARD', 'IPV4'],
  modes: ['off', 'log', 'mask', 'block'],
  stats: { SECRET: { detected: 3, masked: 2, blocked: 1 } },
  rules: 221,
};

type Reply = { status: number; body?: unknown } | 'reject';

// serve answers fetches by method and path, one reply per call.
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

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Redaction', () => {
  it('shows modes, counts and the rule count', async () => {
    serve({ 'GET /api/redact': [{ status: 200, body: state }] });
    render(<Redaction onSessionExpired={() => {}} />);
    expect(await screen.findByText(/221 secret rules are loaded/)).toBeTruthy();
    expect((screen.getByLabelText('Secrets and keys mode') as HTMLSelectElement).value).toBe('mask');
    expect((screen.getByLabelText('Email addresses mode') as HTMLSelectElement).value).toBe('off');
    expect(screen.getByText('3 found · 2 masked · 1 blocked')).toBeTruthy();
    expect(screen.getAllByText('0 found · 0 masked · 0 blocked')).toHaveLength(4);
  });

  it('saves a mode change with the whole policy', async () => {
    const fn = serve({ 'GET /api/redact': [{ status: 200, body: state }], 'PUT /api/redact/policy': [{ status: 204 }] });
    const user = userEvent.setup();
    render(<Redaction onSessionExpired={() => {}} />);
    await user.selectOptions(await screen.findByLabelText('Email addresses mode'), 'block');
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2));
    const [, init] = fn.mock.calls[1] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ ...state.policy, EMAIL: 'block' });
    expect((screen.getByLabelText('Email addresses mode') as HTMLSelectElement).value).toBe('block');
  });

  it('reverts a change the server refused', async () => {
    serve({ 'GET /api/redact': [{ status: 200, body: state }], 'PUT /api/redact/policy': [{ status: 500 }] });
    const user = userEvent.setup();
    render(<Redaction onSessionExpired={() => {}} />);
    await user.selectOptions(await screen.findByLabelText('Card numbers mode'), 'mask');
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't save that change. Try again.");
    expect((screen.getByLabelText('Card numbers mode') as HTMLSelectElement).value).toBe('off');
  });

  it('runs the test box and shows the masked text', async () => {
    serve({
      'GET /api/redact': [{ status: 200, body: state }],
      'POST /api/redact/test': [
        {
          status: 200,
          body: {
            text: 'key REDACTED_SECRET_0123456789',
            findings: [{ type: 'SECRET', rule: 'aws-access-token', placeholder: 'REDACTED_SECRET_0123456789' }],
            blocked: [],
          },
        },
      ],
    });
    const user = userEvent.setup();
    render(<Redaction onSessionExpired={() => {}} />);
    const button = await screen.findByRole('button', { name: 'Test' });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    await user.type(screen.getByLabelText('Test text'), 'key something');
    await user.click(button);
    expect(await screen.findByText('key REDACTED_SECRET_0123456789')).toBeTruthy();
    expect(screen.getByText('Found: Secrets and keys (aws-access-token)')).toBeTruthy();
  });

  it('says when the test text is blocked', async () => {
    serve({
      'GET /api/redact': [{ status: 200, body: state }],
      'POST /api/redact/test': [
        { status: 200, body: { text: '', findings: [{ type: 'EMAIL', rule: 'email' }], blocked: ['EMAIL'] } },
      ],
    });
    const user = userEvent.setup();
    render(<Redaction onSessionExpired={() => {}} />);
    await user.type(await screen.findByLabelText('Test text'), 'x');
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect(await screen.findByText(/Blocked: this text holds Email addresses/)).toBeTruthy();
  });

  it('reports test failures in plain words', async () => {
    serve({
      'GET /api/redact': [{ status: 200, body: state }],
      'POST /api/redact/test': [{ status: 400, body: { error: 'too big' } }, 'reject'],
    });
    const user = userEvent.setup();
    render(<Redaction onSessionExpired={() => {}} />);
    await user.type(await screen.findByLabelText('Test text'), 'x');
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect((await screen.findByRole('alert')).textContent).toBe('That text is too long to test (64 KiB at most).');
    await user.click(screen.getByRole('button', { name: 'Test' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe("Couldn't run the test. Try again."));
  });

  it('routes an expired session to sign-in', async () => {
    serve({ 'GET /api/redact': [{ status: 401 }] });
    const expired = vi.fn();
    render(<Redaction onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });

  it('says when the settings cannot load', async () => {
    serve({ 'GET /api/redact': ['reject'] });
    render(<Redaction onSessionExpired={() => {}} />);
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't load redaction settings. Try again.");
  });
});
