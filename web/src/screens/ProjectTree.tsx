import { useCallback, useEffect, useState } from 'react';
import {
  closeTerminal,
  listProjects,
  listTerminals,
  projectWhere,
  removeProject,
  type Project,
  type TerminalSummary,
  type Where,
} from '../api';
import RemoteIcon from './RemoteIcon';

// ProjectTree lists the projects, each folding open to its own
// terminals: the sidebar shows it on desktop and the Projects tab on
// phones. A project's + opens a terminal in it; ✕ forgets it.
export default function ProjectTree({
  compact = false,
  reload = 0,
  onOpen,
  onNewTerminal,
  onSessionExpired,
}: {
  compact?: boolean;
  // reload changes when the caller knows the list changed.
  reload?: number;
  onOpen: (id: string, where: Where) => void;
  onNewTerminal: (p: Project) => void;
  onSessionExpired: () => void;
}) {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [online, setOnline] = useState<Set<string>>(new Set());
  const [terms, setTerms] = useState<Record<string, TerminalSummary[]>>({});
  const [folded, setFolded] = useState<Set<string>>(readFolded);
  const [confirm, setConfirm] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  // refresh loads the projects and, once per online runner or
  // machine, its terminals, then files each under its project.
  const refresh = useCallback(() => {
    void listProjects().then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (res === 'unreachable') {
        setFailed(true);
        return;
      }
      setFailed(false);
      setProjects(res.projects);
      const up = new Set([...res.runners.map((r) => `runner/${r}`), ...res.machines.map((m) => `machine/${m}`)]);
      setOnline(up);
      const targets = new Map<string, Where>();
      for (const p of res.projects) {
        const key = `${p.kind}/${p.target}`;
        if (up.has(key)) {
          targets.set(key, projectWhere(p));
        }
      }
      const next: Record<string, TerminalSummary[]> = {};
      void Promise.all(
        [...targets.values()].map((where) =>
          listTerminals(where.machine ? where : undefined).then((t) => {
            if (typeof t === 'object') {
              for (const x of t.terminals) {
                if (x.project) {
                  (next[x.project] ??= []).push(x);
                }
              }
            }
          }),
        ),
      ).then(() => setTerms(next));
    });
  }, [onSessionExpired]);

  // Reload on demand, and every 5 s while visible, so a terminal that
  // ended elsewhere drops off.
  useEffect(() => {
    refresh();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        refresh();
      }
    }, 5000);
    return () => window.clearInterval(timer);
  }, [refresh, reload]);

  function toggle(id: string): void {
    setFolded((f) => {
      const next = new Set(f);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      writeFolded(next);
      return next;
    });
  }

  async function forget(p: Project): Promise<void> {
    setConfirm(null);
    if ((await removeProject(p.id)) === 'unauthorized') {
      onSessionExpired();
      return;
    }
    refresh();
  }

  async function close(p: Project, t: TerminalSummary): Promise<void> {
    if ((await closeTerminal(t.term, projectWhere(p).machine ? projectWhere(p) : undefined)) === 'unauthorized') {
      onSessionExpired();
      return;
    }
    refresh();
  }

  if (projects === null) {
    return failed ? <p className="px-3 text-body text-muted">Projects did not load.</p> : null;
  }
  if (projects.length === 0) {
    return compact ? <p className="px-3 text-body text-muted">No projects yet.</p> : null;
  }

  const row = compact ? 'min-h-9' : 'min-h-11';
  // The legend lives below the tree: one muted line naming each mark,
  // so a folder icon never asks for a help page.
  return (
    <>
      <ul aria-label="Projects" className="flex flex-col gap-1">
      {projects.map((p) => {
        const isOpen = !folded.has(p.id);
        const list = terms[p.id] ?? [];
        const up = online.has(`${p.kind}/${p.target}`);
        return (
          <li key={p.id} className={compact ? '' : 'rounded-md bg-surface px-2 py-1'}>
            <div className={`flex ${row} items-center gap-1`}>
              <button
                type="button"
                onClick={() => toggle(p.id)}
                aria-expanded={isOpen}
                aria-label={`${isOpen ? 'Fold' : 'Unfold'} ${p.name}`}
                className={`flex ${row} min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-body text-text`}
              >
                <span aria-hidden="true" className="w-3 text-muted">
                  {isOpen ? '▾' : '▸'}
                </span>
                <span aria-hidden="true" className={up ? 'text-text' : 'text-muted'}>
                  {p.git ? <RemoteIcon remote={p.remote} size={13} /> : '▢'}
                </span>
                <span className="min-w-0 flex-1 truncate">{p.name}</span>
                <span className="text-body text-muted">{list.length}</span>
              </button>
              <button
                type="button"
                onClick={() => onNewTerminal(p)}
                disabled={!up}
                aria-label={`New terminal in ${p.name}`}
                title={up ? `New terminal in ${p.name}` : `${p.target} is offline`}
                className={`${row} min-w-9 rounded-md px-2 text-body text-text disabled:opacity-40`}
              >
                +
              </button>
              <button
                type="button"
                onClick={() => setConfirm(confirm === p.id ? null : p.id)}
                aria-label={`Forget ${p.name}`}
                className={`${row} min-w-9 rounded-md px-2 text-body text-muted`}
              >
                ✕
              </button>
            </div>
            {confirm === p.id && (
              <div className="flex flex-wrap items-center gap-2 px-2 pb-1">
                <span className="text-body text-muted">Forget {p.name}? Files and terminals stay.</span>
                <button type="button" onClick={() => void forget(p)} className={`${row} rounded-md bg-accent px-3 text-body text-base`}>
                  Forget
                </button>
                <button type="button" onClick={() => setConfirm(null)} className={`${row} rounded-md px-3 text-body text-text`}>
                  Keep
                </button>
              </div>
            )}
            {isOpen && (
              <ul aria-label={`Terminals in ${p.name}`} className="flex flex-col">
                {!compact && (
                  <li className="px-7 text-body text-muted">
                    {p.kind === 'machine' ? 'machine' : 'runner'} {p.target} · <span className="font-mono">{p.dir}</span>
                    {up ? '' : ' · offline'}
                  </li>
                )}
                {list.map((t) => (
                  <li key={t.term} className={`flex ${row} items-center gap-1 pl-6`}>
                    <button
                      type="button"
                      onClick={() => onOpen(t.term, projectWhere(p))}
                      aria-label={`Open terminal ${termLabel(t)} in ${p.name}`}
                      className={`flex ${row} min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left text-body text-text`}
                    >
                      <span aria-hidden="true" className="text-muted">
                        ⌨
                      </span>
                      <span className="truncate">{termLabel(t)}</span>
                    </button>
                    <button
                      type="button"
                      onClick={() => void close(p, t)}
                      aria-label={`Close terminal ${termLabel(t)}`}
                      className={`${row} min-w-9 rounded-md px-2 text-body text-muted`}
                    >
                      ✕
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </li>
        );
      })}
      </ul>
      <p className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 px-3 text-body text-muted">
        <span className="flex items-center gap-1">▢ folder</span>
        <span className="flex items-center gap-1"><RemoteIcon remote="" size={11} /> git repo</span>
        <span className="flex items-center gap-1"><RemoteIcon remote="github" size={11} /> GitHub</span>
        <span className="flex items-center gap-1"><RemoteIcon remote="gitlab" size={11} /> GitLab</span>
        <span className="flex items-center gap-1"><RemoteIcon remote="azure" size={11} /> Azure DevOps</span>
      </p>
    </>
  );
}

// termLabel names a terminal: its name, else its agent, else its id.
export function termLabel(t: TerminalSummary): string {
  return t.name ?? (t.agent ? `${t.agent} · ${t.term.slice(0, 6)}` : t.term.slice(0, 8));
}

const foldKey = 'devcules.projects.folded';

// The fold state is a per-browser convenience: storage may be off
// (private windows), so every read and write tolerates failure.
function readFolded(): Set<string> {
  try {
    const raw = window.localStorage.getItem(foldKey);
    const list: unknown = raw ? JSON.parse(raw) : [];
    return new Set(Array.isArray(list) ? list.filter((x): x is string => typeof x === 'string') : []);
  } catch {
    return new Set();
  }
}

function writeFolded(s: Set<string>): void {
  try {
    window.localStorage.setItem(foldKey, JSON.stringify([...s]));
  } catch {
    // Folding still works for this page view.
  }
}
