import { useCallback, useEffect, useState } from 'react';
import { listApprovals } from '../api';
import { Inbox, type PendingState } from '../screens/Approvals';
import Machines from '../screens/Machines';
import Projects, { type ProjectAsk, type ProjectIntent } from '../screens/Projects';
import ProjectTree from '../screens/ProjectTree';
import Terminals from '../screens/Terminals';
import Settings from '../screens/Settings';
import Activity from '../screens/Activity';

type Tab = 'home' | 'projects' | 'terminals' | 'machines' | 'activity' | 'settings';

const tabs: Array<{ id: Tab; label: string }> = [
  { id: 'home', label: 'Home' },
  { id: 'projects', label: 'Projects' },
  { id: 'terminals', label: 'Terminals' },
  { id: 'machines', label: 'Machines' },
  { id: 'activity', label: 'Activity' },
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
  const [pending, setPending] = useState<PendingState>('loading');
  const [focus, setFocus] = useState<string | null>(takeApprovalLink);
  // The sidebar's project tree asks the Projects tab to show things;
  // treeReload tells the tree the list changed.
  const [intent, setIntent] = useState<ProjectIntent | undefined>(undefined);
  const [treeReload, setTreeReload] = useState(0);
  const ask = useCallback((next: ProjectAsk) => {
    setIntent((prev) => ({ ...next, seq: (prev?.seq ?? 0) + 1 }));
    setTab('projects');
  }, []);

  // One poll feeds both the Home inbox and the tab badge. It runs
  // while the page is visible; coming back polls at once.
  const poll = useCallback(() => {
    void listApprovals('pending', 50).then(
      (res) => {
        if (res === 'unauthorized') {
          onSessionExpired();
          return;
        }
        setPending(res === 'unreachable' ? res : res.approvals);
      },
      () => setPending('unreachable'),
    );
  }, [onSessionExpired]);

  useEffect(() => {
    poll();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') {
        poll();
      }
    }, pollEvery);
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        poll();
      }
    };
    // A tapped notice with the app already open arrives from the
    // service worker instead of as a page load.
    const onMessage = (e: MessageEvent) => {
      const data: unknown = e.data;
      if (typeof data === 'object' && data !== null && 'approval' in data && typeof data.approval === 'string') {
        setFocus(data.approval);
        setTab('home');
        poll();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    navigator.serviceWorker?.addEventListener('message', onMessage);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
      navigator.serviceWorker?.removeEventListener('message', onMessage);
    };
  }, [poll]);

  const waiting = Array.isArray(pending) ? pending.length : 0;

  return (
    <div className="flex min-h-dvh bg-base text-text md:flex-row">
      {/* Desktop: sidebar. Phone: bottom bar below. */}
      <nav aria-label="Primary" className="hidden w-60 shrink-0 flex-col gap-1 overflow-y-auto p-4 md:flex">
        {tabs.map((t) => (
          <NavButton key={t.id} active={tab === t.id} onClick={() => setTab(t.id)}>
            {t.label}
            {t.id === 'home' && <Badge count={waiting} />}
          </NavButton>
        ))}
        {/* Projects with their terminals, under the tabs. */}
        <section aria-label="Project tree" className="mt-4 flex flex-col gap-1">
          <div className="flex items-center justify-between px-3">
            <h2 className="text-body font-semibold text-muted">Projects</h2>
            <button
              type="button"
              onClick={() => ask({ kind: 'new-project' })}
              aria-label="Add a project"
              className="min-h-9 rounded-md px-2 text-body text-text"
            >
              + Project
            </button>
          </div>
          <ProjectTree
            compact
            reload={treeReload}
            onSessionExpired={onSessionExpired}
            onOpen={(id, where) => ask({ kind: 'open', id, where })}
            onNewTerminal={(project) => ask({ kind: 'new-terminal', project })}
          />
        </section>
      </nav>
      {/* The standalone PWA draws under the iPhone status bar
          (black-translucent), so content starts below the inset. */}
      <main className="flex flex-1 flex-col pt-[env(safe-area-inset-top)] pb-20 md:pb-0">
        {tab === 'home' && (
          <Inbox pending={pending} focus={focus} onAnswered={poll} onSessionExpired={onSessionExpired} />
        )}
        {tab === 'projects' && (
          <Projects intent={intent} onChanged={() => setTreeReload((n) => n + 1)} onSessionExpired={onSessionExpired} />
        )}
        {tab === 'terminals' && <Terminals onSessionExpired={onSessionExpired} />}
        {tab === 'machines' && <Machines onSessionExpired={onSessionExpired} />}
        {tab === 'activity' && <Activity onSessionExpired={onSessionExpired} />}
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
            className={`flex min-h-11 min-w-0 flex-1 flex-col items-center justify-center px-0.5 text-tab transition-colors duration-150 ${
              tab === t.id ? 'text-text' : 'text-muted'
            }`}
          >
            {/* Six tabs share a phone's width: the labels sit a size
                below body text and truncate rather than run together. */}
            <span className="max-w-full truncate">
              {t.label}
              {t.id === 'home' && <Badge count={waiting} />}
            </span>
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

// Badge counts waiting approvals: the one place the nav uses the
// accent, because it means "needs you".
function Badge({ count }: { count: number }) {
  if (count === 0) {
    return null;
  }
  return (
    <>
      <span
        aria-hidden="true"
        className="ml-1.5 inline-flex min-w-5 items-center justify-center rounded-full bg-accent px-1.5 text-body font-semibold text-on-accent"
      >
        {count}
      </span>
      <span className="sr-only">{count === 1 ? ', 1 approval waiting' : `, ${count} approvals waiting`}</span>
    </>
  );
}

const pollEvery = 5000;

// takeApprovalLink reads ?approval=<id> (a tapped push notice opens
// the app there) and drops it from the address bar.
function takeApprovalLink(): string | null {
  const params = new URLSearchParams(window.location.search);
  const id = params.get('approval');
  if (id === null) {
    return null;
  }
  params.delete('approval');
  const rest = params.toString();
  window.history.replaceState(null, '', window.location.pathname + (rest ? `?${rest}` : '') + window.location.hash);
  return id;
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
