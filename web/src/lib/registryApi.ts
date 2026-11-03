// registryApi.ts — the Extensions screen's client for the devcules
// extensions registry (/api/registry/*, /api/mcp/fleet, /api/skills/fleet).
// Cookie-session like the rest of the app (no bearer token), returning
// the deskApi Result union so the screen can tell "signed out" and
// "server down" from a real answer. Unlike the desk proxy, the registry
// answers errors as {error:{code,message}} with a machine-readable code
// (not_implemented, registry_unreachable, no_inference_backend, …), and
// the screen switches on the code — so this module surfaces it instead
// of flattening to a string.

export type Result<T> = T | 'unauthorized' | 'unreachable';

export type ApiErr = { code: string; message: string };

async function req<T>(path: string, init?: RequestInit): Promise<Result<T>> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    return 'unreachable';
  }
  if (res.status === 401) return 'unauthorized';
  if (res.status >= 500 && res.status !== 502) return 'unreachable';
  if (!res.ok) {
    try {
      const j = (await res.json()) as { error?: { code?: string; message?: string } };
      return ({ __err: { code: j.error?.code ?? `http_${res.status}`, message: j.error?.message ?? `HTTP ${res.status}` } }) as unknown as Result<T>;
    } catch {
      return 'unreachable';
    }
  }
  if (res.status === 204) return true as unknown as T;
  try {
    return (await res.json()) as T;
  } catch {
    return 'unreachable';
  }
}

const json = (method: string, path: string, body: unknown) =>
  req<true>(path, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

// registryErr extracts the {code,message} a failed Result carries, or
// null if the result wasn't a registry error (data, unauthorized, …).
export function registryErr<T>(r: Result<T>): ApiErr | null {
  if (typeof r === 'object' && r !== null && '__err' in (r as object)) {
    return (r as unknown as { __err: ApiErr }).__err;
  }
  return null;
}

export type EnvVarHint = { name: string; description?: string; required?: boolean; secret?: boolean; default?: string };

export type EnvVarHintAlias = EnvVarHint;

export type CatalogEntry = {
  id: string;
  kind: 'mcp' | 'skill' | 'agent';
  name: string;
  description: string;
  source_id: string;
  source_name: string;
  repo_url?: string;
  install: MCPInstallSpec | SkillInstallSpec;
};

export type MCPInstallSpec = {
  type: 'stdio' | 'http' | 'sse';
  command?: string;
  args?: string[];
  url?: string;
  env_vars?: EnvVarHint[];
};

export type SkillInstallSpec = { repo: string; skill: string; installs?: number };

export type ToolInfo = { name: string; description?: string };

export type Inspection = {
  tools?: ToolInfo[];
  skill_md?: string;
  readme?: string;
  install_command?: string;
};

export type InstalledItem = {
  name: string;
  kind: string;
  source?: string;
  status?: string;
  harnesses?: string[];
  repo_url?: string;
};

// Fleet rows: one configured MCP server or one skill, with the agents
// that see it ("shared" | "claude-code" | "codex" | "hermes-agent").
export type FleetMCP = { name: string; status: 'ok' | 'broken' | string; error?: string; agents: string[]; source_url?: string; transport?: string };
export type AgentSkills = { agent: string; skills: FleetSkill[] };
export type FleetSkill = { name: string; description?: string; source: string; status: 'ok' | 'broken' | string; error?: string; agents: string[]; source_url?: string };

// searchRegistry queries one kind's catalog. loose skips the server's
// relevance filter (?loose=1), used only by the "no strong matches" escape
// hatch — the default path always filters.
export const searchRegistry = (kind: 'mcp' | 'skill', q: string, opts?: { loose?: boolean }) =>
  req<CatalogEntry[]>(`/api/registry/${kind}/search?q=${encodeURIComponent(q)}${opts?.loose ? '&loose=1' : ''}`);
export const inspectEntry = (kind: 'mcp' | 'skill', entry: CatalogEntry, params: Record<string, string>) =>
  req<Inspection>(`/api/registry/${kind}/inspect`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ entry, params }),
  });
export const installEntry = (kind: 'mcp' | 'skill', name: string, entry: CatalogEntry, params: Record<string, string>) =>
  json('POST', `/api/registry/${kind}/install`, { name, entry, params });
export const listInstalled = (kind: 'mcp' | 'skill') => req<InstalledItem[]>(`/api/registry/${kind}/installed`);
export const removeInstalled = (kind: 'mcp' | 'skill', name: string, harness?: string) =>
  req<true>(`/api/registry/${kind}/installed/${encodeURIComponent(name)}${harness ? `?harness=${encodeURIComponent(harness)}` : ''}`, { method: 'DELETE' });

export const getMCPFleet = () => req<FleetMCP[]>('/api/mcp/fleet');
export const getSkillsFleet = () => req<AgentSkills[]>('/api/skills/fleet');
