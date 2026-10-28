import { signOut } from '../api';

export default function Settings({ username, onSignedOut }: { username: string; onSignedOut: () => void }) {
  async function leave(): Promise<void> {
    await signOut();
    onSignedOut();
  }

  return (
    <div className="flex flex-1 flex-col gap-4 px-6 pt-8">
      <h2 className="text-lead font-semibold text-text">Settings</h2>
      <p className="text-body text-muted">
        Signed in as <span className="text-text">{username}</span>
      </p>
      <button
        type="button"
        onClick={leave}
        className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150"
      >
        Sign out
      </button>
    </div>
  );
}
