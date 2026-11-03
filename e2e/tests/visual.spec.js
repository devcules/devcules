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

test('settings', async ({ page }) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  // Every section starts folded: the shot is the list of headers.
  await expect(page.getByRole('button', { name: /^Passkeys/ })).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByRole('button', { name: /^Redaction/ })).toBeVisible();
  await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
});

test('machines tab', async ({ page }) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await page.getByRole('button', { name: 'Machines', exact: true }).first().click();
  // The stack enrols no computer, so the tab shows its empty state
  // and the Add a computer action.
  await expect(page.getByText('No computers yet.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add a computer' })).toBeVisible();
  await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
});

test('projects tab', async ({ page }) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await page.getByRole('button', { name: 'Projects', exact: true }).first().click();
  // On desktop the sidebar tree says it too; the tab's own empty state
  // is the one in main.
  await expect(page.getByRole('main').getByText('No projects yet.')).toBeVisible();
  await expect(page.getByRole('main').getByRole('button', { name: 'Add a project' })).toBeVisible();
  await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
});
