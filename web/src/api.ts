// Same-origin API calls. The server answers 401 when the session
// dies; every call funnels through here so the app returns to
// sign-in from anywhere, never strand on a dead session.
export type SignInResult = 'ok' | 'invalid' | 'limited';

export async function signIn(username: string, password: string): Promise<SignInResult> {
  const body = new URLSearchParams({ username, password });
  const res = await fetch('/auth/sign-in', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  });
  // 204 is the new session (M1.2 changed the old 303 redirect: JS is
  // in charge now). Anything else maps to plain words in the screen.
  if (res.status === 204) {
    return 'ok';
  }
  if (res.status === 429) {
    return 'limited';
  }
  return 'invalid';
}

export async function me(): Promise<string | null> {
  const res = await fetch('/auth/me');
  if (!res.ok) {
    return null;
  }
  const data: unknown = await res.json();
  if (typeof data === 'object' && data !== null && 'username' in data && typeof data.username === 'string') {
    return data.username;
  }
  return null;
}

export async function signOut(): Promise<void> {
  await fetch('/auth/sign-out', { method: 'POST' });
}
