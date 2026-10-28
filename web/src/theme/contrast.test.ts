import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { contrast } from './contrast';

// Every text/background token pair, in both themes, must meet WCAG
// AA for body text (4.5:1). The values are read from tokens.css
// itself, so this test guards the shipped colours, not a copy of
// them. (A ?raw import returns empty under the Tailwind plugin, and
// import.meta.url does not survive the transform, so the test reads
// the source file from the project root, where vitest runs.)
const css = readFileSync('src/theme/tokens.css', 'utf8');
function tokens(scheme: 'dark' | 'light'): Record<string, string> {
  const block =
    scheme === 'dark'
      ? /:root\s*{([^}]*)}/.exec(css)?.[1]
      : /@media\s*\(prefers-color-scheme:\s*light\)\s*{\s*:root\s*{([^}]*)}/.exec(css)?.[1];
  if (!block) {
    throw new Error(`no ${scheme} token block in tokens.css`);
  }
  const out: Record<string, string> = {};
  for (const m of block.matchAll(/(--c-[a-z-]+)\s*:\s*(#[0-9a-fA-F]{6})/g)) {
    out[m[1]] = m[2];
  }
  return out;
}

// [foreground, background]: body text, muted text, and the accent in
// both directions (accent marks "needs you" text; on-accent sits on
// accent buttons).
const pairs: Array<[string, string]> = [
  ['--c-text', '--c-base'],
  ['--c-text', '--c-surface'],
  ['--c-muted', '--c-base'],
  ['--c-muted', '--c-surface'],
  ['--c-accent', '--c-base'],
  ['--c-on-accent', '--c-accent'],
];

describe.each(['dark', 'light'] as const)('%s theme contrast', (scheme) => {
  const t = tokens(scheme);
  for (const [fg, bg] of pairs) {
    it(`${fg} on ${bg} meets AA`, () => {
      expect(t[fg], `${fg} missing`).toBeDefined();
      expect(t[bg], `${bg} missing`).toBeDefined();
      expect(contrast(t[fg], t[bg])).toBeGreaterThanOrEqual(4.5);
    });
  }
});
