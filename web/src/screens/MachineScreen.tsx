import { useEffect, useRef, useState } from 'react';
import {
  machineBudget,
  machineGround,
  machineInput,
  machineScreen,
  machineVerify,
  type GroundMatch,
  type MachineError,
} from '../api';

// errorWords turns API outcomes into plain words. 'unauthorized'
// never lands here: callers route it to sign-in first.
function errorWords(err: MachineError): string {
  if (err === 'offline') {
    return 'This machine is offline. Start devcules machine on it and try again.';
  }
  return 'Something went wrong. Try again.';
}

// toScreen maps a click inside the rendered image to the machine's
// screen pixels, proportionally: the driver's boxes live in the
// screen's own space, which the frame may show shrunk.
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

// MachineScreen is the task view for one machine: the live screen,
// take control, grounding a phrase and the done check.
export default function MachineScreen({
  machine,
  budget: initialBudget,
  onBack,
  onSessionExpired,
}: {
  machine: string;
  budget: number;
  onBack: () => void;
  onSessionExpired: () => void;
}) {
  const [frame, setFrame] = useState<string | null>(null);
  const [screenSize, setScreenSize] = useState<{ w: number; h: number } | null>(null);
  const [tick, setTick] = useState(0);
  const [control, setControl] = useState(false);
  const [matches, setMatches] = useState<GroundMatch[] | null>(null);
  const [phrase, setPhrase] = useState('');
  const [note, setNote] = useState<string | null>(null);
  const [refused, setRefused] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [budget, setBudget] = useState(initialBudget);
  const scaleRef = useRef(1);
  const frameRef = useRef<string | null>(null);
  const screenBox = useRef<HTMLDivElement>(null);

  // A fresh frame every 2 s while the tab is visible.
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        setTick((n) => n + 1);
      }
    }, 2000);
    return () => window.clearInterval(timer);
  }, []);

  // Each frame arrives as a blob with its scale; the previous object
  // URL is revoked once the next one is showing.
  useEffect(() => {
    let live = true;
    void machineScreen(machine).then((res) => {
      if (!live) {
        if (typeof res === 'object' && 'url' in res) {
          URL.revokeObjectURL(res.url);
        }
        return;
      }
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (typeof res === 'string') {
        setRefused(errorWords(res));
        return;
      }
      if ('refused' in res) {
        setRefused(res.refused);
        return;
      }
      scaleRef.current = res.scale;
      if (frameRef.current !== null) {
        URL.revokeObjectURL(frameRef.current);
      }
      frameRef.current = res.url;
      setFrame(res.url);
    });
    return () => {
      live = false;
    };
  }, [machine, tick, onSessionExpired]);

  useEffect(
    () => () => {
      if (frameRef.current !== null) {
        URL.revokeObjectURL(frameRef.current);
      }
    },
    [],
  );

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
      setRefused(res.refused);
      return;
    }
    if (res !== true) {
      setRefused(errorWords(res));
      return;
    }
    setRefused(null);
    const b = await machineBudget(machine);
    if (typeof b === 'object') {
      setBudget(b.remaining);
    }
    setTick((n) => n + 1);
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
      setRefused(typeof res === 'string' ? errorWords(res) : res.refused);
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
      setRefused(typeof res === 'string' ? errorWords(res) : res.refused);
      return;
    }
    setNote(res.note === '' ? 'The verifier saw a screen.' : res.note);
  }

  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <div className="flex items-center justify-between gap-2">
        <button type="button" onClick={onBack} className="min-h-11 rounded-md px-3 text-body text-text">
          ← Machines
        </button>
        <h2 className="min-w-0 flex-1 truncate font-mono text-lead text-text">{machine}</h2>
        <span className="text-body text-muted">{budget} actions left</span>
      </div>
      {refused !== null && (
        <p role="alert" className="text-body text-text">
          {refused}
        </p>
      )}
      <div className="flex items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => {
            setControl((on) => !on);
            setMatches(null);
          }}
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
      {/* The live screen. The ref holds the rendered box, so a control
          tap maps its own coordinates to screen pixels; ground markers
          draw in the same space. */}
      <div
        ref={screenBox}
        onClick={(e) => void tap(e)}
        className={`relative mx-auto w-full max-w-3xl overflow-hidden rounded-md bg-surface ${control ? 'cursor-crosshair' : ''}`}
        aria-label={control ? 'Machine screen, tap to click' : 'Machine screen'}
      >
        {frame !== null ? (
          <img
            src={frame}
            alt="The machine's live screen"
            className="block w-full select-none"
            onLoad={(e) => {
              const img = e.currentTarget;
              if (img.naturalWidth > 0) {
                const s = scaleRef.current;
                setScreenSize({ w: Math.round(img.naturalWidth * s), h: Math.round(img.naturalHeight * s) });
              }
            }}
          />
        ) : (
          <div className="flex aspect-video items-center justify-center text-body text-muted" aria-busy="true">
            Loading the screen…
          </div>
        )}
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
    </div>
  );
}
