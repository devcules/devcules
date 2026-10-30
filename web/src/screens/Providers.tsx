import { useCallback, useEffect, useState } from 'react';
import { listProviders, removeProviderKey, setProviderKey, type ProviderState } from '../api';
import { providerLabel } from './NewTerminal';

// Providers sets the model vendors' API keys. A key goes to serve's
// vault and is never shown again, not even in part; the router uses it
// from the next call, with no restart.
export default function Providers({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [list, setList] = useState<ProviderState[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [key, setKey] = useState('');
  const [confirm, setConfirm] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(() => {
    void listProviders().then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      setList(Array.isArray(res) ? res : []);
    });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function save(id: string): Promise<void> {
    setBusy(true);
    setNotice(null);
    const res = await setProviderKey(id, key.trim());
    setBusy(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res !== true) {
      setNotice(res === 'unreachable' ? 'Something went wrong. Try again.' : res.refused);
      return;
    }
    setKey('');
    setEditing(null);
    setNotice(`${providerLabel(id)} key saved.`);
    load();
  }

  async function remove(id: string): Promise<void> {
    setConfirm(null);
    const res = await removeProviderKey(id);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    setNotice(res === true ? `${providerLabel(id)} key removed.` : 'Something went wrong. Try again.');
    load();
  }

  return (
    <section aria-label="Providers" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Providers</h3>
      <p className="text-body text-muted">
        Agents reach their models through Devcules with these keys. A key is kept encrypted on this server and never shown
        again.
      </p>
      {list === null ? (
        <div aria-busy="true" className="h-11" />
      ) : list.length === 0 ? (
        <p className="text-body text-muted">The model router is not running.</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.map((p) => (
            <li key={p.id} className="flex flex-col gap-2 rounded-md bg-surface px-3 py-2">
              <div className="flex min-h-11 flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1">
                  <span className="block text-body text-text">{providerLabel(p.id)}</span>
                  <span className="block text-body text-muted">{p.ready ? 'Key set' : 'No key'}</span>
                </span>
                {editing !== p.id && (
                  <button
                    type="button"
                    onClick={() => {
                      setEditing(p.id);
                      setKey('');
                      setNotice(null);
                    }}
                    className="min-h-11 rounded-md bg-base px-3 text-body text-text"
                  >
                    {p.ready ? 'Replace key' : 'Add key'}
                  </button>
                )}
                {p.ready &&
                  (confirm === p.id ? (
                    <>
                      <button
                        type="button"
                        onClick={() => void remove(p.id)}
                        aria-label={`Confirm remove the ${providerLabel(p.id)} key`}
                        className="min-h-11 rounded-md bg-accent px-3 text-body text-base"
                      >
                        Remove it
                      </button>
                      <button type="button" onClick={() => setConfirm(null)} className="min-h-11 rounded-md px-3 text-body text-text">
                        Keep
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      onClick={() => setConfirm(p.id)}
                      aria-label={`Remove the ${providerLabel(p.id)} key`}
                      className="min-h-11 rounded-md px-3 text-body text-muted"
                    >
                      Remove
                    </button>
                  ))}
              </div>
              {confirm === p.id && <p className="text-body text-muted">Agents on {providerLabel(p.id)} stop working until you add a key again.</p>}
              {editing === p.id && (
                <form
                  className="flex flex-col gap-2 sm:flex-row"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void save(p.id);
                  }}
                >
                  <label className="sr-only" htmlFor={`key-${p.id}`}>
                    {providerLabel(p.id)} API key
                  </label>
                  <input
                    id={`key-${p.id}`}
                    type="password"
                    autoComplete="off"
                    spellCheck={false}
                    value={key}
                    onChange={(e) => setKey(e.target.value)}
                    placeholder={`Paste the ${providerLabel(p.id)} API key`}
                    className="min-h-11 min-w-0 flex-1 rounded-md bg-base px-3 font-mono text-body text-text"
                  />
                  <button
                    type="submit"
                    disabled={busy || key.trim() === ''}
                    className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
                  >
                    {busy ? 'Saving…' : 'Save key'}
                  </button>
                  <button type="button" onClick={() => setEditing(null)} className="min-h-11 rounded-md px-4 text-body text-text">
                    Cancel
                  </button>
                </form>
              )}
            </li>
          ))}
        </ul>
      )}
      {notice !== null && (
        <p role="status" className="text-body text-text">
          {notice}
        </p>
      )}
    </section>
  );
}
