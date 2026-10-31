import { useState } from 'react';
import { THEMES, currentTheme, setTheme } from '../theme/themes';

// Theme section of Settings: a radio group of rows, one per theme.
// Each row shows base/surface/accent swatches, the name, and what
// it's for. The change applies at once and persists; it is purely
// local, so there is no server round-trip and no session expiry.
export default function ThemePicker() {
  const [theme, setThemeState] = useState(currentTheme);

  function pick(id: string): void {
    setTheme(id);
    setThemeState(id);
  }

  return (
    <section aria-labelledby="theme-heading">
      <h2 id="theme-heading" className="text-lead font-semibold">
        Theme
      </h2>
      <div role="radiogroup" aria-labelledby="theme-heading" className="mt-3 space-y-2">
        {THEMES.map((t) => (
          <label key={t.id} className="block cursor-pointer" onClick={() => pick(t.id)}>
            <input
              type="radio"
              name="theme"
              value={t.id}
              checked={theme === t.id}
              readOnly
              className="sr-only peer"
            />
            <span className="flex min-h-11 items-start gap-3 rounded-md bg-surface p-3 transition-colors duration-150 peer-checked:ring-2 peer-checked:ring-accent">
              <span
                aria-hidden="true"
                className="mt-0.5 grid h-6 w-10 shrink-0 grid-cols-2 overflow-hidden rounded border border-muted"
              >
                <span style={{ backgroundColor: t.swatch[0] }} />
                <span className="grid grid-rows-2">
                  <span style={{ backgroundColor: t.swatch[1] }} />
                  <span style={{ backgroundColor: t.swatch[2] }} />
                </span>
              </span>
              <span>
                <span className="block text-body font-semibold text-text">{t.label}</span>
                <span className="block text-body text-muted">{t.hint}</span>
              </span>
            </span>
          </label>
        ))}
      </div>
    </section>
  );
}
