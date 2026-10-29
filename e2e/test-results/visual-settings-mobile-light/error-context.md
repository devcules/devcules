# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: visual.spec.js >> settings
- Location: e2e/tests/visual.spec.js:58:1

# Error details

```
Error: A snapshot doesn't exist at /Users/kiranchilledout/git/devcules/e2e/tests/visual.spec.js-snapshots/settings-1-mobile-light-darwin.png, writing actual.
```

# Page snapshot

```yaml
- generic [ref=e3]:
  - main [ref=e4]:
    - generic [ref=e5]:
      - heading "Settings" [level=2] [ref=e6]
      - paragraph [ref=e7]: Signed in as admin
      - region "Passkeys" [ref=e8]:
        - heading "Passkeys" [level=3] [ref=e9]
        - paragraph [ref=e10]: No passkeys yet.
        - button "Add a passkey" [ref=e11]
        - paragraph [ref=e12]: Your password always works, even with passkeys.
      - region "Redaction" [ref=e13]:
        - heading "Redaction" [level=3] [ref=e14]
        - paragraph [ref=e15]: Every model call is scanned first. Masked values reach the model as placeholders and come back in the reply. 221 secret rules are loaded.
        - list [ref=e16]:
          - listitem [ref=e17]:
            - generic [ref=e18]:
              - generic [ref=e19]: Secrets and keys
              - generic [ref=e20]: 0 found · 0 masked · 0 blocked
            - combobox "Secrets and keys mode" [ref=e21]:
              - option "Off"
              - option "Count only"
              - option "Mask" [selected]
              - option "Block"
          - listitem [ref=e22]:
            - generic [ref=e23]:
              - generic [ref=e24]: Email addresses
              - generic [ref=e25]: 0 found · 0 masked · 0 blocked
            - combobox "Email addresses mode" [ref=e26]:
              - option "Off" [selected]
              - option "Count only"
              - option "Mask"
              - option "Block"
          - listitem [ref=e27]:
            - generic [ref=e28]:
              - generic [ref=e29]: Phone numbers
              - generic [ref=e30]: 0 found · 0 masked · 0 blocked
            - combobox "Phone numbers mode" [ref=e31]:
              - option "Off" [selected]
              - option "Count only"
              - option "Mask"
              - option "Block"
          - listitem [ref=e32]:
            - generic [ref=e33]:
              - generic [ref=e34]: Card numbers
              - generic [ref=e35]: 0 found · 0 masked · 0 blocked
            - combobox "Card numbers mode" [ref=e36]:
              - option "Off" [selected]
              - option "Count only"
              - option "Mask"
              - option "Block"
          - listitem [ref=e37]:
            - generic [ref=e38]:
              - generic [ref=e39]: Public IP addresses
              - generic [ref=e40]: 0 found · 0 masked · 0 blocked
            - combobox "Public IP addresses mode" [ref=e41]:
              - option "Off" [selected]
              - option "Count only"
              - option "Mask"
              - option "Block"
        - generic [ref=e42]: Test text
        - textbox "Test text" [ref=e43]:
          - /placeholder: Paste text to see what a model would receive
        - button "Test" [disabled] [ref=e44]
      - region "Notifications" [ref=e45]:
        - heading "Notifications" [level=3] [ref=e46]
        - paragraph [ref=e47]: Get a notice when an agent asks for approval. It names the agent and the kind of command, never the command itself.
        - paragraph [ref=e48]: No devices get notices yet.
        - generic [ref=e49]: Name this device
        - textbox "Name this device" [ref=e50]: Mac
        - button "Turn on notifications here" [ref=e51]
      - region "Access modes" [ref=e52]:
        - heading "Access modes" [level=3] [ref=e53]
        - paragraph [ref=e54]: What agents may run on each runner without asking you. A runner with no mode blocks every call until you pick one. Unanswered approvals are denied after 10 minutes.
        - list [ref=e55]:
          - listitem [ref=e56]:
            - generic [ref=e57]: Agents without a runner
            - combobox "Agents without a runner access mode" [ref=e58]:
              - 'option "Not set: blocks every call" [disabled] [selected]'
              - 'option "Off: block every tool call"'
              - option "Ask before critical calls"
              - option "Ask before every call"
              - 'option "Full access: never ask"'
        - heading "Critical commands" [level=3] [ref=e59]
        - paragraph [ref=e60]: A command matching an enabled rule counts as critical. These rules catch honest mistakes; they are not a sandbox.
        - list "Critical rules" [ref=e61]:
          - listitem [ref=e62]:
            - generic [ref=e63]:
              - checkbox "Recursive delete" [checked] [ref=e64]
              - generic [ref=e65]: Recursive delete
          - listitem [ref=e67]:
            - generic [ref=e68]:
              - checkbox "Disk format" [checked] [ref=e69]
              - generic [ref=e70]: Disk format
          - listitem [ref=e72]:
            - generic [ref=e73]:
              - checkbox "dd to a device or file" [checked] [ref=e74]
              - generic [ref=e75]: dd to a device or file
          - listitem [ref=e77]:
            - generic [ref=e78]:
              - checkbox "Shutdown or reboot" [checked] [ref=e79]
              - generic [ref=e80]: Shutdown or reboot
          - listitem [ref=e82]:
            - generic [ref=e83]:
              - checkbox "Force push" [checked] [ref=e84]
              - generic [ref=e85]: Force push
          - listitem [ref=e87]:
            - generic [ref=e88]:
              - checkbox "git reset --hard" [checked] [ref=e89]
              - generic [ref=e90]: git reset --hard
          - listitem [ref=e92]:
            - generic [ref=e93]:
              - checkbox "Download piped to a shell" [checked] [ref=e94]
              - generic [ref=e95]: Download piped to a shell
          - listitem [ref=e97]:
            - generic [ref=e98]:
              - checkbox "Recursive chmod or chown" [checked] [ref=e99]
              - generic [ref=e100]: Recursive chmod or chown
          - listitem [ref=e102]:
            - generic [ref=e103]:
              - checkbox "DROP TABLE" [checked] [ref=e104]
              - generic [ref=e105]: DROP TABLE
          - listitem [ref=e107]:
            - generic [ref=e108]:
              - checkbox "crontab -r" [checked] [ref=e109]
              - generic [ref=e110]: crontab -r
          - listitem [ref=e112]:
            - generic [ref=e113]:
              - checkbox "sudo, su or runas" [checked] [ref=e114]
              - generic [ref=e115]: sudo, su or runas
        - generic [ref=e117]:
          - generic [ref=e118]: New rule name
          - textbox "New rule name" [ref=e119]:
            - /placeholder: Deploys to production
          - generic [ref=e120]: Pattern (a regular expression)
          - textbox "Pattern (a regular expression)" [ref=e121]:
            - /placeholder: kubectl .*--context[= ]prod
          - button "Add rule" [disabled] [ref=e122]
        - generic [ref=e123]: Try a command
        - textbox "Try a command" [ref=e124]:
          - /placeholder: git push --force origin main
        - button "Check" [disabled] [ref=e125]
      - button "Sign out" [ref=e126]
  - navigation "Tabs" [ref=e127]:
    - button "Home" [ref=e128]
    - button "Terminals" [ref=e131]
    - button "Activity" [ref=e134]
    - button "Settings" [active] [ref=e137]
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
  15 |   await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
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
> 67 |   await expect(page).toHaveScreenshot({ animations: 'disabled', caret: 'hide' });
     |   ^ Error: A snapshot doesn't exist at /Users/kiranchilledout/git/devcules/e2e/tests/visual.spec.js-snapshots/settings-1-mobile-light-darwin.png, writing actual.
  68 | });
  69 | 
```