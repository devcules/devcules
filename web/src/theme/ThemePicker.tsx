import { useEffect, useRef, useState } from 'react';
import { THEMES, currentTheme, setTheme } from '../theme/themes';

// The top-bar theme picker: one button that reads "◑ <theme>" and
// opens a dropdown, dark items then light, each with its accent dot.
// Kaithos keeps this in the title bar so the theme is reachable from
// every screen; the pick persists per browser and applies at once.
export default function ThemePicker() {
  const [theme, setThemeState] = useState(currentTheme);
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

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

  function pick(id: string): void {
    setTheme(id);
    setThemeState(id);
    setOpen(false);
  }

  const current = THEMES.find((t) => t.id === theme) ?? THEMES[0];

  return (
    <div ref={root} className="relative">
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label="Theme"
        onClick={() => setOpen((o) => !o)}
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
          className="absolute right-0 z-20 mt-1 w-48 rounded-md border border-muted bg-surface py-1 shadow-lg"
        >
          {(['dark', 'light'] as const).map((scheme) => (
            <div key={scheme}>
              <div className="px-3 pt-1 text-tab uppercase text-muted">{scheme}</div>
              {THEMES.filter((t) => t.scheme === scheme).map((t) => {
                const picked = t.id === theme;
                return (
                  <button
                    key={t.id}
                    type="button"
                    role="option"
                    aria-selected={picked}
                    onClick={() => pick(t.id)}
                    className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-body transition-colors duration-150 hover:bg-base ${
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
