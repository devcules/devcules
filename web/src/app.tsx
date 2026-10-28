import { useCallback, useEffect, useState } from 'react';
import { me } from './api';
import SignIn from './screens/SignIn';
import Shell from './shell/Shell';

type Session = 'checking' | 'signed-out' | { user: string };

export default function App() {
  const [session, setSession] = useState<Session>('checking');

  // A 401 from me() shows sign-in. So does an unreachable server:
  // the screen then says so on the next try, instead of leaving a
  // blank page. The effect only resolves the async check; the
  // synchronous reset lives in the sign-in handler below.
  const check = useCallback(() => {
    me().then(
      (user) => setSession(user ? { user } : 'signed-out'),
      () => setSession('signed-out'),
    );
  }, []);

  useEffect(() => {
    check();
    // Locking the phone pauses everything; coming back re-checks the
    // session, so an expiry made while away shows sign-in at once.
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        check();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('online', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('online', onVisible);
    };
  }, [check]);

  function signedIn(): void {
    setSession('checking');
    check();
  }

  if (session === 'checking') {
    return <main className="min-h-dvh bg-base" aria-busy="true" />;
  }
  if (session === 'signed-out') {
    return <SignIn onSignedIn={signedIn} />;
  }
  // Any /api 401 lands here: the session is gone, so the shell
  // unmounts and sign-in shows, instead of stranding dead screens.
  function sessionExpired(): void {
    setSession('signed-out');
  }

  return (
    <Shell
      username={session.user}
      onSignedOut={() => setSession('signed-out')}
      onSessionExpired={sessionExpired}
    />
  );
}
