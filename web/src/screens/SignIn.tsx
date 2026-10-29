import { useCallback, useEffect, useRef, useState } from 'react';
import { beginPasskeyLogin, finishPasskeyLogin, passkeyStatus, signIn } from '../api';
import { credentialToJSON, passkeysUsableHere, toRequestOptions } from '../webauthn';

const errors = {
  invalid: 'Wrong username or password.',
  limited: 'Too many tries. Wait a minute and try again.',
  unreachable: "Can't reach Devcules. Check your connection and try again.",
  passkey: "That passkey didn't work. Try again or use your password.",
} as const;

// attemptPasskeyLogin runs one login ceremony: begin, get, finish.
// A user cancel reads 'cancelled' (the screen stays silent). A
// passkey the user picked that the server then refused reads
// 'rejected'; any failure before a passkey was picked reads 'failed'.
// It never throws.
async function attemptPasskeyLogin(
  signal?: AbortSignal,
): Promise<'ok' | 'failed' | 'rejected' | 'limited' | 'cancelled'> {
  const begun = await beginPasskeyLogin();
  if (typeof begun === 'string') {
    return 'failed';
  }
  let options: PublicKeyCredentialRequestOptions;
  try {
    options = toRequestOptions(begun.options);
  } catch {
    return 'failed';
  }
  const init: CredentialRequestOptions & { publicKey: PublicKeyCredentialRequestOptions } = {
    publicKey: options,
  };
  if (signal !== undefined) {
    init.mediation = 'conditional';
    init.signal = signal;
  }
  let cred: Credential | null;
  try {
    cred = await navigator.credentials.get(init);
  } catch (err) {
    if (err instanceof DOMException && err.name === 'NotAllowedError') {
      return 'cancelled';
    }
    return 'failed';
  }
  if (cred === null) {
    return 'failed';
  }
  const done = await finishPasskeyLogin(credentialToJSON(cred as PublicKeyCredential));
  if (done === 'ok') {
    return 'ok';
  }
  if (done === 'limited') {
    return 'limited';
  }
  return 'rejected';
}

export default function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [failure, setFailure] = useState<keyof typeof errors | null>(null);
  const [busy, setBusy] = useState(false);
  const [passkeys, setPasskeys] = useState(false);
  const [passkeyBusy, setPasskeyBusy] = useState(false);
  const [conditional, setConditional] = useState(false);
  const conditionalAbort = useRef<AbortController | null>(null);

  // The passkey button shows only when the server enables passkeys,
  // the browser speaks WebAuthn, and the page is not at an IP address
  // (WebAuthn refuses those, so the button could only ever fail).
  useEffect(() => {
    passkeyStatus().then(
      (status) => {
        if (
          status !== 'unreachable' &&
          status.enabled &&
          typeof window.PublicKeyCredential !== 'undefined' &&
          passkeysUsableHere()
        ) {
          setPasskeys(true);
        }
      },
      () => {},
    );
  }, []);

  // Conditional mediation: when the browser offers it, the username
  // field doubles as a passkey picker, backed by a quiet request.
  // It stays quiet until the user picks a passkey: only a refused
  // pick or the rate limit says so (the button then retries).
  const startConditional = useCallback(async (): Promise<void> => {
    const controller = new AbortController();
    conditionalAbort.current = controller;
    const outcome = await attemptPasskeyLogin(controller.signal);
    if (outcome === 'ok') {
      onSignedIn();
    } else if (outcome === 'rejected') {
      setFailure('passkey');
    } else if (outcome === 'limited') {
      setFailure('limited');
    }
  }, [onSignedIn]);

  useEffect(() => {
    if (!passkeys) {
      return undefined;
    }
    const pkb = window.PublicKeyCredential;
    if (typeof pkb?.isConditionalMediationAvailable !== 'function') {
      return undefined;
    }
    let live = true;
    pkb.isConditionalMediationAvailable().then(
      (ok) => {
        if (ok && live) {
          setConditional(true);
          void startConditional();
        }
      },
      () => {},
    );
    return () => {
      live = false;
      conditionalAbort.current?.abort();
    };
  }, [passkeys, startConditional]);

  async function submit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setFailure(null);
    try {
      const result = await signIn(username, password);
      if (result === 'ok') {
        onSignedIn();
      } else {
        setFailure(result);
      }
    } finally {
      setBusy(false);
    }
  }

  async function signInWithPasskey(): Promise<void> {
    if (passkeyBusy) {
      return;
    }
    // The modal request replaces the quiet conditional one; the
    // autofill offer returns afterwards.
    conditionalAbort.current?.abort();
    setPasskeyBusy(true);
    setFailure(null);
    const outcome = await attemptPasskeyLogin();
    setPasskeyBusy(false);
    if (outcome === 'ok') {
      onSignedIn();
      return;
    }
    if (outcome === 'limited') {
      setFailure('limited');
    } else if (outcome === 'failed' || outcome === 'rejected') {
      setFailure('passkey');
    }
    if (conditional) {
      void startConditional();
    }
  }

  // The accent stays on the one primary action: the passkey button
  // when passkeys are enabled, otherwise Sign in.
  const accent =
    'min-h-11 rounded-md bg-accent text-body font-semibold text-on-accent transition-colors duration-150 disabled:opacity-60';
  const secondary =
    'min-h-11 rounded-md border border-muted text-body text-text transition-colors duration-150 disabled:opacity-60';

  return (
    <main className="flex min-h-dvh items-center justify-center bg-base px-4 pt-[env(safe-area-inset-top)]">
      <div className="w-full max-w-xs">
        <h1 className="text-lead font-semibold text-text">Sign in</h1>
        <p className="mt-1 text-body text-muted">Your agents are waiting.</p>
        <form onSubmit={submit} className="mt-6 flex flex-col gap-4">
          <label className="flex flex-col gap-1 text-body text-text">
            Username
            <input
              autoFocus
              autoComplete={conditional ? 'username webauthn' : 'username'}
              className="min-h-11 rounded-md border border-muted bg-surface px-3 text-body text-text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
          <label className="flex flex-col gap-1 text-body text-text">
            Password
            <input
              type="password"
              autoComplete="current-password"
              className="min-h-11 rounded-md border border-muted bg-surface px-3 text-body text-text"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          {failure && (
            <p role="alert" className="text-body text-text">
              {errors[failure]}
            </p>
          )}
          <button type="submit" disabled={busy} className={passkeys ? secondary : accent}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
          {passkeys && (
            <button
              type="button"
              disabled={passkeyBusy}
              onClick={() => void signInWithPasskey()}
              className={accent}
            >
              {passkeyBusy ? 'Waiting for your passkey…' : 'Sign in with a passkey'}
            </button>
          )}
        </form>
      </div>
    </main>
  );
}
