import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, type Plugin } from 'vitest/config';

// emptyOutDir wipes the committed placeholder that keeps go build
// working without Node, and git would then stage its deletion. Read
// it before the build and write it back after. The Docker web stage
// has no placeholder (only web/ is copied), so there is nothing to
// keep.
const placeholder = '../internal/web/dist/.gitkeep';
function keepPlaceholder(): Plugin {
  let text: string | null = null;
  return {
    name: 'keep-placeholder',
    apply: 'build',
    buildStart() {
      text = existsSync(placeholder) ? readFileSync(placeholder, 'utf8') : null;
    },
    closeBundle() {
      if (text !== null) {
        writeFileSync(placeholder, text);
      }
    },
  };
}

// The build lands in internal/web/dist, which serve embeds: plain
// `npm run build` is the whole publish step (see docs/web.md).
// assetsInlineLimit 0 keeps every asset a separate file, which the
// strict content security policy requires (no data: URIs to allow).
export default defineConfig({
  plugins: [react(), tailwindcss(), keepPlaceholder()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    coverage: {
      enabled: true,
      provider: 'v8',
      reporter: ['text', 'html'],
      thresholds: {
        lines: 70,
        functions: 70,
        branches: 70,
        statements: 70,
      },
    },
  },
});
