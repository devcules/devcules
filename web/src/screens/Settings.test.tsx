import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import Settings from './Settings';

// The page address check is mocked: jsdom always serves localhost.
const here = vi.hoisted(() => ({ usable: true }));
vi.mock('../webauthn', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../webauthn')>()),
  passkeysUsableHere: () => here.usable,
}));

// Redaction has its own tests and its own fetches; here it is inert.
vi.mock('./Redaction', () => ({ default: () => null }));
vi.mock('./Notifications', () => ({ default: () => null }));
vi.mock('./AccessModes', () => ({ default: () => null }));

type Planned = { status: number; body?: unknown } | Array<{ status: number; body?: unknown }>;

// route answers each fetch by path suffix; an array replays one
// response per call (the last repeats), so lists can change between
// refreshes.
function route(routes: Record<string, Planned>): void {
  const calls = new Map<string, number>();
  // Longest suffix first: /api/passkeys would otherwise swallow
  // /api/passkeys/{id}.
  const ordered = Object.entries(routes).sort((a, b) => b[0].length - a[0].length);
  window.fetch = vi.fn(async (input: string | URL | Request) => {
    const url = input.toString();
    for (const [suffix, planned] of ordered) {
      if (url.endsWith(suffix)) {
        const n = calls.get(suffix) ?? 0;
        calls.set(suffix, n + 1);
        const list = Array.isArray(planned) ? planned : [planned];
        const { status, body } = list[Math.min(n, list.length - 1)];
        return body === undefined ? new Response(null, { status }) : Response.json(body, { status });
      }
    }
    throw new Error(`unexpected fetch ${url}`);
  }) as typeof fetch;
}

const creationOptions = {
  challenge: 'Y2hhbGxlbmdl',
  rp: { id: 'localhost', name: 'Devcules' },
  user: { id: 'dXNlcg', name: 'admin', displayName: 'admin' },
  pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
  attestation: 'none',
};

const fakeCreation = {
  id: 'Y3JlZA',
  rawId: new Uint8Array([1]).buffer,
  type: 'public-key',
  authenticatorAttachment: 'platform',
  getClientExtensionResults: () => ({}),
  response: {
    clientDataJSON: new Uint8Array([2]).buffer,
    attestationObject: new Uint8Array([3]).buffer,
    getTransports: () => ['internal'],
  },
} as unknown as PublicKeyCredential;

const phone = { id: 'cGhvbmU', name: 'Phone', created: 1, lastUsed: 2, backedUp: true };
const laptop = { id: 'bGFwdG9w', name: 'Laptop', created: 3, lastUsed: 4, backedUp: false };

function mockCredentials(methods: Partial<CredentialsContainer>): void {
  Object.defineProperty(window.navigator, 'credentials', {
    value: methods,
    configurable: true,
  });
}

afterEach(() => {
  here.usable = true;
  Reflect.deleteProperty(window.navigator, 'credentials');
});

function renderSettings(expired = vi.fn()) {
  const signedOut = vi.fn();
  render(<Settings username="admin" onSignedOut={signedOut} onSessionExpired={expired} />);
  return { expired, signedOut };
}

describe('Settings', () => {
  it('lists passkeys with their backup state', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [phone, laptop] } },
    });
    renderSettings();
    expect(await screen.findByText('Phone')).toBeDefined();
    expect(screen.getByText('Laptop')).toBeDefined();
    expect(screen.getByText('Backed up')).toBeDefined();
    expect(screen.getByRole('button', { name: 'Add a passkey' })).toBeDefined();
    expect(screen.getByText('Your password always works, even with passkeys.')).toBeDefined();
  });

  it('says there are no passkeys yet', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [] } },
    });
    renderSettings();
    expect(await screen.findByText('No passkeys yet.')).toBeDefined();
  });

  it('shows the disabled reason instead of the Add button', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: false, reason: 'Set the URL.' } },
      '/api/passkeys': { status: 200, body: { passkeys: [] } },
    });
    renderSettings();
    expect(await screen.findByText('Set the URL.')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'Add a passkey' })).toBeNull();
  });

  it('explains passkeys at an IP address, keeping the list but not Add', async () => {
    here.usable = false;
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [phone] } },
    });
    renderSettings();
    expect(await screen.findByText('Phone')).toBeDefined();
    expect(screen.getByText(/Passkeys don.t work at an IP address/)).toBeDefined();
    expect(screen.queryByRole('button', { name: 'Add a passkey' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Remove Phone' })).toBeDefined();
  });

  it('keeps existing keys listed while passkeys are off', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: false, reason: 'Set the URL.' } },
      '/api/passkeys': { status: 200, body: { passkeys: [phone] } },
    });
    renderSettings();
    expect(await screen.findByText('Phone')).toBeDefined();
    expect(screen.getByRole('button', { name: 'Remove Phone' })).toBeDefined();
  });

  it('says the status is unreachable when the server is dead', async () => {
    window.fetch = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }) as typeof fetch;
    renderSettings();
    expect(await screen.findByRole('alert')).toBeDefined();
  });

  it('says the list failed to load, and routes 401 to sign-in', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 500 },
    });
    renderSettings();
    expect(await screen.findByRole('alert')).toBeDefined();
  });

  it('routes a 401 list to sign-in', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 401 },
    });
    const { expired } = renderSettings();
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });

  it('adds a passkey and refreshes the list', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': [
        { status: 200, body: { passkeys: [] } },
        { status: 200, body: { passkeys: [phone] } },
      ],
      '/auth/passkeys/register/begin': { status: 200, body: creationOptions },
      '/auth/passkeys/register/finish': { status: 201 },
    });
    const create: Mock<(opts?: CredentialCreationOptions) => Promise<Credential | null>> =
      vi.fn(async () => fakeCreation);
    mockCredentials({ create });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }));
    expect(await screen.findByText('Phone')).toBeDefined();
    expect(create).toHaveBeenCalledTimes(1);
    expect(create.mock.calls[0][0]).toHaveProperty('publicKey');
  });

  it('stays silent when adding is cancelled', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [] } },
      '/auth/passkeys/register/begin': { status: 200, body: creationOptions },
    });
    mockCredentials({
      create: vi.fn(async () => {
        throw new DOMException('cancelled', 'NotAllowedError');
      }),
    });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }));
    expect(await screen.findByText('No passkeys yet.')).toBeDefined();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('says adding failed when the finish is rejected', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [] } },
      '/auth/passkeys/register/begin': { status: 200, body: creationOptions },
      '/auth/passkeys/register/finish': { status: 400 },
    });
    mockCredentials({ create: vi.fn(async () => fakeCreation) });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      "Couldn't add that passkey. Try again.",
    );
  });

  it('routes an expired session mid-add to sign-in', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [] } },
      '/auth/passkeys/register/begin': { status: 401 },
    });
    mockCredentials({ create: vi.fn(async () => fakeCreation) });
    const { expired } = renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }));
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });

  it('renames a passkey', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': [
        { status: 200, body: { passkeys: [phone] } },
        { status: 200, body: { passkeys: [{ ...phone, name: 'Tablet' }] } },
      ],
      '/api/passkeys/cGhvbmU': { status: 204 },
    });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Rename Phone' }));
    const input = screen.getByLabelText('Passkey name');
    await user.clear(input);
    await user.type(input, 'Tablet');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Tablet')).toBeDefined();
    const patched = vi
      .mocked(window.fetch)
      .mock.calls.find(([input]) => input.toString().endsWith('/api/passkeys/cGhvbmU'));
    expect(patched?.[1]).toMatchObject({ method: 'PATCH' });
  });

  it('cancels a rename without saving', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [phone] } },
    });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Rename Phone' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByLabelText('Passkey name')).toBeNull();
    expect(screen.getByText('Phone')).toBeDefined();
  });

  it('refuses an empty rename', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [phone] } },
    });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Rename Phone' }));
    await user.clear(screen.getByLabelText('Passkey name'));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Give the passkey a name.');
  });

  it('removes a passkey only after confirming', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': [
        { status: 200, body: { passkeys: [phone] } },
        { status: 200, body: { passkeys: [] } },
      ],
      '/api/passkeys/cGhvbmU': { status: 204 },
    });
    renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Remove Phone' }));
    // Keep backs out: no DELETE, the key stays.
    await user.click(await screen.findByRole('button', { name: 'Keep' }));
    expect(screen.getByText('Phone')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'Confirm remove Phone' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Remove Phone' }));
    await user.click(await screen.findByRole('button', { name: 'Confirm remove Phone' }));
    expect(await screen.findByText('No passkeys yet.')).toBeDefined();
  });

  it('routes an expired session mid-remove to sign-in', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [phone] } },
      '/api/passkeys/cGhvbmU': { status: 401 },
    });
    const { expired } = renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Remove Phone' }));
    await user.click(await screen.findByRole('button', { name: 'Confirm remove Phone' }));
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });

  it('still signs out', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/api/passkeys': { status: 200, body: { passkeys: [] } },
      '/auth/sign-out': { status: 204 },
    });
    const { signedOut } = renderSettings();
    await user.click(await screen.findByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(signedOut).toHaveBeenCalled());
  });
});
