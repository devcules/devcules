// Theme registry — one source of truth for the selectable palettes,
// their persisted key, and how they apply. The palette values
// themselves live in theme/tokens.css (one `data-theme` block each);
// this file knows only ids, labels and hints. The contrast test
// parses the CSS, so a theme added in one place and not the other
// fails straight away.

export const THEME_KEY = 'devcules.theme';

export interface Theme {
  id: string;
  label: string;
  scheme: 'dark' | 'light';
  /** The three swatches the theme picker shows: base, surface, accent. */
  swatch: [string, string, string];
}

// One home set above, then the extra palettes, grouped dark then light in the dropdown.
export const THEMES: Theme[] = [
  { id: 'vibe', label: 'vibe ✦', scheme: 'dark', swatch: ['#0b0614', '#1a1030', '#c6ff3d'] },
  { id: 'tron', label: 'gridrunner ◈', scheme: 'dark', swatch: ['#02060f', '#071426', '#00e5ff'] },
  { id: 'ember', label: 'ember 2049 ▲', scheme: 'dark', swatch: ['#120a06', '#1e120b', '#ff7a1a'] },
  { id: 'phosphor', label: 'phosphor ▮', scheme: 'dark', swatch: ['#020703', '#07130a', '#39ff6a'] },
  { id: 'holo', label: 'holo ◎', scheme: 'light', swatch: ['#eef0fa', '#ffffff', '#5b21e6'] },
  { id: 'arc', label: 'arc', scheme: 'dark', swatch: ['#101418', '#1a212b', '#e0a63f'] },
  { id: 'slate', label: 'slate', scheme: 'dark', swatch: ['#11151c', '#1b222e', '#9fb8d4'] },
  { id: 'midnight', label: 'midnight', scheme: 'dark', swatch: ['#0a0f24', '#131a34', '#92b4fb'] },
  { id: 'meta-hackathon', label: 'IASF', scheme: 'dark', swatch: ['#061020', '#0d1b33', '#3d9bfd'] },
  { id: 'nord', label: 'nord', scheme: 'dark', swatch: ['#242933', '#2e3440', '#88c0d0'] },
  { id: 'dracula', label: 'dracula', scheme: 'dark', swatch: ['#21222c', '#282a36', '#bd93f9'] },
  { id: 'gruvbox', label: 'gruvbox', scheme: 'dark', swatch: ['#282828', '#32302f', '#8ec07c'] },
  { id: 'tokyo-night', label: 'tokyo night', scheme: 'dark', swatch: ['#16161e', '#1a1b26', '#bb9af7'] },
  { id: 'solarized', label: 'solarized', scheme: 'dark', swatch: ['#002b36', '#073642', '#6cb3e2'] },
  { id: 'catppuccin', label: 'catppuccin', scheme: 'dark', swatch: ['#1e1e2e', '#303040', '#cba6f7'] },
  { id: 'rose-pine', label: 'rosé pine', scheme: 'dark', swatch: ['#191724', '#1f1d2e', '#ebbcba'] },
  { id: 'everforest', label: 'everforest', scheme: 'dark', swatch: ['#2d353b', '#343f43', '#a7c080'] },
  { id: 'monokai', label: 'monokai', scheme: 'dark', swatch: ['#2d2a2e', '#3a3839', '#ffd866'] },
  { id: 'paper', label: 'paper', scheme: 'light', swatch: ['#ffffff', '#f1f2f4', '#8a5a00'] },
  { id: 'flexoki-light', label: 'flexoki light', scheme: 'light', swatch: ['#fffcf0', '#f2f0e5', '#1d6f68'] },
  { id: 'solarized-light', label: 'solarized light', scheme: 'light', swatch: ['#fdf6e3', '#eee8d5', '#1f6aa6'] },
];

// The five showcase themes, listed first in the picker so the demo set
// (holo, the light one, included) sits at the top of the list.
export const FEATURED_THEMES: readonly string[] = ['vibe', 'tron', 'ember', 'phosphor', 'holo'];

const FALLBACK = THEMES[0];

export function currentTheme(): string {
  try {
    const stored = window.localStorage.getItem(THEME_KEY);
    if (stored && THEMES.some((t) => t.id === stored)) {
      return stored;
    }
  } catch {
    // storage blocked (private window): fall through to the default
  }
  return FALLBACK.id;
}

export function applyTheme(id: string): void {
  const theme = THEMES.find((t) => t.id === id) ?? FALLBACK;
  const root = document.documentElement;
  root.setAttribute('data-theme', theme.id);
  // Arc keeps its original implicit light-follows-OS behaviour; every
  // other theme is explicit, so the media query stops governing it
  // (the CSS keys off the attribute too).
  root.style.colorScheme = theme.scheme;
  // The scheme as an attribute too: light-scheme CSS (tokens.css)
  // repoints the dark-tuned status utilities at AA tokens.
  root.setAttribute('data-scheme', theme.scheme);
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    meta.setAttribute('content', theme.swatch[0]);
  }
}

export function setTheme(id: string): void {
  applyTheme(id);
  try {
    window.localStorage.setItem(THEME_KEY, id);
  } catch {
    // storage blocked: theme applies for this page only
  }
  window.dispatchEvent(new CustomEvent('devcules:theme-change', { detail: { id } }));
}

// Boot hook — call once before the app renders so the first paint
// already carries the palette (set from a <script> in index.html).
export function initTheme(): void {
  applyTheme(currentTheme());
}
