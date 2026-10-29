import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import Notifications, { deviceID, guessLabel, keyBytes, pushUsable } from './Notifications';

type Reply = { status: number; body?: unknown } | 'reject';

function serve(replies: Record<string, Reply[]>): Mock {
  const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const queue = replies[key];
    if (!queue || queue.length === 0) {
      throw new Error(`unexpected fetch ${key}`);
    }
    const r = queue.length > 1 ? queue.shift()! : queue[0];
    if (r === 'reject') {
      throw new TypeError('network down');
    }
    return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

const endpoint = 'https://push.example.net/send/abc';
const publicKey = 'BAEC';

// A fake browser push stack: one registration whose subscription
// starts as `current` and is replaced by subscribe().
type FakeSub = { endpoint: string; unsubscribe: Mock; toJSON: () => PushSubscriptionJSON };
function fakeSub(ep: string): FakeSub {
  return {
    endpoint: ep,
    unsubscribe: vi.fn(async () => true),
    toJSON: () => ({ endpoint: ep, keys: { p256dh: 'pk', auth: 'au' } }),
  };
}

let current: FakeSub | null;
let subscribe: Mock;
let permission: NotificationPermission;

function installPush(): void {
  subscribe = vi.fn(async () => {
    current = fakeSub(endpoint);
    return current;
  });
  const reg = {
    pushManager: { getSubscription: vi.fn(async () => current), subscribe },
  };
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: { getRegistration: vi.fn(async () => reg), register: vi.fn(async () => reg) },
  });
  Object.defineProperty(window, 'PushManager', { configurable: true, value: function PushManager() {} });
  Object.defineProperty(window, 'Notification', {
    configurable: true,
    value: { requestPermission: vi.fn(async () => permission) },
  });
}

function removePush(): void {
  delete (navigator as unknown as Record<string, unknown>).serviceWorker;
  delete (window as unknown as Record<string, unknown>).PushManager;
  delete (window as unknown as Record<string, unknown>).Notification;
}

beforeEach(() => {
  current = null;
  permission = 'granted';
  installPush();
});

afterEach(() => {
  removePush();
  vi.restoreAllMocks();
});

describe('Notifications helpers', () => {
  it('decodes base64url keys', () => {
    expect(Array.from(keyBytes('BAEC'))).toEqual([4, 1, 2]);
    expect(Array.from(keyBytes('-_8'))).toEqual([251, 255]);
  });

  it('matches the server device id', async () => {
    // sha256("x")[:8] hex.
    expect(await deviceID('x')).toBe('2d711642b726b044');
  });

  it('names devices from the user agent', () => {
    expect(guessLabel('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)')).toBe('iPhone');
    expect(guessLabel('Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X)')).toBe('iPad');
    expect(guessLabel('Mozilla/5.0 (Linux; Android 15)')).toBe('Android');
    expect(guessLabel('Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0)')).toBe('Mac');
    expect(guessLabel('Mozilla/5.0 (Windows NT 10.0; Win64; x64)')).toBe('Windows');
    expect(guessLabel('curl/8')).toBe('This browser');
  });

  it('knows when push is missing', () => {
    expect(pushUsable()).toBe(true);
    removePush();
    expect(pushUsable()).toBe(false);
  });
});

describe('Notifications', () => {
  it('turns notifications on for this device', async () => {
    const fn = serve({
      'GET /api/push': [
        { status: 200, body: { publicKey, devices: [] } },
        {
          status: 200,
          body: { publicKey, devices: [{ id: await deviceID(endpoint), label: 'Work phone', service: 'push.example.net', created: '2026-10-29T08:00:00Z' }] },
        },
      ],
      'POST /api/push/devices': [{ status: 201, body: { id: await deviceID(endpoint) } }],
    });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    expect(await screen.findByText('No devices get notices yet.')).toBeTruthy();
    const name = screen.getByLabelText('Name this device');
    await user.clear(name);
    await user.type(name, 'Work phone');
    await user.click(screen.getByRole('button', { name: 'Turn on notifications here' }));
    const list = within(await screen.findByRole('list', { name: 'Notified devices' }));
    expect(list.getByText('· this device')).toBeTruthy();
    expect(subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) });
    const [, init] = fn.mock.calls.find(([u]) => u === '/api/push/devices') as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ endpoint, keys: { p256dh: 'pk', auth: 'au' }, label: 'Work phone' });
    expect(screen.queryByRole('button', { name: 'Turn on notifications here' })).toBeNull();
  });

  it('replaces an old subscription before subscribing', async () => {
    const old = fakeSub('https://push.example.net/send/old');
    current = old;
    serve({
      'GET /api/push': [{ status: 200, body: { publicKey, devices: [] } }],
      'POST /api/push/devices': [{ status: 201, body: { id: 'x' } }],
    });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'Turn on notifications here' }));
    await waitFor(() => expect(subscribe).toHaveBeenCalled());
    expect(old.unsubscribe).toHaveBeenCalled();
  });

  it('says when notifications are blocked', async () => {
    permission = 'denied';
    serve({ 'GET /api/push': [{ status: 200, body: { publicKey, devices: [] } }] });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'Turn on notifications here' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/^Notifications are blocked/);
    expect(subscribe).not.toHaveBeenCalled();
  });

  it('undoes the subscription when serve refuses it', async () => {
    serve({
      'GET /api/push': [{ status: 200, body: { publicKey, devices: [] } }],
      'POST /api/push/devices': [{ status: 400, body: { error: 'endpoint must be https' } }],
    });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'Turn on notifications here' }));
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't turn on notifications: endpoint must be https.");
    expect(current?.unsubscribe).toHaveBeenCalled();
  });

  it('says when the browser fails to subscribe', async () => {
    serve({ 'GET /api/push': [{ status: 200, body: { publicKey, devices: [] } }] });
    subscribe.mockRejectedValueOnce(new DOMException('no push service', 'AbortError'));
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'Turn on notifications here' }));
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't turn on notifications on this device. Try again.");
  });

  it('removes this device and unsubscribes it', async () => {
    current = fakeSub(endpoint);
    const id = await deviceID(endpoint);
    const sub = current;
    serve({
      'GET /api/push': [
        { status: 200, body: { publicKey, devices: [{ id, label: 'iPhone', service: 'web.push.apple.com', created: '2026-10-29T08:00:00Z' }] } },
        { status: 200, body: { publicKey, devices: [] } },
      ],
      [`DELETE /api/push/devices/${id}`]: [{ status: 204 }],
    });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    expect(await screen.findByText('· this device')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Remove iPhone' }));
    expect(await screen.findByText('No devices get notices yet.')).toBeTruthy();
    expect(sub.unsubscribe).toHaveBeenCalled();
  });

  it('says when a remove fails', async () => {
    serve({
      'GET /api/push': [{ status: 200, body: { publicKey, devices: [{ id: 'd1', label: '', service: 'fcm.googleapis.com', created: 'x' }] } }],
      'DELETE /api/push/devices/d1': [{ status: 500 }],
    });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'Remove fcm.googleapis.com' }));
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't remove that device. Try again.");
  });

  it('sends a test notice and reports what happened', async () => {
    serve({
      'GET /api/push': [{ status: 200, body: { publicKey, devices: [{ id: 'd1', label: 'Mac', service: 'push.example.net', created: '2026-10-29T08:00:00Z' }] } }],
      'POST /api/push/test': [
        { status: 200, body: { sent: 1, failed: 0, removed: 0 } },
        { status: 200, body: { sent: 0, failed: 1, removed: 1 } },
        'reject',
      ],
    });
    const user = userEvent.setup();
    render(<Notifications onSessionExpired={() => {}} />);
    await user.click(await screen.findByRole('button', { name: 'Send a test notice' }));
    expect((await screen.findByRole('status')).textContent).toBe('Sent to 1 device.');
    await user.click(screen.getByRole('button', { name: 'Send a test notice' }));
    await waitFor(() =>
      expect(screen.getByRole('status').textContent).toBe('Sent to 0 devices, 1 failed, 1 no longer subscribed and removed.'),
    );
    await user.click(screen.getByRole('button', { name: 'Send a test notice' }));
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't send a test. Try again.");
  });

  it('explains Home Screen install when push is missing', async () => {
    removePush();
    serve({ 'GET /api/push': [{ status: 200, body: { publicKey, devices: [] } }] });
    render(<Notifications onSessionExpired={() => {}} />);
    expect(await screen.findByText(/add Devcules to your Home Screen/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Turn on notifications here' })).toBeNull();
  });

  it('says when it cannot load', async () => {
    serve({ 'GET /api/push': [{ status: 503, body: { error: 'push not configured' } }] });
    render(<Notifications onSessionExpired={() => {}} />);
    expect((await screen.findByRole('alert')).textContent).toBe("Couldn't load notification settings. Try again.");
  });

  it('returns to sign-in on a 401', async () => {
    serve({ 'GET /api/push': [{ status: 401 }] });
    const expired = vi.fn();
    render(<Notifications onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
