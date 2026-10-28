import { describe, expect, it, vi } from 'vitest';
import { closeTerminal, listTerminals, me, openTerminal, signIn } from './api';

function post(status: number, body: unknown = null): void {
  // A null body keeps 204/401/429 constructible (a body with 204
  // throws); only the 200 case carries JSON.
  const response = body === null ? new Response(null, { status }) : Response.json(body, { status });
  window.fetch = vi.fn(async () => response) as typeof fetch;
}

describe('api', () => {
  it('maps sign-in statuses to words', async () => {
    post(204);
    expect(await signIn('admin', 'pw')).toBe('ok');
    post(401);
    expect(await signIn('admin', 'pw')).toBe('invalid');
    post(429);
    expect(await signIn('admin', 'pw')).toBe('limited');
    post(500);
    expect(await signIn('admin', 'pw')).toBe('unreachable');
  });

  it('reports a network failure as unreachable, not a wrong password', async () => {
    window.fetch = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }) as typeof fetch;
    expect(await signIn('admin', 'pw')).toBe('unreachable');
  });

  it('reads the username from /auth/me', async () => {
    post(200, { username: 'admin' });
    expect(await me()).toBe('admin');
    post(401);
    expect(await me()).toBeNull();
  });

  it('lists terminals and filters broken entries', async () => {
    post(200, { terminals: [{ term: 'abc', created: 1 }, null, { term: 7 }] });
    expect(await listTerminals()).toEqual({ terminals: [{ term: 'abc', created: 1 }] });
    post(503);
    expect(await listTerminals()).toBe('offline');
    post(401);
    expect(await listTerminals()).toBe('unauthorized');
  });

  it('opens a terminal and maps runner states', async () => {
    post(201, { id: 'abc' });
    expect(await openTerminal()).toEqual({ id: 'abc' });
    post(503);
    expect(await openTerminal()).toBe('offline');
    post(504);
    expect(await openTerminal()).toBe('busy');
    post(401);
    expect(await openTerminal()).toBe('unauthorized');
  });

  it('closes a terminal idempotently', async () => {
    post(204);
    expect(await closeTerminal('abc')).toBe(true);
    post(404);
    expect(await closeTerminal('abc')).toBe(true);
    post(503);
    expect(await closeTerminal('abc')).toBe('offline');
  });
});
