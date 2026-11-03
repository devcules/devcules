import { test, expect } from '@playwright/test';

// Terminal flows against the compose stack (tmux lives in the
// runner). The terminal paints to canvas, so output assertions ride
// a second viewer: a raw WebSocket opened from the page itself,
// which shares the session cookie and the tmux session.
const password = process.env.E2E_ADMIN_PASSWORD;
if (!password) {
  throw new Error('E2E_ADMIN_PASSWORD is required: print it with scripts/get-admin-password.sh against an up stack');
}

const cspViolations = [];
async function watchCSP(page) {
  cspViolations.length = 0;
  page.on('console', (msg) => {
    if (msg.type() === 'error' && /csp|content security|refused/i.test(msg.text())) {
      cspViolations.push(msg.text());
    }
  });
}

async function signIn(page) {
  const res = await page.request.post('/auth/sign-in', { form: { username: 'admin', password } });
  expect(res.status()).toBe(204);
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Settings', exact: true }).first()).toBeVisible();
}

// Plain terminals open from the Assistant tab: its overview lists the
// live ones as a tab strip and offers a shell to start.
async function openAssistant(page) {
  await page.getByRole('button', { name: 'Assistant', exact: true }).first().click();
  await expect(page.getByRole('region', { name: 'Assistant', exact: true })).toBeVisible();
}

// newTerminal starts a shell and returns the id the server minted,
// read from the POST itself: both projects run in parallel against
// one stack, so the strip may hold other tests' terminals.
async function newTerminal(page) {
  // The URL carries the target link (?runner=<name>), since a serve with
  // more than one linked answers 400 to an untargeted call, so match on
  // the path and require the runner the form picked.
  const created = page.waitForResponse(
    (res) =>
      new URL(res.url()).pathname === '/api/terminals' &&
      new URL(res.url()).searchParams.get('runner') !== null &&
      res.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Start a shell' }).click();
  const res = await created;
  expect(res.status()).toBe(201);
  const { id } = await res.json();
  await expect(page.getByLabel('Terminal', { exact: true })).toBeVisible();
  // The view's own socket is open once its status line clears; keys
  // typed before that are dropped.
  await expect(page.getByRole('status')).toHaveCount(0);
  opened.push(id);
  return id;
}

// nameTerminal gives id a name no parallel test shares, so its tab in
// the strip can be found again (every shell starts as "assistant").
async function nameTerminal(page, id) {
  const name = `e2e-${id.slice(0, 8)}`;
  const res = await page.request.patch(`/api/terminals/${id}`, { data: { name } });
  expect(res.status()).toBe(204);
  return name;
}

// Every test closes what it opened, so reruns against the same stack
// never meet the 16-terminal cap.
const opened = [];
test.afterEach(async ({ page }) => {
  // page.request carries the signed-in cookie; the bare request
  // fixture does not.
  while (opened.length > 0) {
    await page.request.delete(`/api/terminals/${opened.pop()}`);
  }
});

// attachReader opens a raw second viewer on id from inside the page
// and returns a poller for its accumulated output.
async function attachReader(page, id) {
  await page.evaluate((term) => {
    window.__wsLog = [];
    window.__wsDone = false;
    const ws = new WebSocket(`/api/terminals/${term}/stream?cols=80&rows=24`);
    window.__ws = ws;
    ws.binaryType = 'arraybuffer';
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        return;
      }
      window.__wsLog.push(new TextDecoder().decode(ev.data));
    };
    ws.onclose = () => {
      window.__wsDone = true;
    };
  }, id);
  // Sends before the socket opens would throw: wait for it.
  await page.waitForFunction(() => window.__ws.readyState === WebSocket.OPEN);
  return async (want, timeout = 15000) => {
    await expect
      .poll(async () => page.evaluate(() => (window.__wsLog || []).join('')), { timeout })
      .toContain(want);
  };
}

async function terminalText(page) {
  return page.evaluate(() => (window.__wsLog || []).join(''));
}

test('new terminal, type echo, see the output', async ({ page }) => {
  await watchCSP(page);
  await signIn(page);
  await openAssistant(page);
  const id = await newTerminal(page);
  const waitFor = await attachReader(page, id);

  await page.evaluate((term) => window.__ws.send(new TextEncoder().encode('echo hi-$((40+2))\n')), id);
  await waitFor('hi-42');
  expect(await terminalText(page)).toContain('hi-42');
  // xterm.js paints with inline styles (allowed by style-src); our
  // bundle must never trip script-src. Every flow asserts this.
  expect(cspViolations).toEqual([]);
});

test('reload the page and the output is still there', async ({ page }) => {
  await watchCSP(page);
  await signIn(page);
  await openAssistant(page);
  const id = await newTerminal(page);
  const waitFor = await attachReader(page, id);
  await page.evaluate(() => window.__ws.send(new TextEncoder().encode('echo persist-$((20+22))\n')));
  await waitFor('persist-42');

  // A fresh attach redraws the scrollback from tmux: nothing of ours
  // is a replay buffer, the session is the buffer.
  await page.evaluate(() => window.__ws.close());
  const name = await nameTerminal(page, id);
  await page.reload();
  await expect(page.getByRole('button', { name: 'Settings', exact: true }).first()).toBeVisible();
  await openAssistant(page);
  await page.getByRole('button', { name, exact: true }).click();
  await expect(page.getByLabel('Terminal', { exact: true })).toBeVisible();
  const waitAgain = await attachReader(page, id);
  await waitAgain('persist-42');
  expect(cspViolations).toEqual([]);
});

test('the key strip sends Ctrl-C', async ({ page }) => {
  // The docked keyboard strip rides over any open pane; its Ctrl arms
  // a one-shot that turns the next typed letter into its control code.
  await watchCSP(page);
  await signIn(page);
  await openAssistant(page);
  const id = await newTerminal(page);
  const waitFor = await attachReader(page, id);

  // Focus the terminal so the system-keyboard path is live, then run
  // something worth interrupting.
  await page.getByLabel('Terminal', { exact: true }).click();
  await page.keyboard.type('sleep 30');
  await page.keyboard.press('Enter');
  await page.waitForTimeout(1500);

  // One-shot Ctrl plus a typed letter: the shell must get 0x03. The
  // xterm input regains focus without a click, which could disarm it.
  await page.getByRole('button', { name: 'Control (tap for one, hold to keep)' }).click();
  await page.locator('.xterm-helper-textarea').focus();
  await page.keyboard.press('c');
  await page.keyboard.type('echo done-$((20+22))');
  await page.keyboard.press('Enter');
  await waitFor('done-42');
  expect(await terminalText(page)).toContain('done-42');
  expect(cspViolations).toEqual([]);
});

test('close terminal forgets it', async ({ page }) => {
  await watchCSP(page);
  await signIn(page);
  await openAssistant(page);
  const id = await newTerminal(page);
  const name = await nameTerminal(page, id);
  await page.getByRole('button', { name: 'Back to terminals' }).click();
  await page.getByRole('button', { name: `Close ${name}` }).click();
  await expect(page.getByRole('button', { name, exact: true })).toHaveCount(0);

  const list = await page.request.get('/api/terminals');
  expect(list.status()).toBe(200);
  expect((await list.json()).terminals.map((t) => t.term)).not.toContain(id);
  expect(cspViolations).toEqual([]);
});
