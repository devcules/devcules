import { test, expect } from '@playwright/test';

const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

async function signIn(page, username, secret) {
  await page.goto('/sign-in');
  await page.getByLabel('Username').fill(username);
  await page.getByLabel('Password').fill(secret);
  const [response] = await Promise.all([
    page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/auth/sign-in')),
    page.getByRole('button', { name: 'Sign in' }).click(),
  ]);
  return response;
}

test('sign-in success opens a session', async ({ page }) => {
  const response = await signIn(page, 'admin', password);
  expect(response.status()).toBe(303);

  // The browser kept the Secure cookie on plain-http loopback: that is
  // the whole point of this assertion, and the empirical check behind
  // the WebKit project in the config.
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

  const cookies = await page.context().cookies();
  expect(cookies.filter((c) => c.name === 'devcules_session')).toHaveLength(0);

  const me = await page.request.get('/auth/me');
  expect(me.status()).toBe(401);
});
