import { useCallback, useEffect, useState, type ReactNode } from 'react';
import {
  addProvider,
  editProvider,
  listProviders,
  makeDefaultProvider,
  probeModels,
  removeProvider,
  type ProviderDraft,
  type ProviderFormat,
  type ProviderState,
} from '../api';
import { formatLabel, providerLabel } from './providerNames';

const blank: ProviderDraft = { name: '', format: 'openai', alsoAnthropic: false, baseUrl: '', api: 'auto', model: '', default: false, key: '' };

// presets fill the form for the common endpoints; every field stays
// editable.
const presets: Array<{ label: string; draft: Partial<ProviderDraft> }> = [
  { label: 'Meta', draft: { name: 'Meta', format: 'openai', alsoAnthropic: true, baseUrl: 'https://api.meta.ai/v1', api: 'auto' } },
  { label: 'Anthropic', draft: { name: 'Anthropic', format: 'anthropic', baseUrl: 'https://api.anthropic.com' } },
  { label: 'OpenAI', draft: { name: 'OpenAI', format: 'openai', baseUrl: 'https://api.openai.com/v1', api: 'auto' } },
  { label: 'OpenAI-compatible (vLLM, Nebius…)', draft: { name: '', format: 'openai', baseUrl: '', api: 'chat' } },
];

const field = 'min-h-11 rounded-md bg-base px-3 text-body text-text';

function Chip({ children }: { children: ReactNode }) {
  return <span className="inline-flex min-h-6 items-center rounded-md bg-base px-2 text-body text-muted">{children}</span>;
}

// Providers keeps the model provider profiles: a name, the API format,
// a base URL and an optional default model each. A key goes to serve's
// vault and is never shown again, not even in part; the router uses a
// change from the next call, with no restart.
export default function Providers({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [list, setList] = useState<ProviderState[] | null>(null);
  const [editing, setEditing] = useState<ProviderState | null>(null);
  const [draft, setDraft] = useState<ProviderDraft>(blank);
  const [models, setModels] = useState<string[] | null>(null);
  const [modelNote, setModelNote] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
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

  const name = (p: ProviderState) => p.name ?? providerLabel(p.id);

  function reset(next: ProviderDraft, who: ProviderState | null): void {
    setDraft(next);
    setEditing(who);
    setModels(null);
    setModelNote(null);
    setError(null);
  }

  function set<K extends keyof ProviderDraft>(k: K, v: ProviderDraft[K]): void {
    setDraft((d) => ({ ...d, [k]: v }));
    // A new address or format may list other models.
    if (k === 'baseUrl' || k === 'format' || k === 'key') {
      setModels(null);
      setModelNote(null);
    }
  }

  function edit(p: ProviderState): void {
    setNotice(null);
    reset(
      {
        name: name(p),
        format: p.format ?? 'openai',
        alsoAnthropic: p.alsoAnthropic === true,
        baseUrl: p.baseUrl ?? '',
        api: p.api ?? 'auto',
        model: p.model ?? '',
        default: p.default === true,
        key: '',
      },
      p,
    );
  }

  // answer handles a write's reply: the session, a refusal, or done.
  function answer(res: Awaited<ReturnType<typeof addProvider>>): boolean {
    if (res === 'unauthorized') {
      onSessionExpired();
      return false;
    }
    if (res === 'unreachable') {
      setError('Something went wrong. Try again.');
      return false;
    }
    if (typeof res === 'object' && 'refused' in res) {
      setError(res.refused);
      return false;
    }
    return true;
  }

  async function save(): Promise<void> {
    setBusy(true);
    setError(null);
    setNotice(null);
    const body = { ...draft, key: draft.key.trim(), api: draft.format === 'anthropic' ? ('auto' as const) : draft.api };
    const res = editing ? await editProvider(editing.id, body) : await addProvider(body);
    setBusy(false);
    if (!answer(res)) {
      return;
    }
    setNotice(editing ? `${draft.name} saved.` : `${draft.name} added.`);
    reset(blank, null);
    load();
  }

  async function loadModels(): Promise<void> {
    setModelNote(null);
    setBusy(true);
    const res = await probeModels({ format: draft.format, baseUrl: draft.baseUrl, key: draft.key.trim() || undefined, id: editing?.id });
    setBusy(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (Array.isArray(res)) {
      setModels(res);
      setModelNote(res.length === 0 ? 'It lists no models. Type one.' : `${res.length} model${res.length === 1 ? '' : 's'}.`);
      return;
    }
    setModels(null);
    setModelNote(res === 'unreachable' ? 'The models did not load. Try again.' : res.refused);
  }

  async function makeDefault(p: ProviderState): Promise<void> {
    setNotice(null);
    setError(null);
    if (answer(await makeDefaultProvider(p.id))) {
      setNotice(`${name(p)} is the default.`);
      load();
    }
  }

  async function remove(p: ProviderState): Promise<void> {
    setConfirm(null);
    setNotice(null);
    setError(null);
    if (answer(await removeProvider(p.id))) {
      if (editing?.id === p.id) {
        reset(blank, null);
      }
      setNotice(`${name(p)} removed.`);
      load();
    }
  }

  const openai = draft.format === 'openai';
  const keyKept = editing?.ready === true;

  return (
    <section aria-label="Providers" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Providers</h3>
      <p className="text-body text-muted">
        Agents reach their models through Devcules, on one of these providers. A key is kept encrypted on this server and
        never shown again.
      </p>
      {list === null ? (
        <div aria-busy="true" className="h-11" />
      ) : list.length === 0 ? (
        <p className="text-body text-muted">No providers yet: add one below.</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {list.map((p) => (
            <li key={p.id} aria-label={name(p)} className="flex flex-col gap-2 rounded-md bg-surface px-3 py-2">
              <div className="flex min-h-11 flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="text-body font-semibold text-text">{name(p)}</span>
                    {p.format && <Chip>{formatLabel(p.format)}</Chip>}
                    {p.alsoAnthropic && <Chip>+ Anthropic</Chip>}
                    <Chip>{p.ready ? 'Key set' : 'No key'}</Chip>
                    {p.default && <Chip>Default</Chip>}
                  </span>
                  {p.baseUrl && (
                    <code className="mt-1 block break-all font-mono text-body text-muted">
                      {p.baseUrl}
                      {p.model ? ` · ${p.model}` : ''}
                    </code>
                  )}
                </span>
                {!p.default && p.format && (
                  <button type="button" onClick={() => void makeDefault(p)} className="min-h-11 rounded-md bg-base px-3 text-body text-text">
                    Make default
                  </button>
                )}
                {p.format && (
                  <button type="button" onClick={() => edit(p)} aria-label={`Edit ${name(p)}`} className="min-h-11 rounded-md bg-base px-3 text-body text-text">
                    Edit
                  </button>
                )}
                {p.format &&
                  (confirm === p.id ? (
                    <>
                      <button
                        type="button"
                        onClick={() => void remove(p)}
                        aria-label={`Confirm remove ${name(p)}`}
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
                      aria-label={`Remove ${name(p)}`}
                      className="min-h-11 min-w-11 rounded-md px-3 text-body text-muted"
                    >
                      ✕
                    </button>
                  ))}
              </div>
              {confirm === p.id && <p className="text-body text-muted">Its key goes too. Agents started on it stop working.</p>}
            </li>
          ))}
        </ul>
      )}
      {notice !== null && (
        <p role="status" className="text-body text-text">
          {notice}
        </p>
      )}

      <form
        aria-label={editing ? `Edit ${name(editing)}` : 'Add a provider'}
        className="flex flex-col gap-3 rounded-md bg-surface px-3 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <h4 className="text-body font-semibold text-text">{editing ? `Edit ${name(editing)}` : 'Add a provider'}</h4>
        {!editing && (
          <div className="flex flex-wrap gap-2">
            {presets.map((pr) => (
              <button
                key={pr.label}
                type="button"
                onClick={() => reset({ ...blank, ...pr.draft }, null)}
                className="min-h-11 rounded-md bg-base px-3 text-body text-text"
              >
                {pr.label}
              </button>
            ))}
          </div>
        )}
        <label className="flex flex-col gap-1 text-body text-text">
          Name
          <input type="text" required maxLength={64} value={draft.name} onChange={(e) => set('name', e.target.value)} placeholder="Like Nebius, OpenAI or local vLLM" className={field} />
        </label>
        <label className="flex flex-col gap-1 text-body text-text">
          Format
          <select value={draft.format} onChange={(e) => set('format', e.target.value as ProviderFormat)} className={field}>
            <option value="openai">OpenAI-compatible</option>
            <option value="anthropic">Anthropic</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-body text-text">
          Base URL
          <input
            type="url"
            required
            value={draft.baseUrl}
            onChange={(e) => set('baseUrl', e.target.value)}
            placeholder={openai ? 'https://api.studio.nebius.com/v1' : 'https://api.anthropic.com'}
            className={`${field} font-mono`}
          />
          <span className="text-body text-muted">https, or http for localhost and your own network (a local vLLM).</span>
        </label>
        <label className="flex flex-col gap-1 text-body text-text">
          Authentication
          <select value="api_key" disabled className={field}>
            <option value="api_key">API key ({openai ? 'Bearer' : 'x-api-key'})</option>
          </select>
        </label>
        {openai && (
          <>
            <label className="flex flex-col gap-1 text-body text-text">
              API for agents
              <select value={draft.api} onChange={(e) => set('api', e.target.value as ProviderDraft['api'])} className={field}>
                <option value="auto">Auto</option>
                <option value="responses">Responses (/v1/responses)</option>
                <option value="chat">Chat Completions only</option>
              </select>
              <span className="text-body text-muted">Codex needs /v1/responses: pick Chat Completions only if the endpoint has just that.</span>
            </label>
            <label className="flex min-h-11 items-center gap-2 text-body text-text">
              <input type="checkbox" checked={draft.alsoAnthropic} onChange={(e) => set('alsoAnthropic', e.target.checked)} className="h-5 w-5" />
              Also serves Anthropic&apos;s /v1/messages (Claude Code can use it)
            </label>
          </>
        )}
        <label className="flex flex-col gap-1 text-body text-text">
          API key
          <input
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={draft.key}
            onChange={(e) => set('key', e.target.value)}
            placeholder={keyKept ? 'Leave blank to keep the saved key' : 'Paste the API key'}
            className={`${field} font-mono`}
          />
        </label>
        <div className="flex flex-col gap-1 text-body text-text">
          <label htmlFor="provider-model">Default model (optional)</label>
          <div className="flex flex-wrap gap-2">
            <input
              id="provider-model"
              type="text"
              value={draft.model}
              onChange={(e) => set('model', e.target.value)}
              placeholder="Type it or load the list"
              className={`${field} min-w-0 flex-1 font-mono`}
            />
            <button
              type="button"
              disabled={busy || draft.baseUrl.trim() === ''}
              onClick={() => void loadModels()}
              className="min-h-11 rounded-md bg-base px-3 text-body text-text disabled:opacity-60"
            >
              Load models
            </button>
          </div>
          {models !== null && models.length > 0 && (
            <select aria-label="Loaded models" value={models.includes(draft.model) ? draft.model : ''} onChange={(e) => set('model', e.target.value)} className={`${field} font-mono`}>
              <option value="">Pick a model</option>
              {models.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          )}
          {modelNote !== null && <span className="text-body text-muted">{modelNote}</span>}
        </div>
        {!editing?.default && (
          <label className="flex min-h-11 items-center gap-2 text-body text-text">
            <input type="checkbox" checked={draft.default} onChange={(e) => set('default', e.target.checked)} className="h-5 w-5" />
            Use as the default
          </label>
        )}
        {error !== null && (
          <p role="alert" className="text-body text-text">
            {error}
          </p>
        )}
        <div className="flex gap-2">
          <button type="submit" disabled={busy} className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60">
            {busy ? 'Saving…' : editing ? 'Save' : 'Add provider'}
          </button>
          {(editing || draft !== blank) && (
            <button type="button" onClick={() => reset(blank, null)} className="min-h-11 rounded-md px-4 text-body text-text">
              Cancel
            </button>
          )}
        </div>
      </form>
    </section>
  );
}
