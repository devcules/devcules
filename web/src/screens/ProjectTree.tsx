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
import Renameable from './Renameable';
import { useAlive } from './pulse';
import { renameTerminal } from '../api';

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

  // renameTerm mirrors close: a machine terminal rides its link, the
  // runner's ride no query. True tells the row to keep the new label.
  async function renameTerm(p: Project, t: TerminalSummary, next: string): Promise<boolean> {
    const res = await renameTerminal(t.term, next, projectWhere(p).machine ? projectWhere(p) : undefined);
    if (res === 'unauthorized') {
      onSessionExpired();
      return false;
    }
    if (res !== true) {
      return false;
    }
    refresh();
    return true;
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
                data-selected={isOpen ? 'true' : undefined}
                className={`i-row flex ${row} min-w-0 flex-1 items-center gap-1.5 px-2 text-left text-body text-text`}
              >
                <span aria-hidden="true" className="w-3 text-muted">
                  {isOpen ? '▾' : '▸'}
                </span>
                <span aria-hidden="true" className={up ? 'text-text' : 'text-muted'}>
                  {p.git ? <RemoteIcon remote={p.remote} size={13} /> : '▢'}
                </span>
                {/* Long project names shrink a step so the fold arrow and
                    count keep their seats on a narrow sidebar. */}
                <span className="min-w-0 flex-1 truncate text-tab md:text-body" title={p.name}>{p.name}</span>
                <span className="shrink-0 text-body text-muted">{list.length}</span>
              </button>
              <button
                type="button"
                onClick={() => onNewTerminal(p)}
                disabled={!up}
                aria-label={`New terminal in ${p.name}`}
                title={up ? `New terminal in ${p.name}` : `${p.target} is offline`}
                className={`i-control ${row} min-w-9 px-2 text-body text-text disabled:opacity-40`}
              >
                +
              </button>
              <button
                type="button"
                onClick={() => setConfirm(confirm === p.id ? null : p.id)}
                aria-label={`Forget ${p.name}`}
                className={`i-control ${row} min-w-9 px-2 text-body text-muted`}
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
                  <TermRow
                    key={t.term}
                    t={t}
                    p={p}
                    row={row}
                    onOpen={() => onOpen(t.term, projectWhere(p))}
                    onClose={() => void close(p, t)}
                    onRename={(next) => renameTerm(p, t, next)}
                  />
                ))}
              </ul>
            )}
          </li>
        );
      })}
      </ul>
      {/* The icon legend lives in the New Project picker (DirPicker):
          the marks explain themselves where folders are chosen, and
          the tree stays clean after that. */}
    </>
  );
}

// termLabel names a terminal: its name, else its agent, else its id.
export function termLabel(t: TerminalSummary): string {
  return t.name ?? (t.agent ? `${t.agent} · ${t.term.slice(0, 6)}` : t.term.slice(0, 8));
}

// TermRow is one terminal under a project: an open button with the
// live-dot, the right-click-to-rename label, and the close control.
// The label shrinks a step like the project's so a long name folds
// instead of pushing the row (or the close button) off the sidebar.
function TermRow({
  t,
  p,
  row,
  onOpen,
  onClose,
  onRename,
}: {
  t: TerminalSummary;
  p: Project;
  row: string;
  onOpen: () => void;
  onClose: () => void;
  onRename: (next: string) => Promise<boolean>;
}) {
  const label = termLabel(t);
  // The stream lives on the project tab, not here: the dot rests until
  // this row's pane is the open one (and therefore streaming).
  const alive = useAlive(t.term, false);
  return (
    <li className={`flex ${row} items-center gap-1 pl-6`}>
      <button
        type="button"
        onClick={onOpen}
        aria-label={`Open terminal ${label} in ${p.name}`}
        className={`i-row flex ${row} min-w-0 flex-1 items-center gap-1.5 px-2 text-left text-body text-text`}
      >
        <span aria-hidden="true" className="i-live shrink-0" data-on={alive ? 'true' : 'false'} />
        <Renameable value={label} onCommit={onRename} className="flex-1 text-tab md:text-body" inputClassName="text-tab md:text-body" />
      </button>
      <button
        type="button"
        onClick={onClose}
        aria-label={`Close terminal ${label}`}
        className={`${row} min-w-9 shrink-0 rounded-md px-2 text-body text-muted`}
      >
        ✕
      </button>
    </li>
  );
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
