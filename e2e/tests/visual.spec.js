import { test, expect } from '@playwright/test';

// Screenshot baselines: the sign-in screen and the Home shell, in
// dark and light (see playwright.visual.config.js). Baselines come
// from Linux only — Mac fonts differ — via the e2e-baselines
// workflow; never generate them on a Mac.
const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

test('sign-in screen', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
});

test('home shell', async ({ page }) => {
  // Same-context request: the session lands in the page's cookie jar.
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
});

test('terminals tab', async ({ page }) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await page.getByRole('button', { name: 'Terminals', exact: true }).first().click();
  await expect(page.getByRole('button', { name: 'New terminal' })).toBeVisible();
  // The agent list loads on its own: wait for every agent row so the
  // shot never races it. The rows' version text comes from live CLI
  // detection on the runner (muse --version and friends), which can
  // differ between stack runs (PATH timing on first boot), so the
  // detected-state line is masked rather than pinned to a string the
  // runner may not reproduce.
  const panel = page.getByRole('region', { name: 'Start an agent' });
  await expect(panel).toBeVisible();
  // The agent rows show the adapters' display names (Codex's is
  // "Muse", from its adapter).
  for (const name of ['Muse Code', 'Claude Code', 'Muse']) {
    await expect(panel.getByText(name, { exact: true })).toBeVisible();
  }
  await expect(page).toHaveScreenshot({
    animations: 'disabled',
    caret: 'hide',
    mask: [panel.locator('li')],
  });
});

test('settings', async ({ page }) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  await expect(page.getByRole('button', { name: 'Add a passkey' })).toBeVisible();
  await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
});
