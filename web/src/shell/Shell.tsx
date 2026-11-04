import { useCallback, useEffect, useState } from 'react';
import { listApprovals } from '../api';
import { Inbox, type PendingState } from '../screens/Approvals';
import Machines from '../screens/Machines';
import Projects, { type ProjectAsk, type ProjectIntent } from '../screens/Projects';
import ProjectTree from '../screens/ProjectTree';
import Settings from '../screens/Settings';
import Activity from '../screens/Activity';
import Assistant from '../screens/Assistant';
import Extensions from '../screens/Extensions';
import DeskPage from '../screens/desk/DeskPage';
import { useMobile, useVisibleHeight } from '../screens/useMobile';
import ThemePicker from '../theme/ThemePicker';
import StatusBar from './StatusBar';
import UserMenu from './UserMenu';
import { SIDEBAR_MAX, SIDEBAR_MIN, useSidebarWidth } from './useSidebarWidth';

// The standalone Projects and Terminals pages are gone as tab
// destinations: both duplicate what lives alongside the nav — the
// project tree below (browse, fold, +, ×), and every terminal that
// already opens on Assistant or a project. The flows they hosted
// still exist: ask() below still lands project intents on the
// Projects host, and open tabs' state keeps "projects" in the union
// for that host.
type Tab = 'desk' | 'home' | 'projects' | 'terminals' | 'machines' | 'assistant' | 'extensions' | 'activity' | 'settings';

// Desk leads: the instrument panel is the landing tab,
// before the Home inbox.
const tabs: Array<{ id: Tab; label: string }> = [
  { id: 'desk', label: 'Desk' },
  { id: 'home', label: 'Home' },
  { id: 'projects', label: 'Projects' },
  { id: 'assistant', label: 'Assistant' },
  { id: 'machines', label: 'Machines' },
  { id: 'extensions', label: 'Extensions' },
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
  // The Desk is the landing tab — unless a push notice deep-linked a
  // specific approval, in which case Home (where that card lives) wins.
  const [initialApproval] = useState<string | null>(takeApprovalLink);
  const [tab, setTab] = useState<Tab>(() => (initialApproval ? 'home' : 'desk'));
  const [pending, setPending] = useState<PendingState>('loading');
  const [focus, setFocus] = useState<string | null>(initialApproval);
  // The desktop nav column's width: dragged on the handle beside it and
  // remembered per browser, since how much room the tree needs is
  // personal and does not belong in the shared desk document.
  const sidebar = useSidebarWidth();
  // Mobile-first run mode: while a terminal pane is focused the shell
  // chrome (header, sidebar, footer, bottom tabs) folds away and the
  // terminal takes the visible viewport. This is "fs"; the ☰
  // on the strip or the page's own back step drops back out. The pane
  // column is sized to the *visible* height so an open OS keyboard
  // pushes content instead of covering it.
  const [termFs, setTermFs] = useState(false);
  const mobile = useMobile();
  const visH = useVisibleHeight();
  const fs = mobile && termFs;
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

  if (fs) {
    // Mobile fullscreen terminal: the top/side chrome folds and the
    // pane column sizes to the *visible* height so an open OS keyboard
    // shrinks content instead of covering it (visualViewport). The
    // bottom tab bar STAYS on screen — it's how you leave the terminal
    // for another screen without first exiting — and it sits above the
    // safe area so a home-indicator phone keeps the taps reachable.
    const keyboardUp = window.innerHeight - visH > 120;
    return (
      <div
        className="flex flex-col bg-base text-text"
        style={{
          height: visH,
          paddingTop: 'env(safe-area-inset-top)',
          boxSizing: 'border-box',
        }}
      >
        <div className="flex min-h-0 flex-1 flex-col">
          <Assistant
            onSessionExpired={onSessionExpired}
            fs
            onExitFs={() => setTermFs(false)}
            onEnterFs={() => setTermFs(true)}
          />
        </div>
        {/* Static bottom bar, same one as normal mode: the user can hop
            to Home/Projects/etc. straight out of the terminal. Exiting
            fs happens on pick. No bottom safe-area pad while the OS
            keyboard is up — the keys cover that zone already. */}
        <nav
          aria-label="Tabs"
          className="shrink-0 border-t border-muted bg-surface"
          style={{ paddingBottom: keyboardUp ? 0 : 'env(safe-area-inset-bottom)' }}
        >
          <ul className="flex">
            {tabs.map((t) => (
              <li key={t.id} className="min-w-0 flex-1">
                <button
                  type="button"
                  aria-current={tab === t.id ? 'page' : undefined}
                  onClick={() => {
                    setTermFs(false);
                    setTab(t.id);
                  }}
                  className={`flex min-h-11 min-w-0 w-full flex-1 flex-col items-center justify-center px-0.5 text-tab transition-colors duration-150 ${
                    tab === t.id ? 'text-accent font-semibold' : 'text-muted'
                  }`}
                >
                  <span className="max-w-full truncate">
                    {t.label}
                    {t.id === 'home' && <Badge count={waiting} />}
                  </span>
                  <span
                    aria-hidden="true"
                    className={`mt-1 h-1 w-5 rounded-full ${tab === t.id ? 'bg-accent' : ''}`}
                  />
                </button>
              </li>
            ))}
          </ul>
        </nav>
      </div>
    );
  }

  // h-dvh, not min-h-dvh: the shell must be exactly one viewport tall so
  // the flex chain is *bounded*. With min-h the children were free to
  // grow, so the desk's own scroll box ended up 2822px tall inside a
  // 3104px page and could never scroll — and on mobile, where the
  // document is scroll-locked, nothing else could either. The fixed tab
  // bar overlays the bottom edge, so its strip is reserved here.
  return (
    <div className="flex h-dvh flex-col bg-base pb-[calc(2.75rem+env(safe-area-inset-bottom))] text-text md:pb-0">
      {/* Top bar: the brand and, on the right, the theme drop-down.
          The picker sits in the header so the theme is
          reachable from every screen; the pick persists per browser
          and applies at once. The safe-area top inset moves into the
          bar so the body's `pt` stays zero. */}
      <header className="sticky top-0 z-10 flex items-center justify-between gap-2 bg-base px-4 pt-[env(safe-area-inset-top)] pb-1">
        {/* The wordmark carries the accent: it's the app's purple (or
            rose, or green), so it stays readable on every background. */}
        <span className="text-lead font-semibold text-accent">devcules</span>
        <div className="flex items-center gap-1">
          <ThemePicker />
          <UserMenu username={username} onSignedOut={onSignedOut} />
        </div>
      </header>
      {/* min-h-0 matters as much as flex-1: a flex item's default
          min-height is auto, so without it this row grows to its content
          instead of shrinking to share the viewport with the header and
          status bar — which is what left everything below unscrollable. */}
      <div className="flex min-h-0 flex-1 md:flex-row">
        {/* Desktop: sidebar. Phone: bottom bar below. The width is the
            owner's (dragged on the handle beside it); a phone hides the
            column entirely, so the inline width only matters at md+. */}
        <nav
          aria-label="Primary"
          className="hidden w-60 shrink-0 flex-col gap-1 overflow-y-auto p-4 md:flex"
          style={{ width: `${sidebar.width}px` }}
        >
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
              className="min-h-11 rounded-md px-2 text-body text-text"
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
        {/* The drag handle: a thin strip between the column and the
            content. Keyboard users get the same range with the arrow
            keys, and a double-click restores the shipped width. */}
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize the sidebar"
          aria-valuenow={sidebar.width}
          aria-valuemin={SIDEBAR_MIN}
          aria-valuemax={SIDEBAR_MAX}
          tabIndex={0}
          onPointerDown={sidebar.onPointerDown}
          onDoubleClick={sidebar.reset}
          onKeyDown={(e) => {
            if (e.key === 'ArrowLeft') { e.preventDefault(); sidebar.nudge(-16); }
            if (e.key === 'ArrowRight') { e.preventDefault(); sidebar.nudge(16); }
          }}
          className={`hidden w-1 shrink-0 cursor-col-resize md:block ${sidebar.dragging ? 'bg-accent' : 'bg-transparent hover:bg-muted'}`}
        />
      {/* main is the one scrolling region for every screen except the
          terminal panes and the desk grid, which own their own. */}
      <main className="flex min-h-0 flex-1 flex-col overflow-y-auto overscroll-contain">
        {tab === 'desk' && (
          <DeskPage onSessionExpired={onSessionExpired} onOpenApprovals={() => setTab('home')} />
        )}
        {tab === 'home' && (
          <Inbox pending={pending} focus={focus} onAnswered={poll} onSessionExpired={onSessionExpired} />
        )}
        {tab === 'projects' && (
          <Projects intent={intent} onChanged={() => setTreeReload((n) => n + 1)} onSessionExpired={onSessionExpired} />
        )}
        {tab === 'machines' && <Machines onSessionExpired={onSessionExpired} />}
        {tab === 'assistant' && (
          <Assistant
            onSessionExpired={onSessionExpired}
            onEnterFs={mobile ? () => setTermFs(true) : undefined}
            onExitFs={() => setTermFs(false)}
          />
        )}
        {tab === 'extensions' && <Extensions onSessionExpired={onSessionExpired} />}
        {tab === 'activity' && <Activity onSessionExpired={onSessionExpired} />}
        {tab === 'settings' && (
          <Settings username={username} onSignedOut={onSignedOut} onSessionExpired={onSessionExpired} />
        )}
      </main>
      </div>
      {/* The footer strip: after the sidebar+main row, holding the
          machine line. Flow, not fixed, so content runs right down to
          it. Desktop only — on a phone it hides itself (md:block), which
          is why the reserved tab strip below no longer has to make room
          for it. */}
      <StatusBar username={username} />
      {/* Eight tabs do not fit 393px: at a fixed share each label got
          ~49px and half of them truncated mid-word ("Assista…",
          "Machin…"), which is what made the bar read as crowded. The
          strip pans sideways instead, each tab keeping its natural width
          so the labels stay whole. */}
      <nav
        aria-label="Tabs"
        className="d-tab-scroll fixed inset-x-0 bottom-0 flex overflow-x-auto bg-surface pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            aria-current={tab === t.id ? 'page' : undefined}
            onClick={() => setTab(t.id)}
            className={`flex min-h-11 shrink-0 flex-col items-center justify-center px-3 text-tab transition-colors duration-150 ${
              tab === t.id ? 'text-accent font-semibold' : 'text-muted'
            }`}
          >
            <span className="whitespace-nowrap">
              {t.label}
              {t.id === 'home' && <Badge count={waiting} />}
            </span>
            {/* The active tab: an accent dot under the label in the
                theme's hero colour. The dot and the label both use
                the accent now; it's the whole point of the mark. */}
            <span
              aria-hidden="true"
              className={`mt-0.5 h-1 w-6 rounded-full ${tab === t.id ? 'bg-accent' : 'bg-transparent'}`}
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
      data-selected={active ? 'true' : undefined}
      className="i-row min-h-11 px-3 text-left text-body text-muted data-[selected=true]:text-text"
    >
      {children}
    </button>
  );
}
