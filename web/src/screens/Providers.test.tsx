import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Providers from './Providers';

type Reply = { status: number; body?: unknown };

function reply(r: Reply): Response {
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

function serve(handler: (key: string, body: string) => Reply | undefined): Array<{ key: string; body: string }> {
  const seen: Array<{ key: string; body: string }> = [];
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

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Providers', () => {
  it('adds a key, then shows it set without showing it', async () => {
    const user = userEvent.setup();
    let set = false;
    const seen = serve((key) => {
      if (key === 'GET /api/providers') {
        return { status: 200, body: { providers: [{ id: 'anthropic', ready: set }, { id: 'meta', ready: true }] } };
      }
      if (key === 'PUT /api/providers/anthropic/key') {
        set = true;
        return { status: 204 };
      }
      return undefined;
    });
    render(<Providers onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('Anthropic')).toBeDefined();
    expect(screen.getByText('No key')).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Add key' }));
    const field = screen.getByLabelText('Anthropic API key');
    expect(field.getAttribute('type')).toBe('password');
    await user.type(field, 'sk-ant-0123456789');
    await user.click(screen.getByRole('button', { name: 'Save key' }));
    expect(await screen.findByText('Anthropic key saved.')).toBeDefined();
    expect(seen.find((s) => s.key === 'PUT /api/providers/anthropic/key')?.body).toBe('{"key":"sk-ant-0123456789"}');
    await waitFor(() => expect(screen.getAllByText('Key set')).toHaveLength(2));
    expect(screen.queryByText(/sk-ant/)).toBeNull();
  });

  it('shows the server reason for a bad key', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key === 'GET /api/providers') return { status: 200, body: { providers: [{ id: 'openai', ready: false }] } };
      if (key === 'PUT /api/providers/openai/key') return { status: 400, body: { error: 'key: paste the whole API key' } };
      return undefined;
    });
    render(<Providers onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Add key' }));
    await user.type(screen.getByLabelText('OpenAI API key'), 'short');
    await user.click(screen.getByRole('button', { name: 'Save key' }));
    expect(await screen.findByText('key: paste the whole API key')).toBeDefined();
  });

  it('removes a key after a confirm', async () => {
    const user = userEvent.setup();
    let removed = false;
    serve((key) => {
      if (key === 'GET /api/providers') return { status: 200, body: { providers: [{ id: 'meta', ready: !removed }] } };
      if (key === 'DELETE /api/providers/meta/key') {
        removed = true;
        return { status: 204 };
      }
      return undefined;
    });
    render(<Providers onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Remove the Meta key' }));
    expect(screen.getByText(/stop working until you add a key again/)).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Confirm remove the Meta key' }));
    expect(await screen.findByText('Meta key removed.')).toBeDefined();
    expect(await screen.findByText('No key')).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    serve((key) => (key === 'GET /api/providers' ? { status: 401 } : undefined));
    const expired = vi.fn();
    render(<Providers onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
