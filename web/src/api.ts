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

export type TerminalSummary = { term: string; created: number; agent?: string };

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
          typeof t.created === 'number' &&
          (!('agent' in t) || typeof t.agent === 'string'),
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

// Agents (M3.3): the coding agent CLIs a runner offers, started in a
// terminal of their own with a fresh launch token.
export type AgentInfo = {
  id: string;
  name: string;
  provider: string;
  installed: boolean;
  version?: string;
  install?: string;
};

export type RunnerAgents = { runner: string; mode: AccessMode | ''; agents: AgentInfo[] };

function isAgent(a: unknown): a is AgentInfo {
  return (
    typeof a === 'object' &&
    a !== null &&
    'id' in a &&
    typeof a.id === 'string' &&
    'name' in a &&
    typeof a.name === 'string' &&
    'installed' in a &&
    typeof a.installed === 'boolean'
  );
}

export async function listAgents(): Promise<RunnerAgents | TerminalError> {
  const res = await terminalFetch('/api/agents');
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (typeof data === 'object' && data !== null && 'agents' in data && Array.isArray(data.agents)) {
    const d = data as { runner?: unknown; mode?: unknown };
    return {
      runner: typeof d.runner === 'string' ? d.runner : '',
      mode: typeof d.mode === 'string' ? (d.mode as AccessMode | '') : '',
      agents: data.agents.filter(isAgent),
    };
  }
  return 'unreachable';
}

// startAgent opens a terminal running agent. A refusal (not
// installed, no provider) carries the server's reason.
export async function startAgent(agent: string): Promise<{ id: string } | TerminalError | 'busy' | { refused: string }> {
  const res = await terminalFetch('/api/agents', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent }),
  });
  if (typeof res === 'string') {
    return res;
  }
  const data: unknown = await res.json().catch(() => null);
  if (res.status === 201 && typeof data === 'object' && data !== null && 'id' in data && typeof data.id === 'string') {
    return { id: data.id };
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (res.status === 504) {
    return 'busy';
  }
  if (res.status >= 400 && res.status < 500 && isObject(data) && typeof data.error === 'string') {
    return { refused: data.error };
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

// Approvals (M3.2): agents ask before critical tool calls, and the
// owner answers here or from a push notice. Commands arrive with
// secrets already masked by the server.
export type ApprovalStatus = 'pending' | 'allowed' | 'denied' | 'expired';
export type Approval = {
  id: string;
  launchId: string;
  runner: string;
  agent: string;
  tool: string;
  command: string;
  rule?: string;
  ruleName?: string;
  irreversible: boolean;
  status: ApprovalStatus;
  created: string;
  expires: string;
  answered?: string;
  answeredBy?: string;
};
export type AccessMode = 'off' | 'critical' | 'all' | 'full';
export type AccessTarget = { target: string; runner: string; linked: boolean; mode: AccessMode | '' };
export type CriticalRule = { id: string; name: string; pattern: string; enabled: boolean; builtin: boolean };
export type ApprovalSettings = {
  targets: AccessTarget[];
  modes: AccessMode[];
  rules: CriticalRule[];
  timeoutSeconds: number;
};
// A write either lands (the fresh value), or the server refused it
// with a reason written for people ('invalid'), or something broke.
export type Write<T> = Promise<T | { invalid: string } | 'unauthorized' | 'unreachable'>;

async function sendJSON<T>(
  path: string,
  method: string,
  body: unknown,
  valid: (v: Record<string, unknown>) => boolean,
): Write<T> {
  const res = await redactFetch(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (typeof res === 'string') {
    return res;
  }
  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    // 204 and broken bodies both land here; status decides below.
  }
  if (res.status === 400 && isObject(data) && typeof data.error === 'string') {
    return { invalid: data.error };
  }
  if (!res.ok) {
    return 'unreachable';
  }
  return isObject(data) && valid(data) ? (data as T) : 'unreachable';
}

export function listApprovals(status: ApprovalStatus | '' = '', limit = 50): Read<{ approvals: Approval[]; pending: number }> {
  const q = new URLSearchParams({ limit: String(limit) });
  if (status) {
    q.set('status', status);
  }
  return readJSON(`/api/approvals?${q.toString()}`, (d) => Array.isArray(d.approvals) && typeof d.pending === 'number');
}

// answerApproval resolves 'gone' when the approval is unknown or was
// answered already (it expired, or another device got there first).
export async function answerApproval(
  id: string,
  allow: boolean,
): Promise<Approval | 'gone' | 'unauthorized' | 'unreachable'> {
  const res = await redactFetch(`/api/approvals/${encodeURIComponent(id)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ decision: allow ? 'allow' : 'deny' }),
  });
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 404 || res.status === 409) {
    return 'gone';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  try {
    const data: unknown = await res.json();
    return isObject(data) && typeof data.id === 'string' ? (data as Approval) : 'unreachable';
  } catch {
    return 'unreachable';
  }
}

const validSettings = (d: Record<string, unknown>) =>
  Array.isArray(d.targets) && Array.isArray(d.modes) && Array.isArray(d.rules) && typeof d.timeoutSeconds === 'number';

export function getApprovalSettings(): Read<ApprovalSettings> {
  return readJSON('/api/approvals/settings', validSettings);
}

export function setAccessMode(target: string, mode: AccessMode): Write<ApprovalSettings> {
  return sendJSON('/api/approvals/modes', 'PUT', { target, mode }, validSettings);
}

export function setCriticalRules(rules: CriticalRule[]): Write<ApprovalSettings> {
  return sendJSON('/api/approvals/rules', 'PUT', { rules }, validSettings);
}

export function checkCommand(command: string): Write<{ critical: boolean; rule?: CriticalRule }> {
  return sendJSON('/api/approvals/check', 'POST', { command }, (d) => typeof d.critical === 'boolean');
}

// Web Push (M3.2). The server keeps endpoints to itself: a device
// shows its id, label and push service host only.
export type PushDevice = { id: string; label: string; service: string; created: string; lastOk?: string };
export type PushResult = { sent: number; failed: number; removed: number };

export function getPush(): Read<{ publicKey: string; devices: PushDevice[] }> {
  return readJSON('/api/push', (d) => typeof d.publicKey === 'string' && Array.isArray(d.devices));
}

export function addPushDevice(subscription: PushSubscriptionJSON, label: string): Write<{ id: string }> {
  return sendJSON('/api/push/devices', 'POST', { ...subscription, label }, (d) => typeof d.id === 'string');
}

export async function removePushDevice(id: string): Promise<true | 'unauthorized' | 'unreachable'> {
  const res = await redactFetch(`/api/push/devices/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (typeof res === 'string') {
    return res;
  }
  // Already gone counts as removed.
  return res.status === 204 || res.status === 404 ? true : 'unreachable';
}

export function testPush(): Write<PushResult> {
  return sendJSON('/api/push/test', 'POST', undefined, (d) => typeof d.sent === 'number');
}

// Machines (M4.5): the task view over a linked machine. Every call
// resolves like the terminal ones: data, an expired session, a
// missing machine ('offline'), or an unreachable server. A refusal
// (budget exhausted, driver missing) carries the machine's reason.
export type MachineError = 'unauthorized' | 'offline' | 'unreachable';

export type MachineList = { machines: string[]; budget: number };
export type GroundMatch = {
  id: number;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  maskW: number;
  maskH: number;
  clickX: number;
  clickY: number;
};
export type MachineTree = {
  screenW?: number;
  screenH?: number;
  front?: string;
  window?: string;
  elements: Array<{
    role: string;
    x: number;
    y: number;
    w: number;
    h: number;
    title?: string;
    value?: string;
  }>;
  note?: string;
};
export type VerifyResult = { png: string; tree: MachineTree; note: string };

async function machineFetch(path: string, init?: RequestInit): Promise<Response | MachineError> {
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

function machineQuery(machine: string): string {
  return machine === '' ? '' : `?machine=${encodeURIComponent(machine)}`;
}

export async function listMachines(): Promise<MachineList | MachineError> {
  const res = await machineFetch('/api/machines');
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (isObject(data) && Array.isArray(data.machines) && typeof data.budget === 'number') {
    return {
      machines: data.machines.filter((m): m is string => typeof m === 'string'),
      budget: data.budget,
    };
  }
  return 'unreachable';
}

export async function machineBudget(machine = ''): Promise<{ remaining: number } | MachineError> {
  const res = await machineFetch(`/api/machines-budget${machineQuery(machine)}`);
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (isObject(data) && typeof data.remaining === 'number') {
    return { remaining: data.remaining };
  }
  return 'unreachable';
}

// machineScreenURL points at the live capture; the <img> re-fetches
// it on its own, so no JSON wrapper here.
export function machineScreenURL(machine: string): string {
  return `/api/machines-screen${machineQuery(machine)}`;
}

export async function machineTree(machine = ''): Promise<MachineTree | MachineError> {
  const res = await machineFetch(`/api/machines-tree${machineQuery(machine)}`);
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (!res.ok) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (isObject(data) && Array.isArray(data.elements)) {
    return data as unknown as MachineTree;
  }
  return 'unreachable';
}

// machineInput injects one event — the take-control call. The machine
// spends the budget there and refuses at zero; the refusal carries
// its reason.
export async function machineInput(
  ev: { kind: string; x?: number; y?: number; key?: string; text?: string },
  machine = '',
): Promise<true | MachineError | { refused: string }> {
  const res = await machineFetch(`/api/machines-input${machineQuery(machine)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(ev),
  });
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 204) {
    return true;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (res.status === 504) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (res.status >= 400 && res.status < 500 && isObject(data) && typeof data.error === 'string') {
    return { refused: data.error };
  }
  if (res.status === 502 && isObject(data) && typeof data.error === 'string') {
    return { refused: data.error };
  }
  return 'unreachable';
}

export async function machineGround(
  phrase: string,
  machine = '',
): Promise<{ matches: GroundMatch[] } | MachineError | { refused: string }> {
  const res = await machineFetch(`/api/machines-ground${machineQuery(machine)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ phrase }),
  });
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (res.status === 504) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (res.ok && isObject(data) && Array.isArray(data.matches)) {
    return { matches: data.matches as GroundMatch[] };
  }
  if (res.status >= 400 && isObject(data) && typeof data.error === 'string') {
    return { refused: data.error };
  }
  return 'unreachable';
}

export async function machineVerify(machine = ''): Promise<VerifyResult | MachineError | { refused: string }> {
  const res = await machineFetch(`/api/machines-verify${machineQuery(machine)}`, { method: 'POST' });
  if (typeof res === 'string') {
    return res;
  }
  if (res.status === 503) {
    return 'offline';
  }
  if (res.status === 504) {
    return 'unreachable';
  }
  const data: unknown = await res.json().catch(() => null);
  if (res.ok && isObject(data) && typeof data.png === 'string' && isObject(data.tree) && typeof data.note === 'string') {
    return data as unknown as VerifyResult;
  }
  if (res.status >= 400 && isObject(data) && typeof data.error === 'string') {
    return { refused: data.error };
  }
  return 'unreachable';
}
