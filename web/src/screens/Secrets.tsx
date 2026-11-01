// Secrets — names only. Store a key under a name (the Desk sources and
// providers read from these), or remove one. Values are write-only: they
// never come back over HTTP, so this screen lists the names you set and
// never what you set them to.

import { useCallback, useEffect, useState } from 'react';
import { deleteSecret, listSecrets, putSecret } from '../lib/deskApi';
import { toast } from '../lib/toast';

export default function Secrets({ onSessionExpired, onSummary }: { onSessionExpired: () => void; onSummary?: (line: string) => void }) {
  const [names, setNames] = useState<string[] | null>(null);
  const [name, setName] = useState('');
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const load = useCallback(() => {
    void listSecrets().then((r) => {
      if (r === 'unauthorized') { onSessionExpired(); return; }
      if (r === 'unreachable') { setErr('server unreachable'); setNames([]); return; }
      const list = Array.isArray(r?.names) ? r.names : [];
      setNames(list);
      onSummary?.(list.length === 0 ? 'None' : `${list.length} stored`);
    });
  }, [onSessionExpired, onSummary]);
  useEffect(load, [load]);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n || !value) return;
    setBusy(true);
    const r = await putSecret(n, value);
    setBusy(false);
    if (r === 'unauthorized') { onSessionExpired(); return; }
    if (r !== true) { setErr('save failed — 128-char names, value under 4 KB'); return; }
    setName('');
    setValue('');
    toast(`Stored ${n}`);
    load();
  };

  const remove = async (n: string) => {
    const r = await deleteSecret(n);
    if (r === 'unauthorized') { onSessionExpired(); return; }
    if (r !== true) { setErr(`could not remove ${n}`); return; }
    toast(`Removed ${n}`);
    load();
  };

  return (
    <section aria-label="Secrets" className="flex flex-col gap-3 rounded-md bg-surface px-3 py-3">
      <h3 className="text-body font-semibold text-text">Secrets</h3>
      <p className="text-body text-muted">
        API keys the server uses (Desk sources, providers). Names show; values are write-only and never read back — a stored key can be replaced or removed, not seen.
      </p>

      {names === null ? (
        <div className="text-body text-muted">loading…</div>
      ) : names.length === 0 ? (
        <div className="text-body text-muted">No secrets yet. The Desk's Tavily and ticket tokens live here once you add them.</div>
      ) : (
        <ul className="flex flex-col gap-1">
          {names.map((n) => (
            <li key={n} className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded bg-base px-2 py-1 font-mono text-body text-text">{n}</code>
              <button type="button" className="min-h-9 rounded px-3 text-body text-red-400 hover:bg-base" onClick={() => void remove(n)} aria-label={`remove ${n}`}>
                remove
              </button>
            </li>
          ))}
        </ul>
      )}

      <form onSubmit={(e) => void add(e)} className="flex flex-col gap-2">
        <div className="flex gap-2">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="name (e.g. TAVILY_KEY)"
            aria-label="secret name"
            className="min-h-11 w-48 rounded bg-base px-3 font-mono text-body text-text"
          />
          <input
            type="password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="value"
            aria-label="secret value"
            autoComplete="new-password"
            className="min-h-11 min-w-0 flex-1 rounded bg-base px-3 font-mono text-body text-text"
          />
        </div>
        <div className="flex gap-2">
          <button type="submit" disabled={busy || !name.trim() || !value} className="min-h-11 rounded bg-accent px-4 text-body text-on-accent disabled:opacity-60">
            {busy ? 'storing…' : 'store secret'}
          </button>
          {value && (
            <button type="button" className="min-h-11 rounded px-3 text-body text-muted" onClick={() => { setName(''); setValue(''); }}>
              clear
            </button>
          )}
        </div>
      </form>
      {err && <div role="alert" className="text-body text-red-400">{err}</div>}
    </section>
  );
}
