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
  /** One line under the label in the picker — what it's for. */
  hint: string;
  scheme: 'dark' | 'light';
  /** The three swatches the picker shows: base, surface, accent. */
  swatch: [string, string, string];
}

export const THEMES: Theme[] = [
  {
    id: 'arc',
    label: 'Arc',
    hint: 'The default. Warm amber over neutral dark; light follows your OS unless you pick Paper.',
    scheme: 'dark',
    swatch: ['#101418', '#1a212b', '#e0a63f'],
  },
  {
    id: 'paper',
    label: 'Paper',
    hint: 'Daylight desk. Light always, whatever the OS says.',
    scheme: 'light',
    swatch: ['#ffffff', '#f1f2f4', '#8a5a00'],
  },
  {
    id: 'slate',
    label: 'Slate',
    hint: 'Muted blue-grey for long sessions; low-chroma, high quiet.',
    scheme: 'dark',
    swatch: ['#11151c', '#1b222e', '#8fa8c9'],
  },
  {
    id: 'midnight',
    label: 'Midnight',
    hint: 'Room-dark blue with a cold sky accent. For the 2 a.m. commit.',
    scheme: 'dark',
    swatch: ['#0a0f24', '#131a34', '#7aa2f7'],
  },
  {
    id: 'meta-hackathon',
    label: 'IASF',
    hint: 'Meta AI Developer Hackathon (Menlo Park, Oct 26). Electric blue, liquid glass.',
    scheme: 'dark',
    swatch: ['#061020', '#0d1b33', '#0082fb'],
  },
];

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

// Settings fold record — which sections stand open, remembered per
// browser like the theme. An explicit choice is stored (true =
// folded, false = unfolded); sections never touched fall back to the
// page's defaults, so an old record outlives a page reorganisation.
// Storage-off (private window) degrades to the defaults per view.
export const FOLDS_KEY = 'devcules.settings.folds';

export function readFolds(): Record<string, boolean> {
  try {
    const raw = window.localStorage.getItem(FOLDS_KEY);
    const obj: unknown = raw ? JSON.parse(raw) : {};
    if (obj === null || typeof obj !== 'object') {
      return {};
    }
    return Object.fromEntries(
      Object.entries(obj as Record<string, unknown>).filter(
        (e): e is [string, boolean] => typeof e[1] === 'boolean',
      ),
    );
  } catch {
    return {};
  }
}

export function writeFold(id: string, folded: boolean): void {
  try {
    const folds = readFolds();
    folds[id] = folded;
    window.localStorage.setItem(FOLDS_KEY, JSON.stringify(folds));
  } catch {
    // Folding still works for this page view.
  }
}
