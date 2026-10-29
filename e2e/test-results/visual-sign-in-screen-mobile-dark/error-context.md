# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: visual.spec.js >> sign-in screen
- Location: e2e/tests/visual.spec.js:12:1

# Error details

```
Error: A snapshot doesn't exist at /Users/kiranchilledout/git/devcules/e2e/tests/visual.spec.js-snapshots/sign-in-screen-1-mobile-dark-darwin.png, writing actual.
```

# Page snapshot

```yaml
- main [ref=e3]:
  - generic [ref=e4]:
    - heading "Sign in" [level=1] [ref=e5]
    - paragraph [ref=e6]: Your agents are waiting.
    - generic [ref=e7]:
      - generic [ref=e8]:
        - text: Username
        - textbox "Username" [active] [ref=e9]
      - generic [ref=e10]:
        - text: Password
        - textbox "Password" [ref=e11]
      - button "Sign in" [ref=e12]
      - button "Sign in with a passkey" [ref=e13]
```

# Test source

```ts
  1  | import { test, expect } from '@playwright/test';
  2  | 
  3  | // Screenshot baselines: the sign-in screen and the Home shell, in
  4  | // dark and light (see playwright.visual.config.js). Baselines come
  5  | // from Linux only — Mac fonts differ — via the e2e-baselines
  6  | // workflow; never generate them on a Mac.
  7  | const password = process.env.E2E_ADMIN_PASSWORD;
  8  | if (!password) {
  9  |   throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
  10 | }
  11 | 
  12 | test('sign-in screen', async ({ page }) => {
  13 |   await page.goto('/');
  14 |   await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
> 15 |   await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
     |   ^ Error: A snapshot doesn't exist at /Users/kiranchilledout/git/devcules/e2e/tests/visual.spec.js-snapshots/sign-in-screen-1-mobile-dark-darwin.png, writing actual.
  16 | });
  17 | 
  18 | test('home shell', async ({ page }) => {
  19 |   // Same-context request: the session lands in the page's cookie jar.
  20 |   const signedIn = await page.request.post('/auth/sign-in', {
  21 |     form: { username: 'admin', password },
  22 |   });
  23 |   expect(signedIn.status()).toBe(204);
  24 |   await page.goto('/');
  25 |   await expect(page.getByText('Nothing needs you.')).toBeVisible();
  26 |   await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
  27 | });
  28 | 
  29 | test('terminals tab', async ({ page }) => {
  30 |   const signedIn = await page.request.post('/auth/sign-in', {
  31 |     form: { username: 'admin', password },
  32 |   });
  33 |   expect(signedIn.status()).toBe(204);
  34 |   await page.goto('/');
  35 |   await expect(page.getByText('Nothing needs you.')).toBeVisible();
  36 |   await page.getByRole('button', { name: 'Terminals', exact: true }).first().click();
  37 |   await expect(page.getByRole('button', { name: 'New terminal' })).toBeVisible();
  38 |   // The agent list loads on its own: wait for every agent row so the
  39 |   // shot never races it. The rows' version text comes from live CLI
  40 |   // detection on the runner (muse --version and friends), which can
  41 |   // differ between stack runs (PATH timing on first boot), so the
  42 |   // detected-state line is masked rather than pinned to a string the
  43 |   // runner may not reproduce.
  44 |   const panel = page.getByRole('region', { name: 'Start an agent' });
  45 |   await expect(panel).toBeVisible();
  46 |   // The agent rows show the adapters' display names (Codex's is
  47 |   // "Muse", from its adapter).
  48 |   for (const name of ['Muse Code', 'Claude Code', 'Muse']) {
  49 |     await expect(panel.getByText(name, { exact: true })).toBeVisible();
  50 |   }
  51 |   await expect(page).toHaveScreenshot({
  52 |     animations: 'disabled',
  53 |     caret: 'hide',
  54 |     mask: [panel.locator('li')],
  55 |   });
  56 | });
  57 | 
  58 | test('settings', async ({ page }) => {
  59 |   const signedIn = await page.request.post('/auth/sign-in', {
  60 |     form: { username: 'admin', password },
  61 |   });
  62 |   expect(signedIn.status()).toBe(204);
  63 |   await page.goto('/');
  64 |   await expect(page.getByText('Nothing needs you.')).toBeVisible();
  65 |   await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  66 |   await expect(page.getByRole('button', { name: 'Add a passkey' })).toBeVisible();
  67 |   await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
  68 | });
  69 | 
```