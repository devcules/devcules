import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { contrast } from './contrast';
import { THEMES } from './themes';

// Every text/background token pair, in every theme, must meet WCAG
// AA for body text (4.5:1). The values are read from tokens.css
// itself, so this test guards the shipped colours, not a copy of
// them. (A ?raw import returns empty under the Tailwind plugin, and
// import.meta.url does not survive the transform, so the test reads
// the source file from the project root, where vitest runs.)
//
// Tokens are found by scoped regex over each theme block: the arc
// pair (no attribute, plus its light media query) and one block per
// data-theme. A theme in themes.ts without its CSS block fails the
// "missing" assertion; a CSS block with no registry entry is fine
// (it is a pair the parser skipped past), which keeps the failures
// named rather than structural.
const css = readFileSync('src/theme/tokens.css', 'utf8');

function blockTokens(block: string): Record<string, string> {
  const wanted: Record<string, string> = {};
  for (const m of block.matchAll(/(--c-[a-z-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    wanted[m[1]] = m[2].toLowerCase();
  }
  return wanted;
}

function lightBlock(): string {
  const m = /@media\s*\(prefers-color-scheme:\s*light\)\s*\{\s*:root\s*\{([^}]*)\}/.exec(css);
  if (!m) {
    throw new Error('light media block missing from tokens.css');
  }
  return m[1];
}

// themeBlock(id): Arc's the default; its block is the bare :root.
// Others match the literal :root[data-theme="<id>"] block.
function themeBlock(id: string): string {
  const re =
    id === 'arc'
      ? /^:root\s*\{([^}]*)\}/m
      : new RegExp(`:root\\[data-theme="${id}"\\]\\s*\\{([^}]*)\\}`);
  const m = re.exec(css);
  if (!m) {
    throw new Error(`theme block missing for "${id}" in tokens.css`);
  }
  return m[1];
}

// The pairs every theme must hold to WCAG AA: body text on both
// surfaces, the quiet text, and the two accent pairings.
const pairs: Array<[string, string]> = [
  ['--c-text', '--c-base'],
  ['--c-text', '--c-surface'],
  ['--c-muted', '--c-base'],
  ['--c-muted', '--c-surface'],
  ['--c-accent', '--c-base'],
  ['--c-on-accent', '--c-accent'],
];

for (const theme of THEMES) {
  describe(`${theme.id} theme contrast`, () => {
    it('has a block in tokens.css', () => {
      expect(() => themeBlock(theme.id)).not.toThrow();
    });
    for (const [fg, bg] of pairs) {
      it(`${fg} on ${bg} meets AA`, () => {
        const t = blockTokens(themeBlock(theme.id));
        expect(t[fg], `${fg} missing in ${theme.id}`).toBeDefined();
        expect(t[bg], `${bg} missing in ${theme.id}`).toBeDefined();
        expect(contrast(t[fg], t[bg])).toBeGreaterThanOrEqual(4.5);
      });
    }
  });
}

// The Arc pair also ships a light media variant (OS follows light
// when no theme is picked). Its six tokens must pass the same AA
// bar; the registry's "paper" swatch is the same file's light pair,
// explicit.
describe('arc default light (OS) contrast', () => {
  for (const [fg, bg] of pairs) {
    it(`${fg} on ${bg} meets AA`, () => {
      const t = blockTokens(lightBlock());
      expect(t[fg], `${fg} missing in arc/light`).toBeDefined();
      expect(t[bg], `${bg} missing in arc/light`).toBeDefined();
      expect(contrast(t[fg], t[bg])).toBeGreaterThanOrEqual(4.5);
    });
  }
});

// Status colours on the light schemes (DESK-20 / MOB-13). The screens
// use Tailwind's dark-tuned red-400 / amber-400 / the vibe hot pink;
// every light scheme carries danger / warn / hot tokens that hold AA
// on both its base and its surface, and tokens.css repoints the
// utilities at them under data-scheme="light" (and for Arc under the
// OS light query).
const statusTokens = ['--c-danger', '--c-warn', '--c-hot'];
const lightBlocks: Array<[string, () => string]> = [
  ...THEMES.filter((t) => t.scheme === 'light').map((t): [string, () => string] => [t.id, () => themeBlock(t.id)]),
  ['arc (OS light)', lightBlock],
];

for (const [name, block] of lightBlocks) {
  describe(`${name} status colours`, () => {
    for (const fg of statusTokens) {
      for (const bg of ['--c-base', '--c-surface']) {
        it(`${fg} on ${bg} meets AA`, () => {
          const t = blockTokens(block());
          expect(t[fg], `${fg} missing in ${name}`).toBeDefined();
          expect(contrast(t[fg], t[bg])).toBeGreaterThanOrEqual(4.5);
        });
      }
    }
  });
}

describe('light scheme repoints the dark-tuned status utilities', () => {
  const mapBlock = /:root\[data-scheme="light"\]\s*\{([^}]*)\}/.exec(css)?.[1] ?? '';
  it.each([
    ['--color-red-400', '--c-danger'],
    ['--color-red-300', '--c-danger'],
    ['--color-amber-400', '--c-warn'],
  ])('%s -> var(%s)', (util, token) => {
    expect(mapBlock).toMatch(new RegExp(`${util}\\s*:\\s*var\\(${token}\\)`));
  });
  it('the hot pink and amber-500 text classes are overridden (escaped selector)', () => {
    expect(css).toMatch(/:root\[data-scheme="light"\]\)\s*\.text-\\\[\\#ff4fd8\\\]\s*\{\s*color:\s*var\(--c-hot\)/);
    expect(css).toMatch(/:root\[data-scheme="light"\]\)\s*\.text-amber-500\s*\{\s*color:\s*var\(--c-warn\)/);
  });
  it('Arc repeats the mapping under the OS light query', () => {
    expect(css).toMatch(/@media \(prefers-color-scheme: light\)\s*\{\s*:root\[data-theme="arc"\]\s*\{[^}]*--color-red-400:\s*var\(--c-danger\)/);
  });
});

// Holo pressed button (DESK-08): white text on the holo ink gradient,
// so every stop of --v-grad-lime must hold AA against white.
describe('holo pressed .v-btn', () => {
  const future = readFileSync('src/theme/future.css', 'utf8');
  it('has its own pressed rule after the glass .v-btn rule', () => {
    const glass = future.indexOf('[data-theme="holo"] .v-btn {');
    const pressed = future.indexOf('[data-theme="holo"] .v-btn[aria-pressed="true"]');
    expect(glass).toBeGreaterThan(-1);
    expect(pressed).toBeGreaterThan(glass);
    expect(future.slice(pressed, future.indexOf('}', pressed))).toMatch(/background:\s*var\(--v-grad-lime\);\s*color:\s*#ffffff/);
  });
  it('white meets AA on every stop of holo --v-grad-lime', () => {
    const holo = future.slice(future.indexOf('[data-theme="holo"]'));
    const grad = /--v-grad-lime:\s*linear-gradient\(([^;]*)\);/.exec(holo)?.[1] ?? '';
    const stops = grad.match(/#[0-9a-fA-F]{6}/g) ?? [];
    expect(stops.length).toBeGreaterThan(1);
    for (const s of stops) {
      expect(contrast('#ffffff', s), s).toBeGreaterThanOrEqual(4.5);
    }
  });
});

// Calendar slot lines (DESK-09) and the CSP-safe resize corner (DESK-11).
describe('main.css desk rules', () => {
  const main = readFileSync('src/main.css', 'utf8');
  it('hour slots use the grid border token, not a solid muted line', () => {
    const rule = /\.d-calendar \.fc-timegrid-slot \{([^}]*)\}/.exec(main)?.[1] ?? '';
    expect(rule).toMatch(/border-color:\s*var\(--fc-border-color\)/);
    expect(rule).not.toMatch(/var\(--c-muted\)/);
  });
  it('the resize handle drops the data: SVG (CSP) and draws a visible CSS corner', () => {
    const handle = /\.react-grid-layout \.react-grid-item > \.react-resizable-handle \{([^}]*)\}/.exec(main)?.[1] ?? '';
    expect(handle).toMatch(/background-image:\s*none/);
    expect(handle).toMatch(/opacity:\s*1/);
    const corner = /\.react-grid-layout \.react-grid-item > \.react-resizable-handle::after \{([^}]*)\}/.exec(main)?.[1] ?? '';
    expect(corner).toMatch(/border-right-style:\s*solid/);
    expect(corner).toMatch(/border-color:\s*var\(--c-accent\)/);
  });
});
