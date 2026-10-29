import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import SignIn from './SignIn';

// The page address check is mocked: jsdom always serves localhost.
const here = vi.hoisted(() => ({ usable: true }));
vi.mock('../webauthn', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../webauthn')>()),
  passkeysUsableHere: () => here.usable,
}));

function post(status: number): void {
  window.fetch = vi.fn(async () => new Response(null, { status })) as typeof fetch;
}

// route answers each fetch by path suffix, failing the test on an
// unexpected call.
function route(routes: Record<string, { status: number; body?: unknown }>): void {
  window.fetch = vi.fn(async (input: string | URL | Request) => {
    const url = input.toString();
    for (const [suffix, { status, body }] of Object.entries(routes)) {
      if (url.endsWith(suffix)) {
        return body === undefined ? new Response(null, { status }) : Response.json(body, { status });
      }
    }
    throw new Error(`unexpected fetch ${url}`);
  }) as typeof fetch;
}

const requestOptions = { challenge: 'Y2hhbGxlbmdl', rpId: 'localhost', userVerification: 'required' };

const fakeAssertion = {
  id: 'Y3JlZA',
  rawId: new Uint8Array([1]).buffer,
  type: 'public-key',
  authenticatorAttachment: 'platform',
  getClientExtensionResults: () => ({}),
  response: {
    clientDataJSON: new Uint8Array([2]).buffer,
    authenticatorData: new Uint8Array([3]).buffer,
    signature: new Uint8Array([4]).buffer,
    userHandle: new Uint8Array([5]).buffer,
  },
} as unknown as PublicKeyCredential;

function mockCredentials(methods: Partial<CredentialsContainer>): void {
  Object.defineProperty(window.navigator, 'credentials', {
    value: methods,
    configurable: true,
  });
}

afterEach(() => {
  here.usable = true;
  vi.unstubAllGlobals();
  Reflect.deleteProperty(window.navigator, 'credentials');
});

describe('SignIn', () => {
  it('signs in with Tab and Enter, no mouse', async () => {
    const user = userEvent.setup();
    const signedIn = vi.fn();
    post(204);
    render(<SignIn onSignedIn={signedIn} />);
    // autoFocus lands in username; Tab, password, Enter submits.
    await user.tab();
    await user.keyboard('admin');
    await user.tab();
    await user.keyboard('secret');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(signedIn).toHaveBeenCalled());
  });

  it('says the password was wrong on 401, never raw JSON', async () => {
    const user = userEvent.setup();
    post(401);
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.keyboard('admin{Tab}nope{Enter}');
    expect((await screen.findByRole('alert')).textContent).toBe('Wrong username or password.');
  });

  it('says to wait on 429', async () => {
    const user = userEvent.setup();
    post(429);
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.keyboard('admin{Tab}nope{Enter}');
    expect((await screen.findByRole('alert')).textContent).toBe('Too many tries. Wait a minute and try again.');
  });

  it('says the server is unreachable on 5xx, never blames the password', async () => {
    const user = userEvent.setup();
    post(503);
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.keyboard('admin{Tab}nope{Enter}');
    expect((await screen.findByRole('alert')).textContent).toBe(
      "Can't reach Devcules. Check your connection and try again.",
    );
  });

  it('shows the passkey button when enabled, with the accent on it', async () => {
    route({ '/auth/passkeys/status': { status: 200, body: { enabled: true } } });
    vi.stubGlobal('PublicKeyCredential', {});
    render(<SignIn onSignedIn={vi.fn()} />);
    const passkey = await screen.findByRole('button', { name: 'Sign in with a passkey' });
    expect(passkey.className).toContain('bg-accent');
    expect(screen.getByRole('button', { name: /^Sign in$/ }).className).not.toContain(
      'bg-accent',
    );
  });

  it('keeps the accent on Sign in when passkeys are off', async () => {
    route({ '/auth/passkeys/status': { status: 200, body: { enabled: false, reason: 'Off.' } } });
    vi.stubGlobal('PublicKeyCredential', {});
    render(<SignIn onSignedIn={vi.fn()} />);
    await waitFor(() => expect(window.fetch).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: 'Sign in with a passkey' })).toBeNull();
    expect(screen.getByRole('button', { name: /^Sign in$/ }).className).toContain(
      'bg-accent',
    );
  });

  it('hides the passkey button without WebAuthn', async () => {
    route({ '/auth/passkeys/status': { status: 200, body: { enabled: true } } });
    render(<SignIn onSignedIn={vi.fn()} />);
    await waitFor(() => expect(window.fetch).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: 'Sign in with a passkey' })).toBeNull();
  });

  it('signs in with a passkey', async () => {
    const user = userEvent.setup();
    const signedIn = vi.fn();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
      '/auth/passkeys/login/finish': { status: 204 },
    });
    vi.stubGlobal('PublicKeyCredential', {});
    mockCredentials({ get: vi.fn(async () => fakeAssertion) });
    render(<SignIn onSignedIn={signedIn} />);
    await user.click(await screen.findByRole('button', { name: 'Sign in with a passkey' }));
    await waitFor(() => expect(signedIn).toHaveBeenCalled());
  });

  it('says the passkey failed on 401, never raw JSON', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
      '/auth/passkeys/login/finish': { status: 401 },
    });
    vi.stubGlobal('PublicKeyCredential', {});
    mockCredentials({ get: vi.fn(async () => fakeAssertion) });
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Sign in with a passkey' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      "That passkey didn't work. Try again or use your password.",
    );
  });

  it('stays silent when the passkey ceremony is cancelled', async () => {
    const user = userEvent.setup();
    const signedIn = vi.fn();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
    });
    vi.stubGlobal('PublicKeyCredential', {});
    let rejectGet!: (reason?: unknown) => void;
    const get = vi.fn(
      () =>
        new Promise<Credential | null>((_, reject) => {
          rejectGet = reject;
        }),
    );
    mockCredentials({ get });
    render(<SignIn onSignedIn={signedIn} />);
    await user.click(await screen.findByRole('button', { name: 'Sign in with a passkey' }));
    await waitFor(() => expect(get).toHaveBeenCalled());
    await act(async () => {
      rejectGet(new DOMException('cancelled', 'NotAllowedError'));
    });
    expect(screen.queryByRole('alert')).toBeNull();
    expect(signedIn).not.toHaveBeenCalled();
  });

  it('says to wait when passkey logins are limited', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
      '/auth/passkeys/login/finish': { status: 429 },
    });
    vi.stubGlobal('PublicKeyCredential', {});
    mockCredentials({ get: vi.fn(async () => fakeAssertion) });
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Sign in with a passkey' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Too many tries. Wait a minute and try again.',
    );
  });

  it('shows the passkey error when begin fails', async () => {
    const user = userEvent.setup();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 400 },
    });
    vi.stubGlobal('PublicKeyCredential', {});
    mockCredentials({ get: vi.fn(async () => fakeAssertion) });
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Sign in with a passkey' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      "That passkey didn't work. Try again or use your password.",
    );
  });

  it('offers conditional mediation when the browser supports it', async () => {
    const user = userEvent.setup();
    const signedIn = vi.fn();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
      '/auth/passkeys/login/finish': { status: 204 },
    });
    vi.stubGlobal('PublicKeyCredential', {
      isConditionalMediationAvailable: async () => true,
    });
    const get: Mock<(opts?: CredentialRequestOptions) => Promise<Credential | null>> = vi.fn(
      async () => new Promise<Credential | null>(() => {}),
    );
    mockCredentials({ get });
    render(<SignIn onSignedIn={signedIn} />);
    await screen.findByRole('button', { name: 'Sign in with a passkey' });
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(screen.getByLabelText('Username').getAttribute('autocomplete')).toBe('username webauthn');
    expect(get.mock.calls[0][0]).toMatchObject({ mediation: 'conditional' });

    // A manual attempt aborts the quiet request and signs in modally.
    get.mockImplementationOnce(async () => fakeAssertion);
    await user.click(screen.getByRole('button', { name: 'Sign in with a passkey' }));
    await waitFor(() => expect(signedIn).toHaveBeenCalled());
    expect(get).toHaveBeenCalledTimes(2);
    expect(get.mock.calls[1][0]).not.toHaveProperty('mediation');
  });

  it('skips conditional mediation without browser support', async () => {
    route({ '/auth/passkeys/status': { status: 200, body: { enabled: true } } });
    vi.stubGlobal('PublicKeyCredential', {});
    const get = vi.fn(async () => fakeAssertion);
    mockCredentials({ get });
    render(<SignIn onSignedIn={vi.fn()} />);
    await screen.findByRole('button', { name: 'Sign in with a passkey' });
    await waitFor(() => expect(window.fetch).toHaveBeenCalled());
    expect(screen.getByLabelText('Username').getAttribute('autocomplete')).toBe('username');
    expect(get).not.toHaveBeenCalled();
  });

  it('skips conditional mediation when the browser declines', async () => {
    route({ '/auth/passkeys/status': { status: 200, body: { enabled: true } } });
    vi.stubGlobal('PublicKeyCredential', {
      isConditionalMediationAvailable: async () => false,
    });
    const get = vi.fn(async () => fakeAssertion);
    mockCredentials({ get });
    render(<SignIn onSignedIn={vi.fn()} />);
    await screen.findByRole('button', { name: 'Sign in with a passkey' });
    await waitFor(() =>
      expect(screen.getByLabelText('Username').getAttribute('autocomplete')).toBe('username'),
    );
    expect(get).not.toHaveBeenCalled();
  });
  it('hides the passkey button at an IP address', async () => {
    here.usable = false;
    route({ '/auth/passkeys/status': { status: 200, body: { enabled: true } } });
    vi.stubGlobal('PublicKeyCredential', { isConditionalMediationAvailable: async () => true });
    const get = vi.fn(async () => fakeAssertion);
    mockCredentials({ get });
    render(<SignIn onSignedIn={vi.fn()} />);
    await waitFor(() => expect(window.fetch).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole('button', { name: 'Sign in with a passkey' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Sign in' }).className).toContain('bg-accent');
    expect(get).not.toHaveBeenCalled();
  });

  it('says so when an autofill passkey is refused', async () => {
    const signedIn = vi.fn();
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
      '/auth/passkeys/login/finish': { status: 401 },
    });
    vi.stubGlobal('PublicKeyCredential', { isConditionalMediationAvailable: async () => true });
    mockCredentials({ get: vi.fn(async () => fakeAssertion) });
    render(<SignIn onSignedIn={signedIn} />);
    expect((await screen.findByRole('alert')).textContent).toBe(
      "That passkey didn't work. Try again or use your password.",
    );
    expect(signedIn).not.toHaveBeenCalled();
  });

  it('says to wait when an autofill passkey hits the limit', async () => {
    route({
      '/auth/passkeys/status': { status: 200, body: { enabled: true } },
      '/auth/passkeys/login/begin': { status: 200, body: requestOptions },
      '/auth/passkeys/login/finish': { status: 429 },
    });
    vi.stubGlobal('PublicKeyCredential', { isConditionalMediationAvailable: async () => true });
    mockCredentials({ get: vi.fn(async () => fakeAssertion) });
    render(<SignIn onSignedIn={vi.fn()} />);
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Too many tries. Wait a minute and try again.',
    );
  });
});
