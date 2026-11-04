import { useCallback, useEffect, useState } from 'react';
import { me } from './api';
import SignIn from './screens/SignIn';
import Shell from './shell/Shell';
import ToastHost from './shell/ToastHost';
import { useMobile, useMobileBodyClass } from './screens/useMobile';

type Session = 'checking' | 'signed-out' | { user: string };

export default function App() {
  const [session, setSession] = useState<Session>('checking');
  // The one JS flag for the whole mobile story: a coarse pointer, a
  // touch point, and a phone-sized edge. It drives the scroll lock and
  // the docked keyboard through the `devcules-mobile` body class.
  const mobile = useMobile();
  useMobileBodyClass(mobile);

  // A 401 from me() shows sign-in. So does an unreachable server:
  // the screen then says so on the next try, instead of leaving a
  // blank page. The effect only resolves the async check; the
  // synchronous reset lives in the sign-in handler below.
  const check = useCallback(() => {
    me().then(
      // Keep the same session object while the user is unchanged: a
      // fresh {user} on every visibility check re-renders the whole
      // shell and, with it, every open terminal pane.
      (user) =>
        setSession((prev) =>
          !user ? 'signed-out' : typeof prev === 'object' && prev.user === user ? prev : { user },
        ),
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

  // Any /api 401 lands here: the session is gone, so the shell
  // unmounts and sign-in shows, instead of stranding dead screens.
  // Stable, since screens (terminal panes among them) depend on it.
  const sessionExpired = useCallback(() => setSession('signed-out'), []);

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
  return (
    <>
      <Shell
        username={session.user}
        onSignedOut={() => setSession('signed-out')}
        onSessionExpired={sessionExpired}
      />
      {/* One toast host for every screen (Secrets, Settings, the Desk). */}
      <ToastHost />
    </>
  );
}
