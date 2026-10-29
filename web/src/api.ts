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

// Passkeys. Ceremony posts carry JSON; a dead server is always
// 'unreachable', and only the session-carrying calls can answer
// 'unauthorized' (the app returns to sign-in on those).
export type PasskeyStatus = { enabled: true } | { enabled: false; reason: string };

export async function passkeyStatus(): Promise<PasskeyStatus | 'unreachable'> {
  let res: Response;
  try {
    res = await fetch('/auth/passkeys/status');
  } catch {
    return 'unreachable';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  let data: unknown;
  try {
    data = await res.json();
  } catch {
    return 'unreachable';
  }
  if (typeof data === 'object' && data !== null && 'enabled' in data) {
    if (data.enabled === true) {
      return { enabled: true };
    }
    if (data.enabled === false) {
      const reason =
        'reason' in data && typeof data.reason === 'string' ? data.reason : 'Passkeys are off.';
      return { enabled: false, reason };
    }
  }
  return 'unreachable';
}

async function passkeyPost(path: string, body?: unknown): Promise<Response | 'unreachable'> {
  try {
    return await fetch(
      path,
      body === undefined
        ? { method: 'POST' }
        : {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          },
    );
  } catch {
    return 'unreachable';
  }
}

async function passkeyOptions(res: Response): Promise<{ options: unknown } | 'unreachable'> {
  try {
    return { options: (await res.json()) as unknown };
  } catch {
    return 'unreachable';
  }
}

// A begin answers 400 only when passkeys are off (the server has no
// other 400 path there); the screen then re-reads the status reason.
export async function beginRegisterPasskey(): Promise<
  { options: unknown } | 'unauthorized' | 'disabled' | 'unreachable'
> {
  const res = await passkeyPost('/auth/passkeys/register/begin');
  if (res === 'unreachable') {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  if (res.status === 200) {
    return passkeyOptions(res);
  }
  if (res.status === 400) {
    return 'disabled';
  }
  return 'unreachable';
}

export async function finishRegisterPasskey(
  payload: unknown,
): Promise<'created' | 'unauthorized' | 'invalid' | 'unreachable'> {
  const res = await passkeyPost('/auth/passkeys/register/finish', payload);
  if (res === 'unreachable') {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  if (res.status === 201) {
    return 'created';
  }
  if (res.status === 400) {
    return 'invalid';
  }
  return 'unreachable';
}

export async function beginPasskeyLogin(): Promise<
  { options: unknown } | 'disabled' | 'unreachable'
> {
  const res = await passkeyPost('/auth/passkeys/login/begin');
  if (res === 'unreachable') {
    return 'unreachable';
  }
  if (res.status === 200) {
    return passkeyOptions(res);
  }
  if (res.status === 400) {
    return 'disabled';
  }
  return 'unreachable';
}

// A failed assertion is 401; a stale ceremony or a server fault is
// never blamed on the key: both read as a plain failure, and the
// next try begins a fresh ceremony.
export async function finishPasskeyLogin(
  payload: unknown,
): Promise<'ok' | 'failed' | 'limited' | 'unreachable'> {
  const res = await passkeyPost('/auth/passkeys/login/finish', payload);
  if (res === 'unreachable') {
    return 'unreachable';
  }
  if (res.status === 204) {
    return 'ok';
  }
  if (res.status === 401 || res.status === 400) {
    return 'failed';
  }
  if (res.status === 429) {
    return 'limited';
  }
  return 'unreachable';
}

export type PasskeySummary = {
  id: string;
  name: string;
  created: number;
  lastUsed: number;
  backedUp: boolean;
};

export async function listPasskeys(): Promise<
  { passkeys: PasskeySummary[] } | 'unauthorized' | 'unreachable'
> {
  let res: Response;
  try {
    res = await fetch('/api/passkeys');
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  let data: unknown;
  try {
    data = await res.json();
  } catch {
    return 'unreachable';
  }
  if (typeof data === 'object' && data !== null && 'passkeys' in data && Array.isArray(data.passkeys)) {
    return {
      passkeys: data.passkeys.filter(
        (p): p is PasskeySummary =>
          typeof p === 'object' &&
          p !== null &&
          'id' in p &&
          typeof p.id === 'string' &&
          'name' in p &&
          typeof p.name === 'string' &&
          'created' in p &&
          typeof p.created === 'number' &&
          'lastUsed' in p &&
          typeof p.lastUsed === 'number' &&
          'backedUp' in p &&
          typeof p.backedUp === 'boolean',
      ),
    };
  }
  return 'unreachable';
}

export async function renamePasskey(
  id: string,
  name: string,
): Promise<true | 'unauthorized' | 'invalid' | 'missing' | 'unreachable'> {
  let res: Response;
  try {
    res = await fetch(`/api/passkeys/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  if (res.status === 204) {
    return true;
  }
  if (res.status === 400) {
    return 'invalid';
  }
  if (res.status === 404) {
    return 'missing';
  }
  return 'unreachable';
}

export async function deletePasskey(id: string): Promise<true | 'unauthorized' | 'unreachable'> {
  let res: Response;
  try {
    res = await fetch(`/api/passkeys/${id}`, { method: 'DELETE' });
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  // DELETE stays idempotent server-side: 204 either way.
  if (res.status === 204) {
    return true;
  }
  return 'unreachable';
}

export type TerminalSummary = { term: string; created: number };

// Every terminal call resolves to one of these: data, an expired
// session (the app returns to sign-in), a missing runner (the UI
// shows "runner offline"), or an unreachable server.
export type TerminalError = 'unauthorized' | 'offline' | 'unreachable';

async function terminalFetch(path: string, init?: RequestInit): Promise<Response | TerminalError> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  return res;
}

export async function listTerminals(): Promise<{ terminals: TerminalSummary[] } | TerminalError> {
  const res = await terminalFetch('/api/terminals');
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  const data: unknown = await res.json();
  if (typeof data === 'object' && data !== null && 'terminals' in data && Array.isArray(data.terminals)) {
    return {
      terminals: data.terminals.filter(
        (t): t is TerminalSummary =>
          typeof t === 'object' &&
          t !== null &&
          'term' in t &&
          typeof t.term === 'string' &&
          'created' in t &&
          typeof t.created === 'number',
      ),
    };
  }
  return 'unreachable';
}

export async function openTerminal(): Promise<{ id: string } | TerminalError | 'busy'> {
  const res = await terminalFetch('/api/terminals', { method: 'POST' });
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 201) {
    const data: unknown = await res.json();
    if (typeof data === 'object' && data !== null && 'id' in data && typeof data.id === 'string') {
      return { id: data.id };
    }
    return 'unreachable';
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (res.status === 504) {
    return 'busy';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  return 'unreachable';
}

export async function closeTerminal(id: string): Promise<true | TerminalError> {
  const res = await terminalFetch(`/api/terminals/${id}`, { method: 'DELETE' });
  if (typeof res === 'string') {
    return res;
  }
  // DELETE stays idempotent server-side: 204 either way.
  if (res.status === 204 || res.status === 404) {
    return true;
  }
  if (res.status === 503) {
    return 'offline';
  }
  return 'unreachable';
}

// streamURL builds the terminal WebSocket address for id at the
// current size. The session rides the cookie; cols/rows ride the
// query so the first paint arrives at the right size.
export function streamURL(id: string, cols: number, rows: number): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}/api/terminals/${id}/stream?cols=${cols}&rows=${rows}`;
}

// Redaction (M2.2). Values never come back from the server: findings
// carry the type, the rule and the placeholder only.
export type RedactType = 'SECRET' | 'EMAIL' | 'PHONE' | 'CARD' | 'IPV4';
export type RedactMode = 'off' | 'log' | 'mask' | 'block';
export type RedactPolicy = Record<RedactType, RedactMode>;
export type RedactCounts = { detected: number; masked: number; blocked: number };
export type RedactState = {
  policy: RedactPolicy;
  types: RedactType[];
  modes: RedactMode[];
  stats: Partial<Record<RedactType, RedactCounts>>;
  rules: number;
};
export type RedactFinding = { type: RedactType; rule: string; placeholder?: string };
export type RedactTestResult = { text: string; findings: RedactFinding[]; blocked: RedactType[] };

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

async function redactFetch(path: string, init?: RequestInit): Promise<Response | 'unauthorized' | 'unreachable'> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) {
    return 'unauthorized';
  }
  return res;
}

export async function getRedaction(): Promise<RedactState | 'unauthorized' | 'unreachable'> {
  const res = await redactFetch('/api/redact');
  if (typeof res === 'string') {
    return res;
  }
  if (!res.ok) {
    return 'unreachable';
  }
  let data: unknown;
  try {
    data = await res.json();
  } catch {
    return 'unreachable';
  }
  if (
    isObject(data) &&
    isObject(data.policy) &&
    Array.isArray(data.types) &&
    Array.isArray(data.modes) &&
    isObject(data.stats) &&
    typeof data.rules === 'number'
  ) {
    return data as unknown as RedactState;
  }
  return 'unreachable';
}

export async function setRedactionPolicy(
  policy: RedactPolicy,
): Promise<true | 'unauthorized' | 'unreachable'> {
  const res = await redactFetch('/api/redact/policy', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(policy),
  });
  if (typeof res === 'string') {
    return res;
  }
  return res.status === 204 ? true : 'unreachable';
}

export async function testRedaction(
  text: string,
): Promise<RedactTestResult | 'unauthorized' | 'too-long' | 'unreachable'> {
  const res = await redactFetch('/api/redact/test', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  });
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 400) {
    return 'too-long';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  let data: unknown;
  try {
    data = await res.json();
  } catch {
    return 'unreachable';
  }
  if (isObject(data) && typeof data.text === 'string' && Array.isArray(data.findings) && Array.isArray(data.blocked)) {
    return data as unknown as RedactTestResult;
  }
  return 'unreachable';
}

// Activity (M2.4): model call traces per agent launch, and the audit
// log. Reads only; the server audits trace reads and exports itself.
export type TraceSummary = {
  launchId: string;
  label: string;
  provider: string;
  calls: number;
  inputTokens: number;
  outputTokens: number;
  redacted: number;
  first: string;
  last: string;
};
export type TraceToolCall = { name: string; args: string };
export type TraceCall = {
  id: number;
  at: string;
  provider: string;
  path: string;
  model: string;
  status: number;
  stream: boolean;
  durationMs: number;
  inputTokens: number;
  outputTokens: number;
  redacted: Record<string, number>;
  question: string;
  reply: string;
  toolCalls: TraceToolCall[];
};
export type AuditEntry = {
  seq: number;
  at: string;
  actor: string;
  action: string;
  target?: string;
  ip?: string;
  detail?: Record<string, string>;
};
export type AuditCheck = { ok: boolean; checked: number; brokenAt?: number; reason?: string };

type Read<T> = Promise<T | 'unauthorized' | 'unreachable'>;

async function readJSON<T>(path: string, valid: (v: Record<string, unknown>) => boolean): Read<T> {
  const res = await redactFetch(path);
  if (typeof res === 'string') {
    return res;
  }
  if (!res.ok) {
    return 'unreachable';
  }
  let data: unknown;
  try {
    data = await res.json();
  } catch {
    return 'unreachable';
  }
  return isObject(data) && valid(data) ? (data as T) : 'unreachable';
}

export function listTraces(): Read<{ sessions: TraceSummary[] }> {
  return readJSON('/api/traces', (d) => Array.isArray(d.sessions));
}

export function getTrace(launchId: string): Read<{ calls: TraceCall[] }> {
  return readJSON(`/api/traces/${encodeURIComponent(launchId)}`, (d) => Array.isArray(d.calls));
}

export function listAudit(before = 0): Read<{ entries: AuditEntry[] }> {
  const q = before > 0 ? `?limit=50&before=${before}` : '?limit=50';
  return readJSON(`/api/audit${q}`, (d) => Array.isArray(d.entries));
}

export function checkAudit(): Read<AuditCheck> {
  return readJSON('/api/audit/export?verify=1', (d) => typeof d.ok === 'boolean' && typeof d.checked === 'number');
}

export const auditExportURL = '/api/audit/export';
