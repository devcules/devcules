// Extensions — the extensions registry: three views of the local coding
// agents' add-ons, all reading the on-disk truth rather than any
// devcules-side record of "what we installed".
//
//  1. FLEET: what each coding agent on this machine has RIGHT NOW — MCP
//     servers per harness (claude-code, codex) and skills per agent,
//     scanned live from their config files and skills dirs.
//  2. MCP REGISTRY: search registry.modelcontextprotocol.io, inspect the
//     server's real tool list, install into one chosen harness.
//  3. SKILLS REGISTRY: search skills.sh, read the real SKILL.md, install
//     via the `skills` CLI (which propagates to every agent).
//
// Ported from kaithos' Extensions page. Devcules-native: cookie session
// (401 → onSessionExpired), the registryApi Result union, and the theme
// tokens (bg-surface/border-muted/text-muted) instead of kaithos' k-*
// classes. The "explain with the assistant" affordance from kaithos is
// left out: the backend answers it 409 (not wired to devcules' router).

import { useCallback, useEffect, useState } from 'react';
import {
  getMCPFleet, getSkillsFleet, inspectEntry, installEntry, listInstalled,
  registryErr, removeInstalled, searchRegistry,
  type CatalogEntry, type FleetMCP, type FleetSkill, type Inspection, type InstalledItem,
} from '../lib/registryApi';

const MCP_KIND = 'mcp' as const;
const SKILL_KIND = 'skill' as const;

const AGENT_LABEL: Record<string, string> = {
  shared: 'shared (all agents)',
  'claude-code': 'Claude Code',
  codex: 'Codex',
  'hermes-agent': 'Hermes Agent',
};

function agentLabel(a: string): string {
  return AGENT_LABEL[a] ?? a;
}

function StatusDot({ status }: { status: string }) {
  const color =
    status === 'ok' ? 'bg-emerald-500' : status === 'broken' ? 'bg-amber-500' : 'bg-red-500';
  return <span aria-hidden className={`inline-block h-2 w-2 shrink-0 rounded-full ${color}`} />;
}

function Panel({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-muted bg-surface">
      <header className="flex flex-wrap items-baseline gap-x-3 border-b border-muted px-4 py-3">
        <h3 className="text-body font-semibold text-text">{title}</h3>
        {hint && <span className="text-tab text-muted">{hint}</span>}
      </header>
      <div className="px-4 py-3">{children}</div>
    </section>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="py-3 text-body text-muted">{children}</p>;
}

function Loading({ children }: { children: React.ReactNode }) {
  return <p className="py-3 text-body text-muted">{children}…</p>;
}

// ---------------------------------------------------------------- fleet

// MCPFleet lists each configured MCP server with the harnesses that have
// it — one row per server, badges per agent, status dot for broken ones.
function MCPFleet({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [items, setItems] = useState<FleetMCP[] | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    let on = true;
    getMCPFleet().then((r) => {
      if (!on) return;
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setErr('server unreachable'); setItems([]); return; }
      const e = registryErr(r);
      if (e) { setErr(e.message); setItems([]); return; }
      setItems(Array.isArray(r) ? r : []);
    });
    return () => { on = false; };
  }, [onSessionExpired]);

  if (items === null) return <Loading>reading each agent's MCP config</Loading>;
  return (
    <div>
      {err && <p role="alert" className="mb-2 text-tab text-red-400">{err}</p>}
      {items.length === 0 && !err && <Empty>no MCP servers configured in any agent</Empty>}
      <ul className="divide-y divide-muted">
        {items.map((item) => (
          <li key={item.name} className="flex flex-wrap items-baseline gap-x-2 gap-y-1 py-2">
            <StatusDot status={item.status} />
            <span className="text-body font-medium text-text">{item.name}</span>
            {item.transport && <span className="text-tab text-muted">{item.transport}</span>}
            <span className="ml-auto flex flex-wrap gap-1">
              {(item.agents ?? []).map((a) => (
                <span key={a} className="rounded bg-base px-1.5 py-0.5 text-tab text-muted">{agentLabel(a)}</span>
              ))}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

// SkillsFleet lists skills per agent, flattened to one row per skill with
// the agents that see it.
function SkillsFleet({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [rows, setRows] = useState<FleetSkill[] | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    let on = true;
    getSkillsFleet().then((r) => {
      if (!on) return;
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setErr('server unreachable'); setRows([]); return; }
      const e = registryErr(r);
      if (e) { setErr(e.message); setRows([]); return; }
      const flat: FleetSkill[] = [];
      for (const agent of r) {
        for (const s of agent.skills ?? []) flat.push({ ...s, agents: [agent.agent] });
      }
      setRows(flat);
    });
    return () => { on = false; };
  }, [onSessionExpired]);

  if (rows === null) return <Loading>reading each agent's skills</Loading>;
  return (
    <div>
      {err && <p role="alert" className="mb-2 text-tab text-red-400">{err}</p>}
      {rows.length === 0 && !err && <Empty>no skills installed in any agent</Empty>}
      <ul className="divide-y divide-muted">
        {rows.map((s) => (
          <li key={`${s.source}:${s.name}`} className="flex flex-wrap items-baseline gap-x-2 gap-y-1 py-2">
            <StatusDot status={s.status} />
            <span className="text-body font-medium text-text">{s.name}</span>
            {s.description && <span className="text-tab text-muted">{s.description}</span>}
            <span className="ml-auto flex flex-wrap gap-1">
              {(s.agents ?? []).map((a) => (
                <span key={a} className="rounded bg-base px-1.5 py-0.5 text-tab text-muted">{agentLabel(a)}</span>
              ))}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ------------------------------------------------------------- registry

// EnvFields renders the input set for an MCP server's environment
// variables, used pre-install once inspect reveals what the server needs.
function EnvFields({
  hints, values, onChange,
}: {
  hints: { name: string; description?: string; secret?: boolean }[];
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
}) {
  if (hints.length === 0) return null;
  return (
    <div className="mt-2 flex flex-col gap-2">
      {hints.map((h) => (
        <label key={h.name} className="flex flex-col gap-1">
          <span className="text-tab text-muted">
            {h.name}
            {h.secret && <span className="text-amber-500"> (secret)</span>}
            {h.description && <span> — {h.description}</span>}
          </span>
          <input
            type={h.secret ? 'password' : 'text'}
            aria-label={h.name}
            value={values[h.name] ?? ''}
            onChange={(e) => onChange(h.name, e.target.value)}
            className="min-h-9 rounded bg-base px-2 text-tab text-text"
            autoComplete="off"
          />
        </label>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------- page

export default function Extensions({ onSessionExpired }: { onSessionExpired: () => void }) {
  // ---- MCP registry ----
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<CatalogEntry[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [searchErr, setSearchErr] = useState('');
  const [harness, setHarness] = useState<'claude-code' | 'codex'>('claude-code');

  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [inspections, setInspections] = useState<Record<string, Inspection>>({});
  const [inspErr, setInspErr] = useState<Record<string, string>>({});
  const [inspLoading, setInspLoading] = useState<Record<string, boolean>>({});
  const [envValues, setEnvValues] = useState<Record<string, string>>({});
  const [installName, setInstallName] = useState<Record<string, string>>({});
  const [installing, setInstalling] = useState<Record<string, boolean>>({});
  const [installErr, setInstallErr] = useState<Record<string, string>>({});

  const [installed, setInstalled] = useState<InstalledItem[]>([]);
  const [installedLoading, setInstalledLoading] = useState(true);
  const [removing, setRemoving] = useState<Record<string, boolean>>({});

  // ---- skills registry ----
  const [sq, setSq] = useState('');
  const [sResults, setSResults] = useState<CatalogEntry[] | null>(null);
  const [sSearching, setSSearching] = useState(false);
  const [sErr, setSErr] = useState('');
  const [sExpanded, setSExpanded] = useState<string | null>(null);
  const [sInspect, setSInspect] = useState<Record<string, Inspection>>({});
  const [sInspErr, setSInspErr] = useState<Record<string, string>>({});
  const [sInspLoading, setSInspLoading] = useState<Record<string, boolean>>({});
  const [sInstalling, setSInstalling] = useState<Record<string, boolean>>({});
  const [sInstallErr, setSInstallErr] = useState<Record<string, string>>({});

  const [skillsInstalled, setSkillsInstalled] = useState<InstalledItem[]>([]);
  const [skillsLoading, setSkillsLoading] = useState(true);
  const [sRemoving, setSRemoving] = useState<Record<string, boolean>>({});
  const [removeErr, setRemoveErr] = useState('');

  // Refresh loaders for the install/remove buttons: they set the loading
  // flag here and set data in the promise callback. The mount effect fetches
  // inline (no loader call) so the rule against sync set-state-in-effect
  // sees no state write in the effect body.
  const loadInstalled = useCallback(() => {
    setInstalledLoading(true);
    listInstalled(MCP_KIND).then((r) => {
      setInstalledLoading(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      setInstalled(r === 'unreachable' || registryErr(r) ? [] : r);
    });
  }, [onSessionExpired]);

  const loadSkillsInstalled = useCallback(() => {
    setSkillsLoading(true);
    listInstalled(SKILL_KIND).then((r) => {
      setSkillsLoading(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      setSkillsInstalled(r === 'unreachable' || registryErr(r) ? [] : r);
    });
  }, [onSessionExpired]);

  useEffect(() => {
    let on = true;
    listInstalled(MCP_KIND).then((r) => {
      if (!on) return;
      setInstalledLoading(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      setInstalled(r === 'unreachable' || registryErr(r) ? [] : r);
    });
    listInstalled(SKILL_KIND).then((r) => {
      if (!on) return;
      setSkillsLoading(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      setSkillsInstalled(r === 'unreachable' || registryErr(r) ? [] : r);
    });
    return () => { on = false; };
  }, [onSessionExpired]);

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    setSearching(true);
    setSearchErr('');
    searchRegistry(MCP_KIND, query).then((r) => {
      setSearching(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setSearchErr('server unreachable'); setResults([]); return; }
      const er = registryErr(r);
      if (er) { setSearchErr(er.message); setResults([]); return; }
      setResults(r);
    });
  };

  const inspect = (entry: CatalogEntry) => {
    setExpandedId(entry.id);
    if (inspections[entry.id]) return;
    setInspLoading((p) => ({ ...p, [entry.id]: true }));
    setInspErr((p) => ({ ...p, [entry.id]: '' }));
    inspectEntry(MCP_KIND, entry, envValues).then((r) => {
      setInspLoading((p) => ({ ...p, [entry.id]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setInspErr((p) => ({ ...p, [entry.id]: er?.message ?? 'server unreachable' })); return; }
      setInspections((p) => ({ ...p, [entry.id]: r }));
    });
  };

  const install = (entry: CatalogEntry) => {
    const name = (installName[entry.id] ?? entry.name).trim();
    setInstalling((p) => ({ ...p, [entry.id]: true }));
    setInstallErr((p) => ({ ...p, [entry.id]: '' }));
    installEntry(MCP_KIND, name, entry, envValues).then((r) => {
      setInstalling((p) => ({ ...p, [entry.id]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setInstallErr((p) => ({ ...p, [entry.id]: er?.message ?? 'server unreachable' })); return; }
      loadInstalled();
      setExpandedId(null);
    });
  };

  const sSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setSSearching(true);
    setSErr('');
    searchRegistry(SKILL_KIND, sq).then((r) => {
      setSSearching(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setSErr('server unreachable'); setSResults([]); return; }
      const er = registryErr(r);
      if (er) { setSErr(er.message); setSResults([]); return; }
      setSResults(r);
    });
  };

  const sDoInspect = (entry: CatalogEntry) => {
    setSExpanded(entry.id);
    if (sInspect[entry.id]) return;
    setSInspLoading((p) => ({ ...p, [entry.id]: true }));
    setSInspErr((p) => ({ ...p, [entry.id]: '' }));
    inspectEntry(SKILL_KIND, entry, {}).then((r) => {
      setSInspLoading((p) => ({ ...p, [entry.id]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setSInspErr((p) => ({ ...p, [entry.id]: er?.message ?? 'server unreachable' })); return; }
      setSInspect((p) => ({ ...p, [entry.id]: r }));
    });
  };

  const sInstall = (entry: CatalogEntry) => {
    setSInstalling((p) => ({ ...p, [entry.id]: true }));
    setSInstallErr((p) => ({ ...p, [entry.id]: '' }));
    installEntry(SKILL_KIND, entry.name, entry, {}).then((r) => {
      setSInstalling((p) => ({ ...p, [entry.id]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setSInstallErr((p) => ({ ...p, [entry.id]: er?.message ?? 'server unreachable' })); return; }
      loadSkillsInstalled();
      setSExpanded(null);
    });
  };

  const remove = (name: string) => {
    if (!window.confirm(`Remove MCP server "${name}"?`)) return;
    setRemoveErr('');
    setRemoving((p) => ({ ...p, [name]: true }));
    removeInstalled(MCP_KIND, name).then((r) => {
      setRemoving((p) => ({ ...p, [name]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setRemoveErr(er?.message ?? 'server unreachable'); return; }
      loadInstalled();
    });
  };

  const sRemove = (name: string) => {
    if (!window.confirm(`Remove skill "${name}"?`)) return;
    setRemoveErr('');
    setSRemoving((p) => ({ ...p, [name]: true }));
    removeInstalled(SKILL_KIND, name).then((r) => {
      setSRemoving((p) => ({ ...p, [name]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setRemoveErr(er?.message ?? 'server unreachable'); return; }
      loadSkillsInstalled();
    });
  };

  return (
    <div className="flex flex-col gap-6 p-4">
      <header>
        <h2 className="text-lead font-semibold text-text">Extensions</h2>
        <p className="mt-1 text-body text-muted">
          MCP servers and skills for the coding agents on this machine. Reads each agent's real config
          and skills dirs; installs shell out to <code className="text-tab">claude mcp</code> and the{' '}
          <code className="text-tab">skills</code> CLI.
        </p>
      </header>

      <Panel title="Fleet" hint="what each agent already has, read live">
        <MCPFleet onSessionExpired={onSessionExpired} />
        <div className="mt-4 border-t border-muted pt-3">
          <SkillsFleet onSessionExpired={onSessionExpired} />
        </div>
      </Panel>

      <Panel title="MCP registry" hint={`install into: ${agentLabel(harness)}`}>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <span className="text-tab text-muted">install target</span>
          {(['claude-code', 'codex'] as const).map((h) => (
            <button
              key={h}
              type="button"
              aria-pressed={harness === h}
              onClick={() => setHarness(h)}
              className={`min-h-9 rounded px-3 text-tab ${harness === h ? 'bg-accent text-on-accent' : 'bg-base text-muted'}`}
            >
              {agentLabel(h)}
            </button>
          ))}
        </div>

        <form onSubmit={search} className="mb-3 flex gap-2">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="search the MCP registry…"
            aria-label="search the MCP registry"
            className="min-h-10 min-w-0 flex-1 rounded bg-base px-3 text-body text-text"
          />
          <button type="submit" disabled={searching} className="min-h-10 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">
            {searching ? 'searching…' : 'search'}
          </button>
        </form>
        {searchErr && <p role="alert" className="mb-2 text-tab text-red-400">{searchErr}</p>}

        {installedLoading ? (
          <Loading>reading installed servers</Loading>
        ) : installed.length > 0 ? (
          <div className="mb-4">
            <p className="mb-1 text-tab text-muted">installed</p>
            <ul className="divide-y divide-muted">
              {installed.map((s) => (
                <li key={s.name} className="flex items-center gap-2 py-2">
                  <span className="text-body text-text">{s.name}</span>
                  {s.source && <span className="text-tab text-muted">{s.source}</span>}
                  <button
                    type="button"
                    onClick={() => remove(s.name)}
                    disabled={removing[s.name]}
                    className="ml-auto min-h-8 rounded bg-base px-2 text-tab text-red-400 disabled:opacity-60"
                  >
                    {removing[s.name] ? 'removing…' : 'remove'}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {results === null ? (
          <Empty>search the MCP registry to add a server</Empty>
        ) : results.length === 0 ? (
          <Empty>no servers match</Empty>
        ) : (
          <ul className="divide-y divide-muted">
            {results.map((entry) => {
              const open = expandedId === entry.id;
              const insp = inspections[entry.id];
              return (
                <li key={entry.id} className="py-2">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <button
                      type="button"
                      onClick={() => (open ? setExpandedId(null) : inspect(entry))}
                      className="text-left text-body font-medium text-text underline decoration-muted underline-offset-2"
                    >
                      {entry.name}
                    </button>
                    {entry.repo_url && (
                      <a href={entry.repo_url} target="_blank" rel="noreferrer" className="text-tab text-accent">
                        repo ↗
                      </a>
                    )}
                    <span className="ml-auto text-tab text-muted">{entry.source_name}</span>
                  </div>
                  <p className="mt-0.5 text-tab text-muted">{entry.description}</p>

                  {open && (
                    <div className="mt-2 rounded border border-muted bg-base p-3">
                      {inspLoading[entry.id] && <p className="text-tab text-muted">inspecting…</p>}
                      {inspErr[entry.id] && <p role="alert" className="text-tab text-red-400">{inspErr[entry.id]}</p>}
                      {insp && (
                        <div>
                          {(insp.tools ?? []).length > 0 && (
                            <div className="mb-2">
                              <p className="mb-1 text-tab text-muted">tools it exposes</p>
                              <ul className="flex flex-wrap gap-1">
                                {(insp.tools ?? []).map((t) => (
                                  <li key={t.name} title={t.description} className="rounded bg-surface px-1.5 py-0.5 text-tab text-muted">
                                    {t.name}
                                  </li>
                                ))}
                              </ul>
                            </div>
                          )}
                          <EnvFields
                            hints={(entry.install as { env_vars?: { name: string; description?: string; secret?: boolean }[] }).env_vars ?? []}
                            values={envValues}
                            onChange={(n, v) => setEnvValues((p) => ({ ...p, [n]: v }))}
                          />
                          <div className="mt-3 flex flex-wrap items-center gap-2">
                            <input
                              value={installName[entry.id] ?? entry.name}
                              onChange={(e) => setInstallName((p) => ({ ...p, [entry.id]: e.target.value }))}
                              aria-label="install name"
                              className="min-h-9 w-40 rounded bg-surface px-2 text-tab text-text"
                            />
                            <button
                              type="button"
                              onClick={() => install(entry)}
                              disabled={installing[entry.id]}
                              className="min-h-9 rounded bg-accent px-3 text-tab text-on-accent disabled:opacity-60"
                            >
                              {installing[entry.id] ? 'installing…' : `install into ${agentLabel(harness)}`}
                            </button>
                          </div>
                          {installErr[entry.id] && <p role="alert" className="mt-2 text-tab text-red-400">{installErr[entry.id]}</p>}
                        </div>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </Panel>

      <Panel title="Skills registry" hint="browse skills.sh · installs to every agent">
        <form onSubmit={sSearch} className="mb-3 flex gap-2">
          <input
            value={sq}
            onChange={(e) => setSq(e.target.value)}
            placeholder="search skills.sh…"
            aria-label="search skills.sh"
            className="min-h-10 min-w-0 flex-1 rounded bg-base px-3 text-body text-text"
          />
          <button type="submit" disabled={sSearching} className="min-h-10 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">
            {sSearching ? 'searching…' : 'search'}
          </button>
        </form>
        {sErr && <p role="alert" className="mb-2 text-tab text-red-400">{sErr}</p>}

        {skillsLoading ? (
          <Loading>reading installed skills</Loading>
        ) : skillsInstalled.length > 0 ? (
          <div className="mb-4">
            <p className="mb-1 text-tab text-muted">installed</p>
            <ul className="divide-y divide-muted">
              {skillsInstalled.map((s) => (
                <li key={s.name} className="flex items-center gap-2 py-2">
                  <span className="text-body text-text">{s.name}</span>
                  {s.source && <span className="text-tab text-muted">{s.source}</span>}
                  <button
                    type="button"
                    onClick={() => sRemove(s.name)}
                    disabled={sRemoving[s.name]}
                    className="ml-auto min-h-8 rounded bg-base px-2 text-tab text-red-400 disabled:opacity-60"
                  >
                    {sRemoving[s.name] ? 'removing…' : 'remove'}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {sResults === null ? (
          <Empty>search skills.sh to add a skill</Empty>
        ) : sResults.length === 0 ? (
          <Empty>no skills match</Empty>
        ) : (
          <ul className="divide-y divide-muted">
            {sResults.map((entry) => {
              const open = sExpanded === entry.id;
              const insp = sInspect[entry.id];
              return (
                <li key={entry.id} className="py-2">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <button
                      type="button"
                      onClick={() => (open ? setSExpanded(null) : sDoInspect(entry))}
                      className="text-left text-body font-medium text-text underline decoration-muted underline-offset-2"
                    >
                      {entry.name}
                    </button>
                    {entry.repo_url && (
                      <a href={entry.repo_url} target="_blank" rel="noreferrer" className="text-tab text-accent">
                        repo ↗
                      </a>
                    )}
                    <span className="ml-auto text-tab text-muted">{entry.source_name}</span>
                  </div>
                  <p className="mt-0.5 text-tab text-muted">{entry.description}</p>

                  {open && (
                    <div className="mt-2 rounded border border-muted bg-base p-3">
                      {sInspLoading[entry.id] && <p className="text-tab text-muted">fetching SKILL.md…</p>}
                      {sInspErr[entry.id] && <p role="alert" className="text-tab text-red-400">{sInspErr[entry.id]}</p>}
                      {insp?.skill_md && (
                        <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded bg-surface p-2 text-tab text-muted">
                          {insp.skill_md}
                        </pre>
                      )}
                      <div className="mt-3">
                        <button
                          type="button"
                          onClick={() => sInstall(entry)}
                          disabled={sInstalling[entry.id]}
                          className="min-h-9 rounded bg-accent px-3 text-tab text-on-accent disabled:opacity-60"
                        >
                          {sInstalling[entry.id] ? 'installing…' : 'install (all agents)'}
                        </button>
                      </div>
                      {sInstallErr[entry.id] && <p role="alert" className="mt-2 text-tab text-red-400">{sInstallErr[entry.id]}</p>}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </Panel>

      {removeErr && <p role="alert" className="text-tab text-red-400">{removeErr}</p>}
    </div>
  );
}
