import { useEffect, useState } from 'react';
import { addProject, listProjects, projectWhere, type Project, type ProjectKind, type Where } from '../api';
import DirPicker from './DirPicker';
import NewTerminal from './NewTerminal';
import ProjectTree from './ProjectTree';
import TerminalView from './TerminalView';
import { Empty } from './Empty';

// ProjectIntent is what the sidebar asks this screen to show: a
// terminal, the new-terminal form for a project, or the add form.
// seq changes on every ask, so the same ask twice still lands.
export type ProjectAsk =
  | { kind: 'open'; id: string; where: Where }
  | { kind: 'new-terminal'; project: Project }
  | { kind: 'new-project' };
export type ProjectIntent = ProjectAsk & { seq: number };

type View =
  | { view: 'list' }
  | { view: 'add' }
  | { view: 'new-terminal'; project: Project }
  | { view: 'terminal'; id: string; where: Where };

function viewFor(intent: ProjectIntent | undefined): View {
  switch (intent?.kind) {
    case 'open':
      return { view: 'terminal', id: intent.id, where: intent.where };
    case 'new-terminal':
      return { view: 'new-terminal', project: intent.project };
    case 'new-project':
      return { view: 'add' };
    default:
      return { view: 'list' };
  }
}

export default function Projects({
  intent,
  onChanged,
  onSessionExpired,
}: {
  intent?: ProjectIntent;
  // onChanged tells the sidebar tree to reload.
  onChanged?: () => void;
  onSessionExpired: () => void;
}) {
  const [view, setView] = useState<View>(() => viewFor(intent));
  const [reload, setReload] = useState(0);
  const [empty, setEmpty] = useState(false);

  // A new ask from the sidebar replaces whatever is showing. Adjusted
  // while rendering (not in an effect), keyed by the ask's seq.
  const [seen, setSeen] = useState(intent?.seq ?? 0);
  if (intent && intent.seq !== seen) {
    setSeen(intent.seq);
    setView(viewFor(intent));
  }

  useEffect(() => {
    if (view.view !== 'list') {
      return;
    }
    void listProjects().then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      setEmpty(typeof res === 'object' && res.projects.length === 0);
    });
  }, [view, reload, onSessionExpired]);

  const changed = () => {
    setReload((n) => n + 1);
    onChanged?.();
  };
  const back = () => {
    setView({ view: 'list' });
    changed();
  };

  if (view.view === 'terminal') {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <TerminalView id={view.id} where={view.where} onSessionExpired={onSessionExpired} onBack={back} />
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lead font-semibold text-text">Projects</h2>
        {view.view === 'list' && (
          <button type="button" onClick={() => setView({ view: 'add' })} className="min-h-11 rounded-md bg-accent px-4 text-body text-base">
            Add a project
          </button>
        )}
      </div>
      {view.view === 'add' && (
        <AddProject
          onSessionExpired={onSessionExpired}
          onCancel={() => setView({ view: 'list' })}
          onAdded={(p, terminal) => {
            changed();
            // A clone runs in its own terminal: show it at once. A
            // folder goes straight to choosing what runs in it.
            setView(terminal !== '' ? { view: 'terminal', id: terminal, where: projectWhere(p) } : { view: 'new-terminal', project: p });
          }}
        />
      )}
      {view.view === 'new-terminal' && (
        <NewTerminal
          project={view.project}
          onSessionExpired={onSessionExpired}
          onCancel={() => setView({ view: 'list' })}
          onOpened={(id, where) => {
            changed();
            setView({ view: 'terminal', id, where });
          }}
        />
      )}
      {view.view === 'list' && empty && (
        <Empty title="No projects yet." body="Add a folder or a repo, then open terminals and agents inside it." />
      )}
      {view.view === 'list' && (
        <ProjectTree
          reload={reload}
          onSessionExpired={onSessionExpired}
          onOpen={(id, where) => setView({ view: 'terminal', id, where })}
          onNewTerminal={(project) => setView({ view: 'new-terminal', project })}
        />
      )}
    </div>
  );
}

// AddProject adds a folder already on a runner or machine, picked in
// the folder browser (repos marked) or typed, or clones a git URL in a
// terminal you watch.
function AddProject({
  onCancel,
  onAdded,
  onSessionExpired,
}: {
  onCancel: () => void;
  onAdded: (p: Project, terminal: string) => void;
  onSessionExpired: () => void;
}) {
  const [targets, setTargets] = useState<Array<{ kind: ProjectKind; name: string }> | null>(null);
  const [source, setSource] = useState<'folder' | 'clone'>('folder');
  const [pick, setPick] = useState(0);
  const [dir, setDir] = useState('');
  const [git, setGit] = useState(false);
  const [repo, setRepo] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void listProjects().then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (typeof res === 'object') {
        setTargets([
          ...res.runners.map((n) => ({ kind: 'runner' as const, name: n })),
          ...res.machines.map((n) => ({ kind: 'machine' as const, name: n })),
        ]);
      } else {
        setTargets([]);
      }
    });
  }, [onSessionExpired]);

  if (targets === null) {
    return <div aria-busy="true" className="h-11" />;
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
  const target = targets[pick] ?? targets[0];
  const where: Where = target.kind === 'machine' ? { machine: target.name } : {};

  async function submit(): Promise<void> {
    setBusy(true);
    setError(null);
    const res = await addProject({
      kind: target.kind,
      target: target.name,
      name: name.trim() || undefined,
      repo: source === 'clone' ? repo.trim() : undefined,
      dir: dir.trim() || undefined,
      git: source === 'folder' ? git : undefined,
    });
    setBusy(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res === 'unreachable') {
      setError('Something went wrong. Try again.');
      return;
    }
    if ('refused' in res) {
      setError(res.refused);
      return;
    }
    onAdded(res.project, res.terminal);
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
      <h3 className="text-body font-semibold text-text">Add a project</h3>
      <label className="flex flex-col gap-1 text-body text-text">
        On
        <select
          value={pick}
          onChange={(e) => {
            setPick(Number(e.target.value));
            setDir('');
            setGit(false);
          }}
          className="min-h-11 rounded-md bg-base px-3 text-body text-text"
        >
          {targets.map((t, i) => (
            <option key={`${t.kind}/${t.name}`} value={i}>
              {t.kind === 'machine' ? `${t.name} (machine)` : `${t.name} (runner)`}
            </option>
          ))}
        </select>
      </label>
      <fieldset className="flex flex-wrap gap-2">
        <legend className="sr-only">Where the code comes from</legend>
        {(['folder', 'clone'] as const).map((s) => (
          <button
            key={s}
            type="button"
            aria-pressed={source === s}
            onClick={() => setSource(s)}
            className={`min-h-11 rounded-md px-4 text-body ${source === s ? 'bg-text text-base' : 'bg-base text-text'}`}
          >
            {s === 'folder' ? 'Pick a folder' : 'Clone a repo'}
          </button>
        ))}
      </fieldset>
      {source === 'folder' ? (
        <>
          <DirPicker
            where={where}
            selected={dir}
            onSessionExpired={onSessionExpired}
            onSelect={(path, isGit) => {
              setDir(path);
              setGit(isGit);
            }}
          />
          <label className="flex flex-col gap-1 text-body text-text">
            Folder
            <input
              type="text"
              required
              value={dir}
              onChange={(e) => {
                setDir(e.target.value);
                setGit(false);
              }}
              placeholder="Pick one above, or type a path"
              className="min-h-11 rounded-md bg-base px-3 font-mono text-body text-text"
            />
          </label>
          {git && <p className="text-body text-muted">⎇ A git repo.</p>}
        </>
      ) : (
        <>
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
          <label className="flex flex-col gap-1 text-body text-text">
            Folder (optional)
            <input
              type="text"
              value={dir}
              onChange={(e) => setDir(e.target.value)}
              placeholder="Defaults to the repo name in the workspace"
              className="min-h-11 rounded-md bg-base px-3 font-mono text-body text-text"
            />
          </label>
          <p className="text-body text-muted">The clone runs in a terminal you see, so you can answer a password or key prompt there.</p>
        </>
      )}
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
      {error !== null && (
        <p role="alert" className="text-body text-text">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <button type="submit" disabled={busy} className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60">
          {busy ? 'Adding…' : source === 'clone' ? 'Clone it' : 'Add it'}
        </button>
        <button type="button" onClick={onCancel} className="min-h-11 rounded-md px-4 text-body text-text">
          Cancel
        </button>
      </div>
    </form>
  );
}
