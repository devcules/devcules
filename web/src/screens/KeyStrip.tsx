import { useEffect, useState } from 'react';

// One-shot keys for the phone strip. Every target clears 44 px and
// carries a screen-reader label; the strip sits above the software
// keyboard by tracking the visual viewport.
export type StripKey = 'esc' | 'tab' | 'up' | 'down' | 'left' | 'right' | 'enter';

const baseBytes: Record<StripKey, number[]> = {
  esc: [0x1b],
  tab: [0x09],
  up: [0x1b, 0x5b, 0x41],
  down: [0x1b, 0x5b, 0x42],
  right: [0x1b, 0x5b, 0x43],
  left: [0x1b, 0x5b, 0x44],
  enter: [0x0d],
};

// Ctrl-modified arrows are real terminal sequences; the rest have no
// control form, so an armed Ctrl plus one of those sends the plain
// key and disarms.
const ctrlArrows: Partial<Record<StripKey, number[]>> = {
  up: [0x1b, 0x5b, 0x31, 0x3b, 0x35, 0x41],
  down: [0x1b, 0x5b, 0x31, 0x3b, 0x35, 0x42],
  right: [0x1b, 0x5b, 0x31, 0x3b, 0x35, 0x43],
  left: [0x1b, 0x5b, 0x31, 0x3b, 0x35, 0x44],
};

// stripBytes resolves one strip tap to terminal bytes. An armed Ctrl
// applies to arrows; any other strip key sends plain and disarms, so
// Ctrl only ever changes one keypress.
export function stripBytes(key: StripKey, ctrlArmed: boolean): { bytes: number[]; keepArmed: boolean } {
  if (ctrlArmed && ctrlArrows[key] !== undefined) {
    return { bytes: ctrlArrows[key] ?? [], keepArmed: false };
  }
  return { bytes: baseBytes[key], keepArmed: false };
}

// ctrlLetter resolves a typed letter under an armed Ctrl to its
// control code (a/A -> 0x01 through z/Z -> 0x1a). Anything else is
// not a letter: the caller sends it plain.
export function ctrlLetter(data: string): number | null {
  if (!/^[a-zA-Z]$/.test(data)) {
    return null;
  }
  return data.toUpperCase().charCodeAt(0) - 64;
}

// useKeyboardOffset tracks the software keyboard height through the
// visual viewport, so the fixed strip rides above it. Without a
// visual viewport (desktop) the offset stays zero.
export function useKeyboardOffset(): number {
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) {
      return;
    }
    const update = () => {
      setOffset(Math.max(0, window.innerHeight - vv.height - vv.offsetTop));
    };
    update();
    vv.addEventListener('resize', update);
    vv.addEventListener('scroll', update);
    return () => {
      vv.removeEventListener('resize', update);
      vv.removeEventListener('scroll', update);
    };
  }, []);
  return offset;
}

const keys: Array<{ key: StripKey; label: string; text: string }> = [
  { key: 'esc', label: 'Escape', text: 'Esc' },
  { key: 'tab', label: 'Tab', text: 'Tab' },
  { key: 'left', label: 'Left arrow', text: '←' },
  { key: 'up', label: 'Up arrow', text: '↑' },
  { key: 'down', label: 'Down arrow', text: '↓' },
  { key: 'right', label: 'Right arrow', text: '→' },
  { key: 'enter', label: 'Enter', text: '⏎' },
];

// keepFocus stops a strip tap from taking focus off the terminal, so
// the phone keyboard stays up; the click still fires.
function keepFocus(e: { preventDefault: () => void }): void {
  e.preventDefault();
}

// All eight keys fit 390 px (8 × 44 px plus gaps and padding), so
// Enter is never scrolled off the edge; narrower screens still scroll.
export default function KeyStrip({
  ctrlArmed,
  onToggleCtrl,
  onKey,
}: {
  ctrlArmed: boolean;
  onToggleCtrl: () => void;
  onKey: (key: StripKey) => void;
}) {
  const offset = useKeyboardOffset();
  return (
    <div
      role="toolbar"
      aria-label="Terminal keys"
      className="fixed inset-x-0 bottom-0 z-10 flex items-center gap-0.5 overflow-x-auto bg-surface px-2 pt-2"
      style={{ paddingBottom: `calc(env(safe-area-inset-bottom) + 8px + ${offset}px)` }}
    >
      <button
        type="button"
        aria-label="Control (sticky modifier)"
        aria-pressed={ctrlArmed}
        onMouseDown={keepFocus}
        onClick={onToggleCtrl}
        className={`min-h-11 min-w-11 flex-1 rounded-md px-1 text-body transition-colors duration-150 ${
          ctrlArmed ? 'bg-accent text-base' : 'text-text'
        }`}
      >
        Ctrl
      </button>
      {keys.map((k) => (
        <button
          key={k.key}
          type="button"
          aria-label={k.label}
          onMouseDown={keepFocus}
          onClick={() => onKey(k.key)}
          className="min-h-11 min-w-11 flex-1 rounded-md px-1 text-body text-text transition-colors duration-150"
        >
          {k.text}
        </button>
      ))}
    </div>
  );
}
