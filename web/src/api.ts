// Same-origin API calls. The session check is me(): the app runs it
// on load and after sign-in, and a 401 there shows sign-in. Later
// data calls must route their 401s the same way.
export type SignInResult = 'ok' | 'invalid' | 'limited' | 'unreachable';

export async function signIn(username: string, password: string): Promise<SignInResult> {
  const body = new URLSearchParams({ username, password });
  let res: Response;
  try {
    res = await fetch('/auth/sign-in', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body,
    });
  } catch {
    return 'unreachable';
  }
  // 204 is the new session (M1.2 changed the old 303 redirect: JS is
  // in charge now). Anything else maps to plain words in the screen,
  // and a server fault is never blamed on the password.
  if (res.status === 204) {
    return 'ok';
  }
  if (res.status === 429) {
    return 'limited';
  }
  if (res.status >= 500) {
    return 'unreachable';
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
