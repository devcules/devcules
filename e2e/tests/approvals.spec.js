import { test, expect } from '@playwright/test';

const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

// An agent's hook asks before a tool call; the owner allows it from
// the Home card and the hook gets the answer. Each project uses its
// own runner name, so the parallel runs never answer each other's
// card, and every approval is answered so Home ends calm.
test('an agent asks and the owner allows from Home', async ({ page, request }, testInfo) => {
  const signedIn = await page.request.post('/auth/sign-in', {
    form: { username: 'admin', password },
  });
  expect(signedIn.status()).toBe(204);

  const runner = `e2e-${testInfo.project.name}`;
  // Unique per run, so reruns against one stack never match old rows.
  const command = `echo ${runner} ${Date.now()}`;
  const mode = await page.request.put('/api/approvals/modes', { data: { target: `runner:${runner}`, mode: 'all' } });
  expect(mode.status()).toBe(200);
  const minted = await page.request.post('/api/launch-tokens', {
    data: { label: 'e2e approvals', runner, ttlMinutes: 10 },
  });
  expect(minted.status()).toBe(201);
  const { token, launch } = await minted.json();

  // The hook side: a bearer launch token on a request context with
  // no session cookie.
  const auth = { Authorization: `Bearer ${token}` };
  const asked = await request.post('/hook/approvals?wait=0', {
    headers: auth,
    data: { agent: 'e2e', tool: 'Bash', command },
  });
  expect(asked.status()).toBe(200);
  const ask = await asked.json();
  expect(ask.decision).toBe('ask');

  await page.goto('/');
  const card = page.getByRole('listitem', { name: `A tool call from e2e on ${runner}` });
  await expect(card.getByText(command)).toBeVisible();
  await card.getByRole('button', { name: 'Allow once' }).click();
  await expect(card).toHaveCount(0);

  const answered = await request.get(`/hook/approvals/${ask.approval.id}?wait=5`, { headers: auth });
  expect((await answered.json()).decision).toBe('allow');

  await page.getByRole('button', { name: 'Activity', exact: true }).first().click();
  const recent = page.getByRole('region', { name: 'Approvals' });
  await expect(recent.getByText(command)).toBeVisible();

  expect((await page.request.delete(`/api/launch-tokens/${launch.id}`)).status()).toBe(204);
});
