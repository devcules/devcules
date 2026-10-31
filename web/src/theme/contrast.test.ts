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
