import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import Secrets from './Secrets';
import { deleteSecret } from '../lib/deskApi';

type Reply = { status: number; body?: unknown };

// serve answers fetches by method and path, one reply per call (the last
// one repeats).
function serve(replies: Record<string, Reply[]>): Mock {
  const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const queue = replies[key];
    if (!queue || queue.length === 0) throw new Error(`unexpected fetch ${key}`);
    const r = queue.length > 1 ? queue.shift()! : queue[0];
    return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Secrets remove', () => {
  it('deleteSecret treats 404 as already removed', async () => {
    serve({ 'DELETE /api/secrets/MY_KEY': [{ status: 404 }] });
    expect(await deleteSecret('MY_KEY')).toBe(true);
    serve({ 'DELETE /api/secrets/MY_KEY': [{ status: 500 }] });
    expect(await deleteSecret('MY_KEY')).toBe('unreachable');
  });

  it('a name another session already removed (404) shows no error', async () => {
    const fn = serve({
      'GET /api/secrets': [{ status: 200, body: { names: ['MY_KEY'] } }, { status: 200, body: { names: [] } }],
      'DELETE /api/secrets/MY_KEY': [{ status: 404 }],
    });
    const user = userEvent.setup();
    render(<Secrets onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'remove MY_KEY' }));
    expect(await screen.findByText(/No secrets yet/)).toBeTruthy();
    expect(fn.mock.calls.filter(([, i]) => (i as RequestInit | undefined)?.method === 'DELETE')).toHaveLength(1);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('a successful remove clears an earlier error', async () => {
    serve({
      'GET /api/secrets': [{ status: 200, body: { names: ['A', 'B'] } }, { status: 200, body: { names: ['B'] } }],
      'DELETE /api/secrets/B': [{ status: 500 }],
      'DELETE /api/secrets/A': [{ status: 204 }],
    });
    const user = userEvent.setup();
    render(<Secrets onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'remove B' }));
    expect((await screen.findByRole('alert')).textContent).toBe('could not remove B');
    await user.click(screen.getByRole('button', { name: 'remove A' }));
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
    expect(screen.queryByRole('button', { name: 'remove A' })).toBeNull();
  });
});

describe('Secrets load', () => {
  // DESK-16: a parent passing fresh callbacks each render must not refetch.
  it('does not refetch when the parent re-renders with new callbacks', async () => {
    const fn = serve({ 'GET /api/secrets': [{ status: 200, body: { names: ['A'] } }] });
    const { rerender } = render(<Secrets onSessionExpired={() => {}} onSummary={() => {}} />);
    expect(await screen.findByText('A')).toBeTruthy();
    for (let i = 0; i < 3; i++) rerender(<Secrets onSessionExpired={() => {}} onSummary={() => {}} />);
    await new Promise((r) => setTimeout(r, 30));
    expect(fn).toHaveBeenCalledTimes(1);
  });

  // DESK-18: unreachable is unknown, not empty.
  it('an unreachable list says it could not load, not "No secrets yet"', async () => {
    serve({ 'GET /api/secrets': [{ status: 503 }] });
    const summary = vi.fn();
    render(<Secrets onSessionExpired={() => {}} onSummary={summary} />);
    expect((await screen.findByRole('alert')).textContent).toBe('server unreachable');
    expect(screen.getByText("couldn't load the list")).toBeTruthy();
    expect(screen.queryByText(/No secrets yet/)).toBeNull();
    expect(summary).toHaveBeenCalledWith("Couldn't load");
  });
});
