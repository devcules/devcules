import { useEffect, useState } from 'react';
import {
  listAgents,
  listProviders,
  openProjectTerminal,
  openTerminal,
  providerModels,
  startAgent,
  type AgentInfo,
  type Project,
  type ProviderState,
  type Where,
} from '../api';
import { formatLabel, providerLabel } from './providerNames';

const otherModel = '__other__';

// NewTerminal opens a terminal: in a project, or on the runner when
// there is none. It asks what runs in it (a shell, or one of the
// installed agents) and, for an agent, which provider it calls and
// with which model.
export default function NewTerminal({
  project,
  onOpened,
  onCancel,
  onSessionExpired,
}: {
  project?: Project;
  onOpened: (id: string, where: Where) => void;
  onCancel: () => void;
  onSessionExpired: () => void;
}) {
  const onMachine = project?.kind === 'machine';
  const where: Where = onMachine && project ? { machine: project.target } : {};
  const [name, setName] = useState('');
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [providers, setProviders] = useState<ProviderState[]>([]);
  const [run, setRun] = useState('shell');
  const [provider, setProvider] = useState('');
  const [models, setModels] = useState<string[] | null>(null);
  const [modelNote, setModelNote] = useState<string | null>(null);
  const [model, setModel] = useState('');
  const [typedModel, setTypedModel] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Agents run on runners only: a machine terminal is always a shell.
  useEffect(() => {
    if (onMachine) {
      return;
    }
    void listAgents().then((res) => {
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (typeof res === 'object' && (!project || res.runner === project.target)) {
        setAgents(res.agents.filter((a) => a.installed));
      }
    });
    void listProviders().then((res) => {
      if (Array.isArray(res)) {
        setProviders(res);
      }
    });
  }, [onMachine, project, onSessionExpired]);

  const agent = agents.find((a) => a.id === run);
  const speaks = agent ? (agent.providers?.length ? agent.providers : [agent.provider]) : [];
  const served = (id: string) => providers.find((p) => p.id === id);
  const labelOf = (id: string) => served(id)?.name ?? providerLabel(id);

  // pickProvider changes the provider and forgets the old one's model.
  function pickProvider(p: string): void {
    setProvider(p);
    setModels(null);
    setModelNote(null);
    setModel('');
    setTypedModel('');
  }

  // Picking an agent picks its own provider, when that one has a key.
  function pickRun(id: string): void {
    setRun(id);
    setError(null);
    const a = agents.find((x) => x.id === id);
    if (!a) {
      pickProvider('');
      return;
    }
    const own = a.providers?.length ? a.providers : [a.provider];
    pickProvider(own.find((p) => served(p)?.ready) ?? own[0]);
  }

  // The model list follows the provider; serve reads it with the key.
  useEffect(() => {
    if (provider === '' || !served(provider)?.ready) {
      return;
    }
    let live = true;
    void providerModels(provider).then((res) => {
      if (!live) {
        return;
      }
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (Array.isArray(res)) {
        setModels(res);
        return;
      }
      setModels([]);
      setModelNote(typeof res === 'object' ? `${res.refused}. Type a model instead.` : 'The model list did not load. Type a model instead.');
    });
    return () => {
      live = false;
    };
    // served() reads providers, which this list depends on too.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [provider, providers, onSessionExpired]);

  const chosenModel = model === otherModel || (models !== null && models.length === 0) ? typedModel.trim() : model;
  const foreign = agent !== undefined && provider !== agent.provider;
  // A profile's default model stands in when none is picked.
  const fallback = served(provider)?.model ?? '';
  const needsModel = foreign && fallback === '';

  async function create(): Promise<void> {
    setError(null);
    if (agent && needsModel && chosenModel === '') {
      setError(`Pick a model: ${agent.name} on ${labelOf(provider)} has no default.`);
      return;
    }
    setBusy(true);
    const label = name.trim();
    const res = agent
      ? await startAgent(agent.id, { project: project?.id, provider, model: chosenModel, name: label })
      : project
        ? await openProjectTerminal(project.id, label)
        : await openTerminal(undefined, label);
    setBusy(false);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'object' && 'id' in res) {
      onOpened(res.id, where);
      return;
    }
    if (typeof res === 'object') {
      setError(res.refused);
      return;
    }
    setError(res === 'offline' ? 'That runner or machine is offline.' : res === 'busy' ? 'It did not answer. Try again.' : 'Something went wrong. Try again.');
  }

  return (
    <form
      aria-label="New terminal"
      className="flex flex-col gap-3 rounded-md bg-surface px-3 py-3"
      onSubmit={(e) => {
        e.preventDefault();
        void create();
      }}
    >
      <h3 className="text-body font-semibold text-text">{project ? `New terminal in ${project.name}` : 'New terminal'}</h3>
      <label className="flex flex-col gap-1 text-body text-text">
        Name (optional)
        <input
          type="text"
          maxLength={40}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Like api server"
          className="min-h-11 rounded-md bg-base px-3 text-body text-text"
        />
      </label>
      <label className="flex flex-col gap-1 text-body text-text">
        Runs
        <select value={run} onChange={(e) => pickRun(e.target.value)} className="min-h-11 rounded-md bg-base px-3 text-body text-text">
          <option value="shell">Shell</option>
          {agents.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </label>
      {onMachine && <p className="text-body text-muted">Agents run on runners; on a machine a terminal is a shell.</p>}
      {agent && (
        <fieldset className="flex flex-col gap-1">
          <legend className="text-body text-text">Provider</legend>
          <div className="flex flex-wrap gap-2">
            {speaks.map((p) => {
              const state = served(p);
              const ok = state?.ready === true;
              return (
                <button
                  key={p}
                  type="button"
                  disabled={!ok}
                  aria-pressed={provider === p}
                  onClick={() => pickProvider(p)}
                  className={`flex min-h-11 flex-col items-start justify-center rounded-md px-4 py-1 text-left text-body disabled:opacity-50 ${provider === p ? 'bg-text text-base' : 'bg-base text-text'}`}
                >
                  <span>
                    {labelOf(p)}
                    {p === agent.provider ? ' (its own)' : ''}
                    {state?.default ? ' · default' : ''}
                    {!state ? ' · not served' : !ok ? ' · no key' : ''}
                  </span>
                  {state?.format && <span className="text-body opacity-75">{formatLabel(state.format)}</span>}
                </button>
              );
            })}
          </div>
          {speaks.some((p) => served(p) && !served(p)?.ready) && (
            <p className="text-body text-muted">A provider with no key is off: add its key in Settings.</p>
          )}
        </fieldset>
      )}
      {agent && provider !== '' && served(provider)?.ready && (
        <label className="flex flex-col gap-1 text-body text-text">
          Model{needsModel ? '' : ' (optional)'}
          {models === null ? (
            <span className="text-body text-muted" aria-busy="true">
              Loading {labelOf(provider)} models…
            </span>
          ) : models.length > 0 ? (
            <select value={model} onChange={(e) => setModel(e.target.value)} className="min-h-11 rounded-md bg-base px-3 font-mono text-body text-text">
              <option value="">{needsModel ? 'Pick a model' : fallback !== '' ? `${fallback} (the provider's default)` : `${agent.name}'s default`}</option>
              {models.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
              <option value={otherModel}>Another model…</option>
            </select>
          ) : null}
          {(model === otherModel || (models !== null && models.length === 0)) && (
            <input
              type="text"
              aria-label="Model id"
              value={typedModel}
              onChange={(e) => setTypedModel(e.target.value)}
              placeholder="Model id"
              className="min-h-11 rounded-md bg-base px-3 font-mono text-body text-text"
            />
          )}
          {modelNote !== null && <span className="text-body text-muted">{modelNote}</span>}
        </label>
      )}
      {error !== null && (
        <p role="alert" className="text-body text-text">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <button
          type="submit"
          disabled={busy || (agent !== undefined && !served(provider)?.ready)}
          className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
        >
          {busy ? 'Opening…' : agent ? `Start ${agent.name}` : 'Open shell'}
        </button>
        <button type="button" onClick={onCancel} className="min-h-11 rounded-md px-4 text-body text-text">
          Cancel
        </button>
      </div>
    </form>
  );
}
