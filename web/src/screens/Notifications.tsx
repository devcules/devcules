import { useCallback, useEffect, useState } from 'react';
import { addPushDevice, getPush, removePushDevice, testPush, type PushDevice } from '../api';

// pushUsable reports whether this browser can take Web Push at all.
// iPhone Safari only offers it to the app added to the Home Screen.
export function pushUsable(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
}

// keyBytes decodes the server's base64url VAPID key.
export function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const b64 = b64url.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (b64url.length % 4)) % 4);
  const raw = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) {
    out[i] = raw.charCodeAt(i);
  }
  return out;
}

// deviceID matches the server's id for an endpoint (the first 8
// bytes of its SHA-256, hex), so the list can say "this device".
export async function deviceID(endpoint: string): Promise<string> {
  const sum = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(endpoint)));
  return Array.from(sum.slice(0, 8), (b) => b.toString(16).padStart(2, '0')).join('');
}

// guessLabel names this device for the list; the owner can change it.
export function guessLabel(ua: string): string {
  if (/iPhone/.test(ua)) return 'iPhone';
  if (/iPad/.test(ua)) return 'iPad';
  if (/Android/.test(ua)) return 'Android';
  if (/Mac OS X/.test(ua)) return 'Mac';
  if (/Windows/.test(ua)) return 'Windows';
  return 'This browser';
}

async function registration(): Promise<ServiceWorkerRegistration> {
  return (await navigator.serviceWorker.getRegistration('/')) ?? navigator.serviceWorker.register('/sw.js');
}

function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString();
}

// Notifications turns on Web Push for this device and lists the
// devices that get approval notices. A notice never carries the
// command: it says who wants to run what kind of thing, and opens
// the app on the approval.
export default function Notifications({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [key, setKey] = useState<string | null>(null);
  const [devices, setDevices] = useState<PushDevice[] | 'loading' | 'unreachable'>('loading');
  const [mine, setMine] = useState<string | null>(null);
  const [label, setLabel] = useState(() => guessLabel(navigator.userAgent));
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const usable = pushUsable();

  const refresh = useCallback((): Promise<void> => {
    return getPush().then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        if (res === 'unreachable') {
          setDevices('unreachable');
          return;
        }
        setKey(res.publicKey);
        setDevices(res.devices);
      },
      () => setDevices('unreachable'),
    );
  }, [onSessionExpired]);

  useEffect(() => {
    void refresh();
    if (!usable) {
      return;
    }
    let live = true;
    void navigator.serviceWorker
      .getRegistration('/')
      .then((reg) => reg?.pushManager.getSubscription())
      .then((sub) => (sub ? deviceID(sub.endpoint) : null))
      .then(
        (id) => {
          if (live) {
            setMine(id);
          }
        },
        () => {},
      );
    return () => {
      live = false;
    };
  }, [refresh, usable]);

  async function turnOn(): Promise<void> {
    if (busy || key === null) {
      return;
    }
    setBusy(true);
    setNotice(null);
    setStatus(null);
    try {
      const permission = await Notification.requestPermission();
      if (permission !== 'granted') {
        setNotice('Notifications are blocked for Devcules. Allow them in your browser or phone settings, then try again.');
        return;
      }
      const reg = await registration();
      // A subscription made under another server key can't be reused.
      await (await reg.pushManager.getSubscription())?.unsubscribe();
      const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(key) });
      const res = await addPushDevice(sub.toJSON(), label.trim() || guessLabel(navigator.userAgent));
      if (res === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (res === 'unreachable' || 'invalid' in res) {
        await sub.unsubscribe();
        setNotice(
          res !== 'unreachable' ? `Couldn't turn on notifications: ${res.invalid}.` : "Couldn't turn on notifications. Try again.",
        );
        return;
      }
      setMine(res.id);
      await refresh();
    } catch {
      setNotice("Couldn't turn on notifications on this device. Try again.");
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string): Promise<void> {
    setNotice(null);
    setStatus(null);
    const res = await removePushDevice(id);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res === 'unreachable') {
      setNotice("Couldn't remove that device. Try again.");
      return;
    }
    if (id === mine) {
      try {
        await (await navigator.serviceWorker.getRegistration('/'))?.pushManager.getSubscription().then((s) => s?.unsubscribe());
      } catch {
        // The server no longer sends here either way.
      }
      setMine(null);
    }
    await refresh();
  }

  async function test(): Promise<void> {
    setNotice(null);
    setStatus(null);
    const res = await testPush();
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res === 'unreachable' || 'invalid' in res) {
      setNotice("Couldn't send a test. Try again.");
      return;
    }
    const parts = [`Sent to ${res.sent} ${res.sent === 1 ? 'device' : 'devices'}`];
    if (res.failed > 0) parts.push(`${res.failed} failed`);
    if (res.removed > 0) parts.push(`${res.removed} no longer subscribed and removed`);
    setStatus(`${parts.join(', ')}.`);
    if (res.removed > 0) {
      await refresh();
    }
  }

  const list = Array.isArray(devices) ? devices : [];
  const subscribed = mine !== null && list.some((d) => d.id === mine);

  return (
    <section aria-label="Notifications" className="flex flex-col gap-3">
      <h3 className="text-body font-semibold text-text">Notifications</h3>
      <p className="text-body text-muted">
        Get a notice when an agent asks for approval. It names the agent and the kind of command, never the
        command itself.
      </p>
      {devices === 'loading' && <p className="text-body text-muted">Loading devices…</p>}
      {devices === 'unreachable' && (
        <p role="alert" className="text-body text-text">
          Couldn&apos;t load notification settings. Try again.
        </p>
      )}
      {Array.isArray(devices) && (
        <>
          {list.length === 0 ? (
            <p className="text-body text-muted">No devices get notices yet.</p>
          ) : (
            <ul aria-label="Notified devices" className="flex flex-col gap-2">
              {list.map((d) => (
                <li key={d.id} className="flex min-h-11 items-center gap-2 rounded-md border border-muted px-3 py-1">
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body text-text">
                      {d.label || d.service}
                      {d.id === mine && <span className="text-muted"> · this device</span>}
                    </span>
                    <span className="block truncate text-body text-muted">
                      {d.service} · added {when(d.created)}
                    </span>
                  </span>
                  <button
                    type="button"
                    aria-label={`Remove ${d.label || d.service}`}
                    onClick={() => void remove(d.id)}
                    className="min-h-11 px-2 text-body text-muted transition-colors duration-150"
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}
          {!usable && (
            <p className="text-body text-muted">
              This browser can&apos;t take notifications. On iPhone, add Devcules to your Home Screen (Share, Add
              to Home Screen) and open it from there.
            </p>
          )}
          {usable && !subscribed && key !== null && (
            <>
              <label htmlFor="push-label" className="text-body text-text">
                Name this device
              </label>
              <input
                id="push-label"
                value={label}
                maxLength={80}
                onChange={(e) => setLabel(e.target.value)}
                className="min-h-11 rounded-md border border-muted bg-surface px-2 text-body text-text"
              />
              <button
                type="button"
                onClick={() => void turnOn()}
                disabled={busy}
                className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150 disabled:opacity-60"
              >
                {busy ? 'Turning on…' : 'Turn on notifications here'}
              </button>
            </>
          )}
          {list.length > 0 && (
            <button
              type="button"
              onClick={() => void test()}
              className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150"
            >
              Send a test notice
            </button>
          )}
        </>
      )}
      {status && (
        <p role="status" className="text-body text-text">
          {status}
        </p>
      )}
      {notice && (
        <p role="alert" className="text-body text-text">
          {notice}
        </p>
      )}
    </section>
  );
}
