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
