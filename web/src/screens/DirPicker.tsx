import { useCallback, useEffect, useState } from 'react';
import { browse, type Browsed, type Where } from '../api';

// DirPicker browses folders on a runner or machine so a project can be
// picked instead of typed. A tap selects a folder; Open goes into it.
// Repositories carry a git mark.
export default function DirPicker({
  where,
  selected,
  onSelect,
  onSessionExpired,
}: {
  where: Where;
  selected: string;
  onSelect: (path: string, git: boolean) => void;
  onSessionExpired: () => void;
}) {
  const [list, setList] = useState<Browsed | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hidden, setHidden] = useState(false);
  const [typed, setTyped] = useState('');
  const machine = where.machine;

  const go = useCallback(
    (path: string) => {
      void browse(machine ? { machine } : {}, path).then((res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        if (res === 'offline') {
          setError('That runner or machine is offline.');
          return;
        }
        if (res === 'unreachable') {
          setError('Something went wrong. Try again.');
          return;
        }
        if ('refused' in res) {
          setError(res.refused === 'directory not found' ? 'No such folder there.' : res.refused);
          return;
        }
        setError(null);
        setList(res);
      });
    },
    [machine, onSessionExpired],
  );

  // Start in the workspace whenever the target changes.
  useEffect(() => {
    go('');
  }, [go]);

  const join = (name: string) => (list === null ? name : list.path === '/' ? `/${name}` : `${list.path}/${name}`);
  const entries = (list?.entries ?? []).filter((e) => hidden || !e.hidden);

  return (
    <div className="flex flex-col gap-2 rounded-md bg-base p-2">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => list?.parent && go(list.parent)}
          disabled={!list?.parent}
          aria-label="Up one folder"
          className="min-h-11 rounded-md px-3 text-body text-text disabled:opacity-40"
        >
          ↑ Up
        </button>
        {list?.home && (
          <button type="button" onClick={() => go(list.home ?? '')} aria-label="Home folder" className="min-h-11 rounded-md px-3 text-body text-text">
            ~
          </button>
        )}
        <button type="button" onClick={() => go('')} aria-label="Workspace folder" className="min-h-11 rounded-md px-3 text-body text-text">
          Workspace
        </button>
        <span className="min-w-0 flex-1 truncate font-mono text-body text-muted" title={list?.path}>
          {list?.path ?? '…'}
        </span>
        <label className="flex min-h-11 items-center gap-1 text-body text-muted">
          <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />
          Hidden
        </label>
      </div>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (typed.trim() !== '') {
            go(typed.trim());
          }
        }}
      >
        <label className="sr-only" htmlFor="dirpicker-path">
          Go to a folder
        </label>
        <input
          id="dirpicker-path"
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          placeholder="Type a path and press Go"
          className="min-h-11 min-w-0 flex-1 rounded-md bg-surface px-3 font-mono text-body text-text"
        />
        <button type="submit" className="min-h-11 rounded-md px-3 text-body text-text">
          Go
        </button>
      </form>
      {error !== null && (
        <p role="alert" className="text-body text-text">
          {error}
        </p>
      )}
      {list !== null && (
        <button
          type="button"
          onClick={() => onSelect(list.path, list.git === true)}
          aria-pressed={selected === list.path}
          className={`min-h-11 rounded-md px-3 text-left text-body ${selected === list.path ? 'bg-text text-base' : 'bg-surface text-text'}`}
        >
          Use this folder{list.git ? ' (git repo)' : ''}
        </button>
      )}
      <ul aria-label="Folders" className="flex max-h-64 flex-col overflow-y-auto">
        {list !== null && entries.length === 0 && <li className="px-3 py-2 text-body text-muted">No folders here.</li>}
        {entries.map((e) => {
          const path = join(e.name);
          return (
            <li key={e.name} className="flex min-h-11 items-center gap-1">
              <button
                type="button"
                onClick={() => onSelect(path, e.git === true)}
                onDoubleClick={() => go(path)}
                aria-pressed={selected === path}
                aria-label={`Select ${e.name}${e.git ? ', a git repo' : ''}`}
                className={`flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-md px-3 text-left text-body ${
                  selected === path ? 'bg-text text-base' : 'text-text'
                }`}
              >
                <span aria-hidden="true">{e.git ? '⎇' : '▸'}</span>
                <span className="truncate">{e.name}</span>
                {e.git && <span className="rounded bg-surface px-1.5 text-body text-muted">git</span>}
              </button>
              <button
                type="button"
                onClick={() => go(path)}
                aria-label={`Open ${e.name}`}
                className="min-h-11 min-w-11 rounded-md px-2 text-body text-muted"
              >
                →
              </button>
            </li>
          );
        })}
      </ul>
      {list?.truncated && <p className="text-body text-muted">Showing the first 500 folders. Type a path to go further.</p>}
    </div>
  );
}
