import { useEffect, useRef, useState } from 'react';

// The header's user chip: the signed-in name, opening to a small menu
// with Sign out. It needs no data fetch — the same page already knows
// the username; the action posts /auth/sign-out and the shell's
// onSignedOut clears the session.
export default function UserMenu({ username, onSignedOut }: { username: string; onSignedOut: () => void }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent): void {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent): void {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  async function signOut(): Promise<void> {
    setOpen(false);
    try {
      await fetch('/auth/sign-out', { method: 'POST', credentials: 'same-origin' });
    } catch {
      // offline still swaps the local session; the cookie expires no
      // matter what, and a reconnect reauthenticates on its own.
    }
    onSignedOut();
  }

  return (
    <div ref={root} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        data-selected={open ? 'true' : undefined}
        className="i-control flex min-h-11 items-center gap-1.5 px-2 text-body text-text"
      >
        <span
          aria-hidden="true"
          className="flex h-6 w-6 items-center justify-center rounded-full bg-text text-base"
        >
          {(username.trim()[0] ?? '?').toUpperCase()}
        </span>
        <span className="max-w-28 truncate">{username}</span>
      </button>
      {open && (
        <div
          role="menu"
          aria-label={`Signed in as ${username}`}
          className="absolute right-0 z-20 mt-1 min-w-40 rounded-md border border-muted bg-surface py-1 shadow-lg"
        >
          <div className="px-3 py-1.5 text-body text-muted">Signed in as {username}</div>
          <button
            type="button"
            role="menuitem"
            onClick={() => void signOut()}
            className="flex min-h-11 w-full items-center px-3 text-left text-body text-text"
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
