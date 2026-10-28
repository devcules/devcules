import { defineConfig } from '@playwright/test';

// Sign-in flows against a running stack (compose in CI, or a local
// `devcules serve`). The base URL and the bootstrap admin password
// come from the environment; screenshot baselines wait for the M1.2
// React UI, so this suite asserts flows and cookies only.
export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  fullyParallel: true,
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:7659',
  },
  projects: [
    { name: 'chromium-desktop', use: { browserName: 'chromium', viewport: { width: 1280, height: 800 } } },
    { name: 'chromium-mobile', use: { browserName: 'chromium', viewport: { width: 390, height: 844 } } },
    { name: 'webkit-desktop', use: { browserName: 'webkit', viewport: { width: 1280, height: 800 } } },
    { name: 'webkit-mobile', use: { browserName: 'webkit', viewport: { width: 390, height: 844 } } },
  ],
});
