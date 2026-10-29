import { test, expect } from '@playwright/test';

const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

// The Activity tab shows the audit log (this very sign-in is in it),
// checks the hash chain, and offers the JSON-lines export. Other specs
// run in parallel against the same stack, so counts are not exact.
test('activity shows the audit log and checks it', async ({ page }) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByText('Nothing needs you.')).toBeVisible();
  await page.getByRole('button', { name: 'Activity', exact: true }).first().click();

  const log = page.getByRole('region', { name: 'Audit log' });
  await expect(log.getByText('Signed in').first()).toBeVisible();
  await log.getByRole('button', { name: 'Check the log' }).click();
  await expect(log.getByRole('status')).toHaveText(/^All \d+ entries are intact\.$/);

  const exported = await page.request.get('/api/audit/export');
  expect(exported.status()).toBe(200);
  expect(exported.headers()['content-type']).toBe('application/x-ndjson');
  const lines = (await exported.text()).trim().split('\n').map((l) => JSON.parse(l));
  expect(lines.some((e) => e.action === 'auth.sign_in' && e.actor === 'admin')).toBe(true);
  expect(lines.some((e) => e.action === 'audit.export')).toBe(true);
  expect(lines.every((e, i) => i === 0 || e.prevHash === lines[i - 1].hash)).toBe(true);
});
