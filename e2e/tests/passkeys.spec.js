import { test, expect } from '@playwright/test';

// Passkey flows against a stack whose DEVCULES_PUBLIC_URL is
// http://localhost:7659 (see the e2e job): WebAuthn needs https or
// http://localhost, never 127.0.0.1, so this spec carries its own
// baseURL. A CDP virtual authenticator stands in for a phone.
test.use({ baseURL: 'http://localhost:7659' });

const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

async function virtualAuthenticator(page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
    },
  });
  return { cdp, authenticatorId };
}

async function signInWithPassword(page) {
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Settings', exact: true }).first()).toBeVisible();
}

test('passkey status is enabled on localhost', async ({ page }) => {
  const status = await page.request.get('/auth/passkeys/status');
  expect(status.status()).toBe(200);
  expect(await status.json()).toEqual({ enabled: true });
});

test('register, sign in, and remove a passkey', async ({ page }, testInfo) => {
  // One project only: both projects share the admin account, and two
  // parallel flows would register two keys both called "Passkey",
  // which no selector can tell apart. The status test above still
  // runs everywhere.
  test.skip(
    testInfo.project.name !== 'chromium-desktop',
    'shared account state runs once',
  );
  // Headless Chromium answers the quiet conditional (autofill)
  // request on its own, with no user pick, so it would race the
  // button and sign straight back in after sign-out. Turn the
  // autofill path off for this page: the button drives every
  // ceremony here, and Vitest covers the conditional path.
  await page.addInitScript(() => {
    if (window.PublicKeyCredential) {
      window.PublicKeyCredential.isConditionalMediationAvailable = async () => false;
    }
  });
  await virtualAuthenticator(page);

  // The sign-in screen offers passkeys.
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Sign in with a passkey' })).toBeVisible();

  // Sign in with the password, add a passkey in Settings.
  await signInWithPassword(page);
  await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  await expect(page.getByRole('button', { name: 'Add a passkey' })).toBeVisible();
  await page.getByRole('button', { name: 'Add a passkey' }).click();
  await expect(page.getByText('Passkey', { exact: true })).toBeVisible();

  // Sign out, sign in with the passkey.
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await page.getByRole('button', { name: 'Sign in with a passkey' }).click();
  await expect(page.getByRole('button', { name: 'Settings', exact: true }).first()).toBeVisible();

  // Remove it: passkey sign-in now fails, the password still works.
  await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  await page.getByRole('button', { name: 'Remove Passkey' }).click();
  await page.getByRole('button', { name: 'Confirm remove Passkey' }).click();
  await expect(page.getByText('No passkeys yet.')).toBeVisible();
  await page.getByRole('button', { name: 'Sign out' }).click();
  await page.getByRole('button', { name: 'Sign in with a passkey' }).click();
  await expect(
    page.getByText("That passkey didn't work. Try again or use your password."),
  ).toBeVisible();
  await signInWithPassword(page);
});
