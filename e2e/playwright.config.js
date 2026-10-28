import { defineConfig } from '@playwright/test';

// Sign-in flows against a running stack (compose in CI, or a local
// `devcules serve`). The base URL and the bootstrap admin password
// come from the environment; screenshot baselines wait for the M1.2
// React UI, so this suite asserts flows and cookies only.
export default defineConfig({
  testDir: './tests',
  // Flows only: the visual suite has its own config (different
  // projects), so the default run must not collect it — otherwise a
  // missing baseline fails the flow job, and a present one gets
  // compared twice under the wrong project names.
  testMatch: 'signin.spec.js',
  timeout: 30_000,
  fullyParallel: true,
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:7659',
  },
  projects: [
    { name: 'chromium-desktop', use: { browserName: 'chromium', viewport: { width: 1280, height: 800 } } },
    { name: 'chromium-mobile', use: { browserName: 'chromium', viewport: { width: 390, height: 844 } } },
    // No WebKit: it drops Secure cookies on plain-http loopback, so
    // sign-in can never hold a session there (CI run 40656098122:
    // both WebKit projects failed with the cookie absent while
    // Chromium kept it). Revisit with real HTTPS or the M1.2 UI.
  ],
});
