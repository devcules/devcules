import { useState } from 'react';
import { Home } from '../screens/Empty';
import Terminals from '../screens/Terminals';
import Settings from '../screens/Settings';

type Tab = 'home' | 'terminals' | 'settings';

const tabs: Array<{ id: Tab; label: string }> = [
  { id: 'home', label: 'Home' },
  { id: 'terminals', label: 'Terminals' },
  { id: 'settings', label: 'Settings' },
];

export default function Shell({
  username,
  onSignedOut,
  onSessionExpired,
}: {
  username: string;
  onSignedOut: () => void;
  onSessionExpired: () => void;
}) {
  const [tab, setTab] = useState<Tab>('home');

  return (
    <div className="flex min-h-dvh bg-base text-text md:flex-row">
      {/* Desktop: sidebar. Phone: bottom bar below. */}
      <nav aria-label="Primary" className="hidden w-48 flex-col gap-1 p-4 md:flex">
        {tabs.map((t) => (
          <NavButton key={t.id} active={tab === t.id} onClick={() => setTab(t.id)}>
            {t.label}
          </NavButton>
        ))}
      </nav>
      {/* The standalone PWA draws under the iPhone status bar
          (black-translucent), so content starts below the inset. */}
      <main className="flex flex-1 flex-col pt-[env(safe-area-inset-top)] pb-20 md:pb-0">
        {tab === 'home' && <Home />}
        {tab === 'terminals' && <Terminals onSessionExpired={onSessionExpired} />}
        {tab === 'settings' && (
          <Settings username={username} onSignedOut={onSignedOut} onSessionExpired={onSessionExpired} />
        )}
      </main>
      <nav
        aria-label="Tabs"
        className="fixed inset-x-0 bottom-0 flex bg-surface pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            aria-current={tab === t.id ? 'page' : undefined}
            onClick={() => setTab(t.id)}
            className={`flex min-h-11 flex-1 flex-col items-center justify-center text-body transition-colors duration-150 ${
              tab === t.id ? 'text-text' : 'text-muted'
            }`}
          >
            {t.label}
            {/* The active tab reads as text with an indicator, never
                the accent: the accent means primary actions and
                "needs you" only. */}
            <span
              aria-hidden="true"
              className={`mt-0.5 h-1 w-6 rounded-full ${tab === t.id ? 'bg-current' : 'bg-transparent'}`}
            />
          </button>
        ))}
      </nav>
    </div>
  );
}

function NavButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-current={active ? 'page' : undefined}
      onClick={onClick}
      className={`min-h-11 rounded-md px-3 text-left text-body transition-colors duration-150 ${
        active ? 'bg-surface text-text' : 'text-muted'
      }`}
    >
      {children}
    </button>
  );
}
