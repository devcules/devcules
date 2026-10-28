import { useCallback, useEffect, useState } from 'react';
import { me } from './api';
import SignIn from './screens/SignIn';
import Shell from './shell/Shell';

type Session = 'checking' | 'signed-out' | { user: string };

export default function App() {
  const [session, setSession] = useState<Session>('checking');

  // Any 401 anywhere lands back here: me() returns null and the
  // sign-in screen takes over. The effect only resolves the async
  // check; the synchronous reset lives in the sign-in handler below.
  const check = useCallback(() => {
    me().then((user) => {
      setSession(user ? { user } : 'signed-out');
    });
  }, []);

  useEffect(() => {
    check();
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
  return <Shell username={session.user} onSignedOut={() => setSession('signed-out')} />;
}
