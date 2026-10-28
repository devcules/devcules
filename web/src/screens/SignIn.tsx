import { useState } from 'react';
import { signIn } from '../api';

const errors = {
  invalid: 'Wrong username or password.',
  limited: 'Too many tries. Wait a minute and try again.',
  unreachable: "Can't reach Devcules. Check your connection and try again.",
} as const;

export default function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [failure, setFailure] = useState<keyof typeof errors | null>(null);
  const [busy, setBusy] = useState(false);

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
              autoComplete="username"
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
          <button
            type="submit"
            disabled={busy}
            className="min-h-11 rounded-md bg-accent text-body font-semibold text-on-accent transition-colors duration-150 disabled:opacity-60"
          >
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </main>
  );
}
