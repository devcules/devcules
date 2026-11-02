// Extensions — browse and install MCP servers and skills for the coding
// agents on this machine, against the on-disk truth (never a devcules
// record). Two views:
//
//  • AT A GLANCE: a summary strip of what's already here — counts per
//    kind across the agents, plus a health signal. Expand to read the
//    per-agent list on demand.
//  • SEARCH: one box with an MCP / Skills toggle. MCP searches the
//    official registry and installs into a chosen harness; Skills
//    searches skills.sh and installs to every agent via the skills CLI.
//
// The installed lists live under the matching search tab (install there,
// remove there); the glance strip is read-only. Ported from kaithos'
// Extensions page — devcules-native cookie session (401 → onSessionExpired)
// and theme tokens. The "ask the assistant" affordance is out: the backend
// answers it 409 (not wired to devcules' router).

import { useCallback, useEffect, useState } from 'react';
import {
  getMCPFleet, getSkillsFleet, inspectEntry, installEntry, listInstalled,
  registryErr, removeInstalled, searchRegistry,
  type CatalogEntry, type FleetMCP, type FleetSkill, type Inspection, type InstalledItem,
} from '../lib/registryApi';

type Kind = 'mcp' | 'skill';

const AGENT_LABEL: Record<string, string> = {
  shared: 'shared (all agents)',
  'claude-code': 'Claude Code',
  codex: 'Codex',
  'hermes-agent': 'Hermes Agent',
};
const agentLabel = (a: string): string => AGENT_LABEL[a] ?? a;

const KIND_META: Record<Kind, { title: string; placeholder: string; hint: string; installWord: string }> = {
  mcp: {
    title: 'MCP server',
    placeholder: 'search the MCP registry…',
    hint: 'registry.modelcontextprotocol.io',
    installWord: 'install into',
  },
  skill: {
    title: 'Skill',
    placeholder: 'search skills.sh…',
    hint: 'skills.sh — installs to every agent',
    installWord: 'install',
  },
};

// --------------------------------------------------------------- glance

// Glance is the summary strip: per-kind counts across the agents, a
// broken count when anything's unwell, and an inline expand to the
// per-agent detail (which is the fleet data, read live).
function Glance({
  mcp, skills, onExpand,
}: {
  mcp: FleetMCP[];
  skills: FleetSkill[];
  onExpand: () => void;
}) {
  const brokenMcp = mcp.filter((m) => m.status === 'broken').length;
  const brokenSkills = skills.filter((s) => s.status === 'broken').length;
  const agents = Array.from(new Set(mcp.flatMap((m) => m.agents ?? []).concat(skills.flatMap((s) => s.agents ?? []))));
  const broken = brokenMcp + brokenSkills;
  return (
    <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
      <span className="text-body text-text">
        <strong className="font-semibold">{mcp.length}</strong> MCP server{mcp.length === 1 ? '' : 's'}
      </span>
      <span className="text-body text-text">
        <strong className="font-semibold">{skills.length}</strong> skill{skills.length === 1 ? '' : 's'}
      </span>
      {broken > 0 && <span className="text-body text-red-400">{broken} broken</span>}
      {agents.length > 0 && (
        <span className="text-tab text-muted">across {agents.map(agentLabel).join(', ')}</span>
      )}
      <button type="button" onClick={onExpand} className="ml-auto text-tab text-accent">
        details
      </button>
    </div>
  );
}

// Detail lists each configured MCP server then each skill, one row each,
// with the agents that have it. Shown only when the glance expands.
function FleetDetail({ mcp, skills }: { mcp: FleetMCP[]; skills: FleetSkill[] }) {
  if (mcp.length === 0 && skills.length === 0) return null;
  return (
    <div className="mt-2 border-t border-muted pt-3">
      <ul className="divide-y divide-muted">
        {mcp.map((item) => (
          <li key={`mcp:${item.name}`} className="flex flex-wrap items-baseline gap-x-2 gap-y-1 py-1.5">
            <Dot status={item.status} />
            <span className="text-body font-medium text-text">{item.name}</span>
            {item.transport && <span className="text-tab text-muted">{item.transport}</span>}
            <AgentBadges agents={item.agents ?? []} />
          </li>
        ))}
        {skills.map((s) => (
          <li key={`skill:${s.source}:${s.name}`} className="flex flex-wrap items-baseline gap-x-2 gap-y-1 py-1.5">
            <Dot status={s.status} />
            <span className="text-body font-medium text-text">{s.name}</span>
            {s.description && <span className="text-tab text-muted">{s.description}</span>}
            <AgentBadges agents={s.agents ?? []} />
          </li>
        ))}
      </ul>
    </div>
  );
}

function Dot({ status }: { status: string }) {
  const c = status === 'ok' ? 'bg-emerald-500' : status === 'broken' ? 'bg-amber-500' : 'bg-red-500';
  return <span aria-hidden className={`inline-block h-2 w-2 shrink-0 rounded-full ${c}`} />;
}

function AgentBadges({ agents }: { agents: string[] }) {
  return (
    <span className="ml-auto flex flex-wrap gap-1">
      {agents.map((a) => (
        <span key={a} className="rounded bg-base px-1.5 py-0.5 text-tab text-muted">
          {agentLabel(a)}
        </span>
      ))}
    </span>
  );
}

// --------------------------------------------------------------- pieces

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
            className="min-h-9 rounded bg-surface px-2 text-tab text-text"
            autoComplete="off"
          />
        </label>
      ))}
    </div>
  );
}

// InstalledList is the remove-here list under a search tab.
function InstalledList({
  items, loading, removing, onRemove,
}: {
  items: InstalledItem[];
  loading: boolean;
  removing: Record<string, boolean>;
  onRemove: (name: string) => void;
}) {
  if (loading) return <p className="py-2 text-tab text-muted">reading installed…</p>;
  if (items.length === 0) return null;
  return (
    <div className="mb-3">
      <p className="mb-1 text-tab text-muted">installed</p>
      <ul className="divide-y divide-muted">
        {items.map((s) => (
          <li key={s.name} className="flex items-center gap-2 py-1.5">
            <span className="text-body text-text">{s.name}</span>
            {s.source && <span className="text-tab text-muted">{s.source}</span>}
            <button
              type="button"
              onClick={() => onRemove(s.name)}
              disabled={removing[s.name]}
              className="ml-auto min-h-8 rounded bg-base px-2 text-tab text-red-400 disabled:opacity-60"
            >
              {removing[s.name] ? 'removing…' : 'remove'}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ---------------------------------------------------------------- page

export default function Extensions({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [kind, setKind] = useState<Kind>('mcp');
  const meta = KIND_META[kind];

  // ---- glance (fleet) ----
  const [mcp, setMcp] = useState<FleetMCP[] | null>(null);
  const [skills, setSkills] = useState<FleetSkill[] | null>(null);
  const [fleetErr, setFleetErr] = useState('');
  const [expanded, setExpanded] = useState(false);

  // ---- search (shared box, kind toggle) ----
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<CatalogEntry[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [searchErr, setSearchErr] = useState('');

  // ---- per-entry install state ----
  const [openId, setOpenId] = useState<string | null>(null);
  const [inspections, setInspections] = useState<Record<string, Inspection>>({});
  const [inspErr, setInspErr] = useState<Record<string, string>>({});
  const [inspLoading, setInspLoading] = useState<Record<string, boolean>>({});
  const [envValues, setEnvValues] = useState<Record<string, string>>({});
  const [installName, setInstallName] = useState<Record<string, string>>({});
  const [installing, setInstalling] = useState<Record<string, boolean>>({});
  const [installErr, setInstallErr] = useState<Record<string, string>>({});
  const [harness, setHarness] = useState<'claude-code' | 'codex'>('claude-code');

  // ---- installed (per kind) ----
  const [installed, setInstalled] = useState<Record<Kind, InstalledItem[]>>({ mcp: [], skill: [] });
  const [installedLoading, setInstalledLoading] = useState(true);
  const [removing, setRemoving] = useState<Record<string, boolean>>({});
  const [removeErr, setRemoveErr] = useState('');

  const loadInstalled = useCallback(() => {
    listInstalled(kind).then((r) => {
      setInstalledLoading(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      setInstalled((p) => ({ ...p, [kind]: r === 'unreachable' || registryErr(r) ? [] : r }));
    });
  }, [kind, onSessionExpired]);

  // Mount: fleet + both installed lists.
  useEffect(() => {
    let on = true;
    getMCPFleet().then((r) => {
      if (!on) return;
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setFleetErr('server unreachable'); setMcp([]); return; }
      const e = registryErr(r);
      if (e) { setFleetErr(e.message); setMcp([]); return; }
      setMcp(Array.isArray(r) ? r : []);
    });
    getSkillsFleet().then((r) => {
      if (!on) return;
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setFleetErr('server unreachable'); setSkills([]); return; }
      const e = registryErr(r);
      if (e) { setFleetErr(e.message); setSkills([]); return; }
      const flat: FleetSkill[] = [];
      for (const agent of r) for (const s of agent.skills ?? []) flat.push({ ...s, agents: [agent.agent] });
      setSkills(flat);
    });
    return () => { on = false; };
  }, [onSessionExpired]);

  useEffect(() => { loadInstalled(); }, [loadInstalled]);

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    setSearching(true);
    setSearchErr('');
    searchRegistry(kind, query).then((r) => {
      setSearching(false);
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setSearchErr('server unreachable'); setResults([]); return; }
      const er = registryErr(r);
      if (er) { setSearchErr(er.message); setResults([]); return; }
      setResults(r);
    });
  };

  const inspect = (entry: CatalogEntry) => {
    setOpenId(entry.id);
    if (inspections[entry.id]) return;
    setInspLoading((p) => ({ ...p, [entry.id]: true }));
    setInspErr((p) => ({ ...p, [entry.id]: '' }));
    inspectEntry(kind, entry, kind === 'mcp' ? envValues : {}).then((r) => {
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
    installEntry(kind, name, entry, kind === 'mcp' ? envValues : {}).then((r) => {
      setInstalling((p) => ({ ...p, [entry.id]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setInstallErr((p) => ({ ...p, [entry.id]: er?.message ?? 'server unreachable' })); return; }
      loadInstalled();
      setOpenId(null);
    });
  };

  const remove = (name: string) => {
    if (!window.confirm(`Remove ${meta.title} "${name}"?`)) return;
    setRemoveErr('');
    setRemoving((p) => ({ ...p, [name]: true }));
    removeInstalled(kind, name).then((r) => {
      setRemoving((p) => ({ ...p, [name]: false }));
      if (r === 'unauthorized') { onSessionExpired(); return; }
      const er = registryErr(r);
      if (r === 'unreachable' || er) { setRemoveErr(er?.message ?? 'server unreachable'); return; }
      loadInstalled();
    });
  };

  const switchKind = (k: Kind) => {
    if (k === kind) return;
    setKind(k);
    setResults(null);
    setSearchErr('');
    setOpenId(null);
  };

  return (
    <div className="flex flex-col gap-4 p-4">
      <header>
        <h2 className="text-lead font-semibold text-text">Extensions</h2>
        <p className="mt-1 text-body text-muted">
          MCP servers and skills for the coding agents on this machine.
        </p>
      </header>

      <section className="rounded-lg border border-muted bg-surface px-4 py-3" aria-label="At a glance">
        {mcp === null || skills === null ? (
          <p className="text-body text-muted">reading what's here…</p>
        ) : (
          <>
            {fleetErr && <p role="alert" className="mb-1 text-tab text-red-400">{fleetErr}</p>}
            <Glance mcp={mcp} skills={skills} onExpand={() => setExpanded((e) => !e)} />
            {expanded && <FleetDetail mcp={mcp} skills={skills} />}
          </>
        )}
      </section>

      <section className="rounded-lg border border-muted bg-surface" aria-label="Search the registries">
        <div className="flex items-center gap-2 border-b border-muted px-4 py-3">
          {(['mcp', 'skill'] as const).map((k) => (
            <button
              key={k}
              type="button"
              aria-pressed={kind === k}
              onClick={() => switchKind(k)}
              className={`min-h-9 rounded px-3 text-tab ${kind === k ? 'bg-accent text-on-accent' : 'bg-base text-muted'}`}
            >
              {k === 'mcp' ? 'MCP servers' : 'Skills'}
            </button>
          ))}
          <span className="ml-auto text-tab text-muted">{meta.hint}</span>
        </div>

        <div className="px-4 py-3">
          <form onSubmit={search} className="mb-3 flex gap-2">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={meta.placeholder}
              aria-label={meta.placeholder}
              className="min-h-10 min-w-0 flex-1 rounded bg-base px-3 text-body text-text"
            />
            <button type="submit" disabled={searching} className="min-h-10 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">
              {searching ? 'searching…' : 'search'}
            </button>
          </form>
          {searchErr && <p role="alert" className="mb-2 text-tab text-red-400">{searchErr}</p>}

          {kind === 'mcp' && (
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <span className="text-tab text-muted">{meta.installWord}</span>
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
          )}

          <InstalledList items={installed[kind]} loading={installedLoading} removing={removing} onRemove={remove} />
          {removeErr && <p role="alert" className="mb-2 text-tab text-red-400">{removeErr}</p>}

          {results === null ? (
            <p className="py-3 text-body text-muted">search to add a {meta.title.toLowerCase()}</p>
          ) : results.length === 0 ? (
            <p className="py-3 text-body text-muted">no {meta.title.toLowerCase()}s match</p>
          ) : (
            <ul className="divide-y divide-muted">
              {results.map((entry) => {
                const open = openId === entry.id;
                const insp = inspections[entry.id];
                return (
                  <li key={entry.id} className="py-2">
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <button
                        type="button"
                        onClick={() => (open ? setOpenId(null) : inspect(entry))}
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
                        {inspLoading[entry.id] && <p className="text-tab text-muted">{kind === 'skill' ? 'fetching SKILL.md…' : 'inspecting…'}</p>}
                        {inspErr[entry.id] && <p role="alert" className="text-tab text-red-400">{inspErr[entry.id]}</p>}
                        {insp && (
                          <div>
                            {kind === 'skill' && insp.skill_md && (
                              <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded bg-surface p-2 text-tab text-muted">
                                {insp.skill_md}
                              </pre>
                            )}
                            {kind === 'mcp' && (insp.tools ?? []).length > 0 && (
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
                            {kind === 'mcp' && (
                              <EnvFields
                                hints={(entry.install as { env_vars?: { name: string; description?: string; secret?: boolean }[] }).env_vars ?? []}
                                values={envValues}
                                onChange={(n, v) => setEnvValues((p) => ({ ...p, [n]: v }))}
                              />
                            )}
                            <div className="mt-3 flex flex-wrap items-center gap-2">
                              {kind === 'mcp' && (
                                <input
                                  value={installName[entry.id] ?? entry.name}
                                  onChange={(e) => setInstallName((p) => ({ ...p, [entry.id]: e.target.value }))}
                                  aria-label="install name"
                                  className="min-h-9 w-40 rounded bg-surface px-2 text-tab text-text"
                                />
                              )}
                              <button
                                type="button"
                                onClick={() => install(entry)}
                                disabled={installing[entry.id]}
                                className="min-h-9 rounded bg-accent px-3 text-tab text-on-accent disabled:opacity-60"
                              >
                                {installing[entry.id]
                                  ? 'installing…'
                                  : kind === 'skill'
                                    ? 'install (all agents)'
                                    : `${meta.installWord} ${agentLabel(harness)}`}
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
        </div>
      </section>
    </div>
  );
}
