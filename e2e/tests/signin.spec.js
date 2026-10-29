import { test, expect } from '@playwright/test';

const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

async function signIn(page, username, secret) {
  await page.goto('/');
  await page.getByLabel('Username').fill(username);
  await page.getByLabel('Password').fill(secret);
  const [response] = await Promise.all([
    page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/auth/sign-in')),
    // Exact: with passkeys enabled the screen also offers
    // "Sign in with a passkey".
    page.getByRole('button', { name: 'Sign in', exact: true }).click(),
  ]);
  return response;
}

test('sign-in success opens the shell', async ({ page }) => {
  const response = await signIn(page, 'admin', password);
  // 204: no redirect anymore, the React app takes over in place.
  expect(response.status()).toBe(204);
  await expect(page.getByRole('button', { name: 'Settings', exact: true }).first()).toBeVisible();

  // The browser kept the Secure cookie on plain-http loopback.
  const cookies = await page.context().cookies();
  const session = cookies.find((c) => c.name === 'devcules_session');
  expect(session).toBeDefined();
  expect(session.httpOnly).toBe(true);

  const me = await page.request.get('/auth/me');
  expect(me.status()).toBe(200);
  expect((await me.json()).username).toBe('admin');
});

test('wrong password stays out', async ({ page }) => {
  const response = await signIn(page, 'admin', `${password}x`);
  expect(response.status()).toBe(401);
  // Plain words, never raw JSON.
  await expect(page.getByText('Wrong username or password.')).toBeVisible();

  const cookies = await page.context().cookies();
  expect(cookies.filter((c) => c.name === 'devcules_session')).toHaveLength(0);

  const me = await page.request.get('/auth/me');
  expect(me.status()).toBe(401);
});

test('sign-out returns to sign-in', async ({ page }) => {
  const response = await signIn(page, 'admin', password);
  expect(response.status()).toBe(204);

  await page.getByRole('button', { name: 'Settings' }).click();
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();

  const cookies = await page.context().cookies();
  expect(cookies.filter((c) => c.name === 'devcules_session')).toHaveLength(0);

  const me = await page.request.get('/auth/me');
  expect(me.status()).toBe(401);
});

test('keyboard-only sign-in (Tab and Enter, no mouse)', async ({ page }) => {
  await page.goto('/');
  // Wait for the app to mount (keyboard does not auto-wait like
  // fill does), then prove the autofocus and Tab order explicitly.
  await expect(page.getByLabel('Username')).toBeVisible();
  await expect(page.getByLabel('Username')).toBeFocused();
  // No click anywhere in this test.
  await page.keyboard.type('admin');
  await page.keyboard.press('Tab');
  await expect(page.getByLabel('Password')).toBeFocused();
  await page.keyboard.type(password);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Settings', exact: true }).first()).toBeVisible();

  const me = await page.request.get('/auth/me');
  expect(me.status()).toBe(200);
});
