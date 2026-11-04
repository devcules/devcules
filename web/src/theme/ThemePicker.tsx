import { useEffect, useRef, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';
import { FEATURED_THEMES, THEMES, currentTheme, setTheme } from '../theme/themes';
import type { Theme } from '../theme/themes';

// Featured (the five showcase themes) first, then the other dark
// themes, then the other light ones. The listbox caps at the viewport
// and scrolls, so every theme stays reachable on a 720px screen.
const GROUPS: Array<{ name: string; themes: Theme[] }> = [
  { name: 'featured', themes: FEATURED_THEMES.map((id) => THEMES.find((t) => t.id === id)).filter((t): t is Theme => !!t) },
  { name: 'dark', themes: THEMES.filter((t) => t.scheme === 'dark' && !FEATURED_THEMES.includes(t.id)) },
  { name: 'light', themes: THEMES.filter((t) => t.scheme === 'light' && !FEATURED_THEMES.includes(t.id)) },
];
const ORDER: Theme[] = GROUPS.flatMap((g) => g.themes);

// The top-bar theme picker: one button that reads "◑ <theme>" and
// opens a dropdown, featured items then dark then light, each with
// its accent dot. It sits in the title bar so the theme is reachable
// from every screen; the pick persists per browser and applies at once.
// Keyboard: focus moves into the list on open (the current theme),
// Up/Down/Home/End move a roving tabindex, Enter/Space pick, Escape
// closes back to the trigger.
export default function ThemePicker() {
  const [theme, setThemeState] = useState(currentTheme);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const options = useRef<Array<HTMLButtonElement | null>>([]);

  // Outside click and Escape close. React-strict double-mount arms
  // the listener twice; the ref unmount cleans both up.
  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(e: MouseEvent): void {
      if (root.current && !root.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(e: KeyboardEvent): void {
      if (e.key === 'Escape') {
        setOpen(false);
        if (root.current && root.current.contains(document.activeElement)) {
          trigger.current?.focus();
        }
      }
    }
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Another control on the page (the devtools preset launcher) may
  // switch the theme: keep the label honest.
  useEffect(() => {
    function onChanged(e: Event): void {
      setThemeState((e as CustomEvent<{ id: string }>).detail.id);
    }
    window.addEventListener('devcules:theme-change', onChanged);
    return () => window.removeEventListener('devcules:theme-change', onChanged);
  }, []);

  // On open (toggle() made the current theme the active option), that
  // option takes focus.
  useEffect(() => {
    if (open) {
      options.current[ORDER.findIndex((t) => t.id === theme)]?.focus();
    }
  }, [open, theme]);

  function toggle(): void {
    if (!open) {
      setActive(Math.max(0, ORDER.findIndex((t) => t.id === theme)));
    }
    setOpen((o) => !o);
  }

  function move(i: number): void {
    const n = ORDER.length;
    const next = ((i % n) + n) % n;
    setActive(next);
    options.current[next]?.focus();
  }

  function onListKey(e: ReactKeyboardEvent<HTMLDivElement>): void {
    switch (e.key) {
      case 'ArrowDown':
        move(active + 1);
        break;
      case 'ArrowUp':
        move(active - 1);
        break;
      case 'Home':
        move(0);
        break;
      case 'End':
        move(ORDER.length - 1);
        break;
      default:
        return;
    }
    e.preventDefault();
  }

  function pick(id: string): void {
    setTheme(id);
    setThemeState(id);
    setOpen(false);
    trigger.current?.focus();
  }

  const current = THEMES.find((t) => t.id === theme) ?? THEMES[0];

  return (
    <div ref={root} className="relative">
      <button
        ref={trigger}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label="Theme"
        onClick={toggle}
        data-selected={open ? 'true' : undefined}
        className="i-control flex min-h-9 items-center gap-1.5 px-2 text-body text-muted"
      >
        <span
          aria-hidden="true"
          className="h-3 w-3 rounded-full border border-muted"
          style={{ backgroundColor: current.swatch[2] }}
        />
        <span className="text-body">{current.label}</span>
        <span aria-hidden="true" className="text-tab">
          ▾
        </span>
      </button>
      {open && (
        <div
          role="listbox"
          aria-label="Themes"
          onKeyDown={onListKey}
          className="absolute right-0 z-20 mt-1 max-h-[calc(100dvh-4rem)] w-48 overflow-y-auto overscroll-contain rounded-md border border-muted bg-surface py-1 shadow-lg"
        >
          {GROUPS.map((g) => (
            <div key={g.name} role="group" aria-label={g.name}>
              <div aria-hidden="true" className="px-3 pt-1 text-tab uppercase text-muted">{g.name}</div>
              {g.themes.map((t) => {
                const picked = t.id === theme;
                const i = ORDER.indexOf(t);
                return (
                  <button
                    key={t.id}
                    ref={(el) => {
                      options.current[i] = el;
                    }}
                    type="button"
                    role="option"
                    aria-selected={picked}
                    tabIndex={i === active ? 0 : -1}
                    onClick={() => pick(t.id)}
                    onFocus={() => setActive(i)}
                    className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-body transition-colors duration-150 hover:bg-base focus-visible:bg-base ${
                      picked ? 'text-accent' : 'text-text'
                    }`}
                  >
                    <span
                      aria-hidden="true"
                      className="h-3 w-3 shrink-0 rounded-full border border-muted"
                      style={{ backgroundColor: t.swatch[2] }}
                    />
                    <span className="min-w-0 flex-1 truncate">{t.label}</span>
                    {picked && <span aria-hidden="true">✓</span>}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
