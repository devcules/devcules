import { describe, expect, it, vi } from 'vitest';
import {
  beginPasskeyLogin,
  beginRegisterPasskey,
  closeTerminal,
  deletePasskey,
  finishPasskeyLogin,
  finishRegisterPasskey,
  listPasskeys,
  listTerminals,
  me,
  openTerminal,
  passkeyStatus,
  renamePasskey,
  signIn,
} from './api';

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

  it('reads the passkey status and its disabled reason', async () => {
    post(200, { enabled: true });
    expect(await passkeyStatus()).toEqual({ enabled: true });
    post(200, { enabled: false, reason: 'Set DEVCULES_PUBLIC_URL.' });
    expect(await passkeyStatus()).toEqual({ enabled: false, reason: 'Set DEVCULES_PUBLIC_URL.' });
    post(200, { enabled: false });
    expect(await passkeyStatus()).toEqual({ enabled: false, reason: 'Passkeys are off.' });
    post(200, { enabled: 'yes' });
    expect(await passkeyStatus()).toBe('unreachable');
    post(500);
    expect(await passkeyStatus()).toBe('unreachable');
  });

  it('reports a dead status endpoint as unreachable', async () => {
    window.fetch = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }) as typeof fetch;
    expect(await passkeyStatus()).toBe('unreachable');
  });

  it('runs the register ceremony calls', async () => {
    post(200, { challenge: 'abc' });
    expect(await beginRegisterPasskey()).toEqual({ options: { challenge: 'abc' } });
    post(401);
    expect(await beginRegisterPasskey()).toBe('unauthorized');
    post(400);
    expect(await beginRegisterPasskey()).toBe('disabled');
    post(500);
    expect(await beginRegisterPasskey()).toBe('unreachable');
    post(201);
    expect(await finishRegisterPasskey({})).toBe('created');
    post(400);
    expect(await finishRegisterPasskey({})).toBe('invalid');
    post(401);
    expect(await finishRegisterPasskey({})).toBe('unauthorized');
    post(500);
    expect(await finishRegisterPasskey({})).toBe('unreachable');
  });

  it('runs the login ceremony calls', async () => {
    post(200, { challenge: 'abc' });
    expect(await beginPasskeyLogin()).toEqual({ options: { challenge: 'abc' } });
    post(400);
    expect(await beginPasskeyLogin()).toBe('disabled');
    post(500);
    expect(await beginPasskeyLogin()).toBe('unreachable');
    post(204);
    expect(await finishPasskeyLogin({})).toBe('ok');
    post(401);
    expect(await finishPasskeyLogin({})).toBe('failed');
    post(400);
    expect(await finishPasskeyLogin({})).toBe('failed');
    post(429);
    expect(await finishPasskeyLogin({})).toBe('limited');
    post(500);
    expect(await finishPasskeyLogin({})).toBe('unreachable');
  });

  it('lists passkeys and filters broken entries', async () => {
    const good = { id: 'abc', name: 'Phone', created: 1, lastUsed: 2, backedUp: false };
    post(200, { passkeys: [good, null, { id: 'bad' }] });
    expect(await listPasskeys()).toEqual({ passkeys: [good] });
    post(401);
    expect(await listPasskeys()).toBe('unauthorized');
    post(500);
    expect(await listPasskeys()).toBe('unreachable');
    post(200, { passkeys: 'nope' });
    expect(await listPasskeys()).toBe('unreachable');
  });

  it('renames and deletes passkeys', async () => {
    post(204);
    expect(await renamePasskey('abc', 'Tablet')).toBe(true);
    post(400);
    expect(await renamePasskey('abc', '')).toBe('invalid');
    post(404);
    expect(await renamePasskey('abc', 'Ghost')).toBe('missing');
    post(401);
    expect(await renamePasskey('abc', 'Ghost')).toBe('unauthorized');
    post(500);
    expect(await renamePasskey('abc', 'Ghost')).toBe('unreachable');
    post(204);
    expect(await deletePasskey('abc')).toBe(true);
    post(401);
    expect(await deletePasskey('abc')).toBe('unauthorized');
    post(500);
    expect(await deletePasskey('abc')).toBe('unreachable');
  });
});
