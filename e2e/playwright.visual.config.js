import { defineConfig } from '@playwright/test';

// Visual-only run: screenshot baselines for the sign-in screen and
// the Home shell, dark and light, at both sizes. The flow suite
// keeps its own config; this one runs in CI e2e after it, and with
// --update-snapshots in the e2e-baselines workflow (Linux only).
export default defineConfig({
  testDir: './tests',
  testMatch: 'visual.spec.js',
  timeout: 30_000,
  fullyParallel: true,
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:7659',
  },
  projects: [
    { name: 'desktop-dark', use: { browserName: 'chromium', viewport: { width: 1280, height: 800 }, colorScheme: 'dark' } },
    { name: 'desktop-light', use: { browserName: 'chromium', viewport: { width: 1280, height: 800 }, colorScheme: 'light' } },
    { name: 'mobile-dark', use: { browserName: 'chromium', viewport: { width: 390, height: 844 }, colorScheme: 'dark' } },
    { name: 'mobile-light', use: { browserName: 'chromium', viewport: { width: 390, height: 844 }, colorScheme: 'light' } },
  ],
});
