import { useCallback, useEffect, useState } from 'react';
import {
  addProject,
  closeTerminal,
  listAgents,
  listProjects,
  listTerminals,
  openProjectTerminal,
  projectWhere,
  removeProject,
  startAgent,
  type AgentInfo,
  type Project,
  type ProjectKind,
  type TerminalSummary,
  type Where,
} from '../api';
import TerminalView from './TerminalView';
import { Empty } from './Empty';

type View = { view: 'loading' } | { view: 'list' } | { view: 'terminal'; id: string; where: Where };

// where says in words which runner or machine a project lives on.
function whereWords(p: Project): string {
  return p.kind === 'machine' ? `machine ${p.target}` : `runner ${p.target}`;
}

function refusedWords(res: 'offline' | 'unreachable' | 'busy' | { refused: string }): string {
  if (typeof res === 'object') {
    return res.refused;
  }
  if (res === 'offline') {
    return 'That runner or machine is offline. Start it and try again.';
  }
  if (res === 'busy') {
    return 'It did not answer. Try again.';
  }
  return 'Something went wrong. Try again.';
}

export default function Projects({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<View>({ view: 'loading' });
  const [projects, setProjects] = useState<Project[]>([]);
  const [runners, setRunners] = useState<string[]>([]);
  const [machines, setMachines] = useState<string[]>([]);
  const [open, setOpen] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const refresh = useCallback((): Promise<void> => {
    return listProjects().then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setView((v) => (v.view === 'loading' ? { view: 'list' } : v));
        if (res === 'unreachable') {
          setNotice(refusedWords(res));
          return;
        }
        setNotice(null);
        setProjects(res.projects);
        setRunners(res.runners);
        setMachines(res.machines);
      },
      () => {
        setView((v) => (v.view === 'loading' ? { view: 'list' } : v));
        setNotice(refusedWords('unreachable'));
      },
    );
  }, [onSessionExpired]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const show = (id: string, where: Where) => setView({ view: 'terminal', id, where });

  if (view.view === 'loading') {
    return <div className="flex flex-1 items-center justify-center" aria-busy="true" />;
  }
  if (view.view === 'terminal') {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <TerminalView
          id={view.id}
          where={view.where}
          onSessionExpired={onSessionExpired}
          onBack={() => {
            setView({ view: 'list' });
            void refresh();
          }}
        />
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lead font-semibold text-text">Projects</h2>
        {!adding && (
          <button type="button" onClick={() => setAdding(true)} className="min-h-11 rounded-md bg-accent px-4 text-body text-base">
            Add a project
          </button>
        )}
      </div>
      {notice !== null && (
        <p role="alert" className="text-body text-text">
          {notice}
        </p>
      )}
      {adding && (
        <AddProject
          runners={runners}
          machines={machines}
          onSessionExpired={onSessionExpired}
          onCancel={() => setAdding(false)}
          onAdded={(p, terminal) => {
            setAdding(false);
            void refresh();
            setOpen(p.id);
            if (terminal !== '') {
              // The clone runs in this terminal: show it at once.
              show(terminal, projectWhere(p));
            }
          }}
        />
      )}
      {projects.length === 0 && !adding && notice === null && (
        <Empty title="No projects yet." body="Add a repo by its git URL or a folder that is already there, then open terminals and agents inside it." />
      )}
      {projects.length > 0 && (
        <ul className="flex flex-col gap-3">
          {projects.map((p) => (
            <ProjectRow
              key={p.id}
              project={p}
              online={p.kind === 'machine' ? machines.includes(p.target) : runners.includes(p.target)}
              expanded={open === p.id}
              onToggle={() => setOpen((o) => (o === p.id ? null : p.id))}
              onShow={show}
              onRemoved={() => void refresh()}
              onSessionExpired={onSessionExpired}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

// ProjectRow is one project: where it lives, and when expanded its
// terminals, a new terminal, and the agents its runner offers.
function ProjectRow({
  project: p,
  online,
  expanded,
  onToggle,
  onShow,
  onRemoved,
  onSessionExpired,
}: {
  project: Project;
  online: boolean;
  expanded: boolean;
  onToggle: () => void;
  onShow: (id: string, where: Where) => void;
  onRemoved: () => void;
  onSessionExpired: () => void;
}) {
  const [terms, setTerms] = useState<TerminalSummary[]>([]);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const where = projectWhere(p);

  const load = useCallback(() => {
    void listTerminals(where.machine ? { machine: where.machine } : undefined).then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      setTerms(typeof res === 'object' ? res.terminals.filter((t) => t.project === p.id) : []);
    });
    if (p.kind === 'runner') {
      void listAgents().then((res) => {
        setAgents(typeof res === 'object' && res.runner === p.target ? res.agents.filter((a) => a.installed) : []);
      });
    }
  }, [p.id, p.kind, p.target, where.machine, onSessionExpired]);

  useEffect(() => {
    if (expanded && online) {
      load();
    }
  }, [expanded, online, load]);

  async function terminal(): Promise<void> {
    setBusy('terminal');
    setError(null);
    const res = await openProjectTerminal(p.id);
    setBusy(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'object' && 'id' in res) {
      onShow(res.id, where);
      return;
    }
    setError(refusedWords(res));
  }

  async function agent(id: string): Promise<void> {
    setBusy(id);
    setError(null);
    const res = await startAgent(id, p.id);
    setBusy(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'object' && 'id' in res) {
      onShow(res.id, where);
      return;
    }
    setError(refusedWords(res));
  }

  async function close(id: string): Promise<void> {
    if ((await closeTerminal(id, where.machine ? { machine: where.machine } : undefined)) === 'unauthorized') {
      onSessionExpired();
      return;
    }
    load();
  }

  async function remove(): Promise<void> {
    if ((await removeProject(p.id)) === 'unauthorized') {
      onSessionExpired();
      return;
    }
    onRemoved();
  }

  return (
    <li className="flex flex-col gap-2 rounded-md bg-surface px-3 py-3">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="flex min-h-11 items-center gap-2 text-left"
      >
        <span aria-hidden="true" className={`h-2.5 w-2.5 shrink-0 rounded-full ${online ? 'bg-text' : 'border border-muted'}`} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-body font-semibold text-text">{p.name}</span>
          <span className="block truncate text-body text-muted">
            {whereWords(p)} · <span className="font-mono">{p.dir}</span>
            {online ? '' : ' · offline'}
          </span>
        </span>
        <span aria-hidden="true" className="text-muted">
          {expanded ? '▾' : '▸'}
        </span>
      </button>
      {expanded && (
        <div className="flex flex-col gap-2">
          {p.repo && <p className="break-all font-mono text-body text-muted">{p.repo}</p>}
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => void terminal()}
              disabled={!online || busy !== null}
              className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
            >
              {busy === 'terminal' ? 'Opening…' : 'New terminal'}
            </button>
            {agents.map((a) => (
              <button
                key={a.id}
                type="button"
                onClick={() => void agent(a.id)}
                disabled={!online || busy !== null}
                aria-label={`Start ${a.name} in ${p.name}`}
                className="min-h-11 rounded-md bg-base px-4 text-body text-text disabled:opacity-60"
              >
                {busy === a.id ? 'Starting…' : `Start ${a.name}`}
              </button>
            ))}
            {confirm ? (
              <>
                <button type="button" onClick={() => void remove()} className="min-h-11 rounded-md bg-accent px-3 text-body text-base">
                  Forget it
                </button>
                <button type="button" onClick={() => setConfirm(false)} className="min-h-11 rounded-md px-3 text-body text-text">
                  Keep
                </button>
              </>
            ) : (
              <button type="button" onClick={() => setConfirm(true)} className="min-h-11 rounded-md px-3 text-body text-muted">
                Forget project
              </button>
            )}
          </div>
          {confirm && <p className="text-body text-muted">This only removes it from the list. Its files and terminals stay.</p>}
          {error !== null && (
            <p role="alert" className="text-body text-text">
              {error}
            </p>
          )}
          {terms.length > 0 && (
            <ul aria-label={`Terminals in ${p.name}`} className="flex flex-col gap-1">
              {terms.map((t) => (
                <li key={t.term} className="flex min-h-11 items-center gap-2 rounded-md bg-base px-3">
                  <button
                    type="button"
                    onClick={() => onShow(t.term, where)}
                    aria-label={`Open terminal ${t.term.slice(0, 8)}`}
                    className="min-h-11 min-w-0 flex-1 truncate text-left font-mono text-body text-text"
                  >
                    {t.agent ? `${t.agent} · ${t.term.slice(0, 8)}` : t.term.slice(0, 8)}
                  </button>
                  <button
                    type="button"
                    onClick={() => void close(t.term)}
                    aria-label={`Close terminal ${t.term.slice(0, 8)}`}
                    className="min-h-11 min-w-11 rounded-md px-3 text-body text-muted"
                  >
                    ✕
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </li>
  );
}

// AddProject adds a repo by git URL (cloned in a visible terminal) or
// a folder already on the runner or machine.
function AddProject({
  runners,
  machines,
  onCancel,
  onAdded,
  onSessionExpired,
}: {
  runners: string[];
  machines: string[];
  onCancel: () => void;
  onAdded: (p: Project, terminal: string) => void;
  onSessionExpired: () => void;
}) {
  const targets: Array<{ kind: ProjectKind; name: string }> = [
    ...runners.map((name) => ({ kind: 'runner' as const, name })),
    ...machines.map((name) => ({ kind: 'machine' as const, name })),
  ];
  const [source, setSource] = useState<'repo' | 'dir'>('repo');
  const [pick, setPick] = useState(0);
  const [repo, setRepo] = useState('');
  const [dir, setDir] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(): Promise<void> {
    const t = targets[pick];
    if (t === undefined) {
      return;
    }
    setBusy(true);
    setError(null);
    const res = await addProject({
      kind: t.kind,
      target: t.name,
      name: name.trim() || undefined,
      repo: source === 'repo' ? repo.trim() : undefined,
      dir: dir.trim() || undefined,
    });
    setBusy(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res === 'unreachable' || 'refused' in res) {
      setError(refusedWords(res));
      return;
    }
    onAdded(res.project, res.terminal);
  }

  if (targets.length === 0) {
    return (
      <section aria-label="Add a project" className="flex flex-col gap-2 rounded-md bg-surface px-3 py-3">
        <p className="text-body text-text">No runner or machine is online. A project lives on one: start the runner, or add a computer in Machines.</p>
        <button type="button" onClick={onCancel} className="min-h-11 self-start rounded-md px-4 text-body text-text">
          Close
        </button>
      </section>
    );
  }

  return (
    <form
      aria-label="Add a project"
      className="flex flex-col gap-3 rounded-md bg-surface px-3 py-3"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <fieldset className="flex gap-2">
        <legend className="sr-only">Where the code comes from</legend>
        {(['repo', 'dir'] as const).map((s) => (
          <button
            key={s}
            type="button"
            aria-pressed={source === s}
            onClick={() => setSource(s)}
            className={`min-h-11 rounded-md px-4 text-body ${source === s ? 'bg-text text-base' : 'bg-base text-text'}`}
          >
            {s === 'repo' ? 'Clone a repo' : 'A folder already there'}
          </button>
        ))}
      </fieldset>
      <label className="flex flex-col gap-1 text-body text-text">
        On
        <select
          value={pick}
          onChange={(e) => setPick(Number(e.target.value))}
          className="min-h-11 rounded-md bg-base px-3 text-body text-text"
        >
          {targets.map((t, i) => (
            <option key={`${t.kind}/${t.name}`} value={i}>
              {t.kind === 'machine' ? `${t.name} (machine)` : `${t.name} (runner)`}
            </option>
          ))}
        </select>
      </label>
      {source === 'repo' && (
        <label className="flex flex-col gap-1 text-body text-text">
          Git URL
          <input
            type="text"
            required
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            placeholder="https://github.com/you/app.git or git@github.com:you/app.git"
            className="min-h-11 rounded-md bg-base px-3 font-mono text-body text-text"
          />
        </label>
      )}
      <label className="flex flex-col gap-1 text-body text-text">
        {source === 'repo' ? 'Folder (optional)' : 'Folder'}
        <input
          type="text"
          required={source === 'dir'}
          value={dir}
          onChange={(e) => setDir(e.target.value)}
          placeholder={source === 'repo' ? 'Defaults to the repo name in the workspace' : '/Users/you/src/app, or app inside the workspace'}
          className="min-h-11 rounded-md bg-base px-3 font-mono text-body text-text"
        />
      </label>
      <label className="flex flex-col gap-1 text-body text-text">
        Name (optional)
        <input
          type="text"
          maxLength={64}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Defaults to the folder name"
          className="min-h-11 rounded-md bg-base px-3 text-body text-text"
        />
      </label>
      {source === 'repo' && (
        <p className="text-body text-muted">The clone runs in a terminal you see, so you can answer a password or key prompt there.</p>
      )}
      {error !== null && (
        <p role="alert" className="text-body text-text">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <button type="submit" disabled={busy} className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60">
          {busy ? 'Adding…' : source === 'repo' ? 'Clone it' : 'Add it'}
        </button>
        <button type="button" onClick={onCancel} className="min-h-11 rounded-md px-4 text-body text-text">
          Cancel
        </button>
      </div>
    </form>
  );
}
