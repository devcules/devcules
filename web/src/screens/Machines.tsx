import { useCallback, useEffect, useRef, useState } from 'react';
import {
  listMachines,
  machineBudget,
  machineGround,
  machineInput,
  machineScreenURL,
  machineVerify,
  type GroundMatch,
  type MachineError,
} from '../api';
import { Empty } from './Empty';

type State =
  | { view: 'loading' }
  | { view: 'list'; machines: string[]; budget: number; notice: string | null; refused: string | null };

// errorWords turns API outcomes into plain words. 'unauthorized'
// never lands here: callers route it to sign-in first.
function errorWords(err: MachineError): string {
  if (err === 'offline') {
    return 'No machine is linked. Enrol one and it appears here.';
  }
  return 'Something went wrong. Try again.';
}

// toScreen maps a click inside the rendered image to the machine's
// screen pixels, proportionally: the driver's boxes live in the
// capture's own space.
function toScreen(
  e: React.MouseEvent<HTMLDivElement>,
  box: HTMLDivElement,
  screenW: number,
  screenH: number,
): { x: number; y: number } {
  const r = box.getBoundingClientRect();
  const px = Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1);
  const py = Math.min(Math.max((e.clientY - r.top) / r.height, 0), 1);
  return { x: Math.round(px * screenW), y: Math.round(py * screenH) };
}

export default function Machines({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [state, setState] = useState<State>({ view: 'loading' });
  const [control, setControl] = useState(false);
  const [screenAt, setScreenAt] = useState(0);
  const [screenSize, setScreenSize] = useState<{ w: number; h: number } | null>(null);
  const [matches, setMatches] = useState<GroundMatch[] | null>(null);
  const [phrase, setPhrase] = useState('');
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const screenBox = useRef<HTMLDivElement>(null);

  // refresh reloads the machine list and the budget mirror. Promise
  // chains, not await: the session and list states resolve off the
  // effect's synchronous path, the way Terminals.refresh does.
  const refresh = useCallback((): Promise<void> => {
    return listMachines().then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        if (typeof res !== 'object') {
          setState({ view: 'list', machines: [], budget: 0, notice: errorWords(res), refused: null });
          return;
        }
        setState({ view: 'list', machines: res.machines, budget: res.budget, notice: null, refused: null });
      },
      () => setState({ view: 'list', machines: [], budget: 0, notice: errorWords('unreachable'), refused: null }),
    );
  }, [onSessionExpired]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // The live screen re-fetches every 2 s while a machine is linked
  // and the tab is visible: the img src carries a counter, so the
  // browser never serves a stale frame from its cache.
  const linkedCount = state.view === 'list' ? state.machines.length : 0;
  useEffect(() => {
    if (linkedCount === 0) {
      return;
    }
    const tick = () => {
      if (document.visibilityState === 'visible') {
        setScreenAt((n) => n + 1);
      }
    };
    tick();
    const timer = window.setInterval(tick, 2000);
    return () => window.clearInterval(timer);
  }, [linkedCount]);

  // machine is the single linked machine's name (the only case the
  // task view supports today); '' lets the API pick it the same way.
  const machine = state.view === 'list' && state.machines.length === 1 ? state.machines[0] : '';

  // tap posts one click when take control is on. The machine spends
  // one budget unit there; a refusal lands as its own words.
  async function tap(e: React.MouseEvent<HTMLDivElement>): Promise<void> {
    if (!control || !screenSize || screenBox.current === null) {
      return;
    }
    const { x, y } = toScreen(e, screenBox.current, screenSize.w, screenSize.h);
    const res = await machineInput({ kind: 'tap', x, y }, machine);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'object') {
      setState((s) => (s.view === 'list' ? { ...s, refused: res.refused } : s));
      return;
    }
    if (res !== true) {
      setState((s) => (s.view === 'list' ? { ...s, refused: errorWords(res) } : s));
      return;
    }
    setState((s) => (s.view === 'list' ? { ...s, refused: null } : s));
    const b = await machineBudget(machine);
    if (typeof b === 'object') {
      setState((s) => (s.view === 'list' ? { ...s, budget: b.remaining } : s));
    }
    setScreenAt((n) => n + 1);
  }

  async function find(): Promise<void> {
    if (phrase.trim() === '') {
      return;
    }
    setBusy('find');
    const res = await machineGround(phrase, machine);
    setBusy(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'string' || !('matches' in res)) {
      const words = typeof res === 'string' ? errorWords(res) : res.refused;
      setState((s) => (s.view === 'list' ? { ...s, refused: words } : s));
      return;
    }
    setMatches(res.matches);
  }

  async function checkDone(): Promise<void> {
    setBusy('verify');
    const res = await machineVerify(machine);
    setBusy(null);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (typeof res === 'string' || !('png' in res)) {
      const words = typeof res === 'string' ? errorWords(res) : res.refused;
      setState((s) => (s.view === 'list' ? { ...s, refused: words } : s));
      return;
    }
    setNote(res.note === '' ? 'The verifier saw a screen.' : res.note);
  }

  if (state.view === 'loading') {
    return <div className="flex flex-1 items-center justify-center" aria-busy="true" />;
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lead font-semibold text-text">Machines</h2>
        <span className="text-body text-muted">{state.budget} actions left</span>
      </div>
      {state.notice !== null && (
        <p role="alert" className="text-body text-text">
          {state.notice}
        </p>
      )}
      {state.refused !== null && (
        <p role="alert" className="text-body text-text">
          {state.refused}
        </p>
      )}
      {state.machines.length === 0 ? (
        <>
          <p role="status" className="text-body text-muted">
            Machines will appear here when one enrols.
          </p>
          {state.notice === null && (
            <Empty title="No machines yet." body="Enrol a machine to see its screen and take control here." />
          )}
        </>
      ) : (
        <>
          <ul className="flex flex-col gap-2">
            {state.machines.map((m) => (
              <li
                key={m}
                className="flex min-h-11 items-center justify-between gap-2 rounded-md bg-surface px-3 py-2"
              >
                <span className="min-w-0 flex-1 truncate font-mono text-body text-text">{m}</span>
                <span className="text-body text-muted">linked</span>
              </li>
            ))}
          </ul>
          <div className="flex items-center justify-between gap-2">
            <button
              type="button"
              onClick={() => { setControl((on) => !on); setMatches(null); }}
              aria-pressed={control}
              className={`min-h-11 rounded-md px-4 text-body text-base ${control ? 'bg-accent text-on-accent' : 'bg-surface text-text'}`}
            >
              {control ? 'Take control: on' : 'Take control'}
            </button>
            <button
              type="button"
              onClick={() => void checkDone()}
              disabled={busy !== null}
              className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
            >
              {busy === 'verify' ? 'Checking…' : 'Check done'}
            </button>
          </div>
          {/* The live screen. The ref holds the rendered box, so a
              control tap maps its own coordinates to screen pixels;
              ground markers draw in the same space. */}
          <div
            ref={screenBox}
            onClick={(e) => void tap(e)}
            className={`relative mx-auto w-full max-w-3xl overflow-hidden rounded-md bg-surface ${control ? 'cursor-crosshair' : ''}`}
            aria-label={control ? 'Machine screen, tap to click' : 'Machine screen'}
          >
            <img
              src={`${machineScreenURL(machine)}&t=${screenAt}`}
              alt="The machine's live screen"
              className="block w-full select-none"
              onLoad={(e) => {
                const img = e.currentTarget;
                if (img.naturalWidth > 0) {
                  setScreenSize({ w: img.naturalWidth, h: img.naturalHeight });
                }
              }}
            />
            {matches !== null &&
              matches.map((m) => (
                <span
                  key={m.id}
                  aria-label={`Grounded match ${m.id + 1}`}
                  className="pointer-events-none absolute h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-accent bg-accent/30"
                  style={{ left: `${(m.clickX / (screenSize?.w ?? 1)) * 100}%`, top: `${(m.clickY / (screenSize?.h ?? 1)) * 100}%` }}
                />
              ))}
          </div>
          <form
            className="flex flex-col gap-2 sm:flex-row"
            onSubmit={(e) => {
              e.preventDefault();
              void find();
            }}
          >
            <label className="sr-only" htmlFor="machine-phrase">
              What to find
            </label>
            <input
              id="machine-phrase"
              type="text"
              value={phrase}
              onChange={(e) => setPhrase(e.target.value)}
              placeholder="Name one thing, like green Pay button"
              className="min-h-11 flex-1 rounded-md bg-surface px-3 text-body text-text"
            />
            <button
              type="submit"
              disabled={busy !== null || phrase.trim() === ''}
              className="min-h-11 rounded-md bg-accent px-4 text-body text-base disabled:opacity-60"
            >
              {busy === 'find' ? 'Finding…' : 'Find it'}
            </button>
          </form>
          {matches !== null && (
            <p role="status" className="text-body text-muted">
              {matches.length === 0
                ? 'No matches on the screen. Try another phrase.'
                : `${matches.length} ${matches.length === 1 ? 'match' : 'matches'} marked on the screen.`}
            </p>
          )}
          {note !== null && (
            <p role="note" className="rounded-md bg-surface px-3 py-2 text-body text-text">
              {note}
            </p>
          )}
        </>
      )}
    </div>
  );
}
