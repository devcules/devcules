import { describe, expect, it, vi } from 'vitest';
import { me, signIn } from './api';

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
});
