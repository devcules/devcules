import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Shell from './Shell';
import { requestFs } from '../screens/keybus';

// jsdom shows both responsive navs at once (no breakpoints), so the
// sidebar and the tab bar carry different labels and tests scope to
// one of them.
function tabs() {
  return within(screen.getByRole('navigation', { name: 'Tabs' }));
}

const waiting = {
  id: 'a1',
  launchId: 'l1',
  runner: 'laptop',
  agent: 'Claude Code',
  tool: 'Bash',
  command: 'rm -rf build',
  rule: 'recursive-delete',
  ruleName: 'Recursive delete',
  irreversible: false,
  status: 'pending',
  created: '2026-10-29T08:00:00Z',
  expires: '2099-01-01T00:00:00Z',
};

// hostAnswer is what /api/host-info answers when a test asks for it,
// so the status bar has something to show.
const hostAnswer = {
  hostname: 'Mac.home',
  os: 'darwin',
  arch: 'arm64',
  cpuPercent: 30,
  cores: 8,
  memTotalBytes: 16 * 1024 ** 3,
  memUsedBytes: 8 * 1024 ** 3,
  memPercent: 50,
  collected: '2026-11-04T10:00:00Z',
};

// route answers the approvals poll with pending (a list per call, the
// last repeating) and everything else with an empty terminal list.
function route(pending: unknown[][], host = false): void {
  let n = 0;
  window.fetch = vi.fn(async (input: string | URL | Request) => {
    const url = input.toString();
    if (host && url.startsWith('/api/host-info')) {
      return Response.json(hostAnswer, { status: 200 });
    }
    if (url.startsWith('/api/approvals?')) {
      const list = pending[Math.min(n++, pending.length - 1)];
      return Response.json({ approvals: list, pending: list.length }, { status: 200 });
    }
    // The Extensions tab (or a Shell landing on it) polls the two fleet
    // routes and the two installed lists; all read-once arrays.
    if (url.startsWith('/api/mcp/fleet') || url.startsWith('/api/skills/fleet')
      || url.startsWith('/api/registry/mcp/installed') || url.startsWith('/api/registry/skill/installed')) {
      return Response.json([], { status: 200 });
    }
    return Response.json({ terminals: [] }, { status: 200 });
  }) as typeof fetch;
}

const phoneUndo: Array<() => void> = [];

afterEach(() => {
  phoneUndo.splice(0).forEach((undo) => undo());
  vi.unstubAllGlobals();
  vi.useRealTimers();
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('Shell', () => {
  it('renders the sidebar and tab bar pair', () => {
    route([[]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    expect(screen.getByRole('navigation', { name: 'Primary' })).toBeDefined();
    expect(screen.getByRole('navigation', { name: 'Tabs' })).toBeDefined();
  });

  it('opens on a calm Home and switches tabs', async () => {
    const user = userEvent.setup();
    route([[]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    await user.click(tabs().getByRole('button', { name: 'Home' }));
    expect(await screen.findByText('Nothing needs you.')).toBeDefined();
    await user.click(tabs().getByRole('button', { name: 'Machines' }));
    expect(await screen.findByRole('heading', { name: 'Machines' })).toBeDefined();
    await user.click(tabs().getByRole('button', { name: 'Settings' }));
    expect(screen.getByText('Signed in as')).toBeDefined();
  });

  it('signs out from Settings', async () => {
    const user = userEvent.setup();
    window.fetch = vi.fn(async () => new Response(null, { status: 200 })) as typeof fetch;
    const signedOut = vi.fn();
    render(<Shell username="admin" onSignedOut={signedOut} onSessionExpired={vi.fn()} />);
    await user.click(tabs().getByRole('button', { name: 'Settings' }));
    await user.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(signedOut).toHaveBeenCalled();
  });

  it('shows waiting approvals on Home with a badge, and polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    route([[waiting], []]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    (await tabs().findByRole('button', { name: 'Home, 1 approval waiting' })).click();
    expect(await screen.findByRole('heading', { name: '1 approval needs you' })).toBeDefined();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(await screen.findByText('Nothing needs you.')).toBeDefined();
    expect(tabs().getByRole('button', { name: 'Home' })).toBeDefined();
  });

  it('returns to sign-in when the poll gets a 401', async () => {
    window.fetch = vi.fn(async () => new Response(null, { status: 401 })) as typeof fetch;
    const expired = vi.fn();
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });

  it('opens the approval a notice linked to and cleans the address', async () => {
    window.history.replaceState(null, '', '/?approval=a1&x=1');
    Element.prototype.scrollIntoView = vi.fn();
    route([[waiting]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    const card = await screen.findByRole('listitem', { name: 'Recursive delete from Claude Code on laptop' });
    expect(card.className).toContain('border-2');
    expect(window.location.search).toBe('?x=1');
  });

  it('follows a tapped notice while open', async () => {
    const target = new EventTarget();
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: target });
    Element.prototype.scrollIntoView = vi.fn();
    const user = userEvent.setup();
    route([[waiting]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    await user.click(tabs().getByRole('button', { name: 'Settings' }));
    act(() => {
      target.dispatchEvent(new MessageEvent('message', { data: { approval: 'a1' } }));
    });
    // A tapped notice jumps to Home: the Inbox card is the focus, even
    // though the Desk is where the shell otherwise lands.
    const card = await screen.findByRole('listitem', { name: 'Recursive delete from Claude Code on laptop' });
    expect(card.className).toContain('border-2');
    delete (navigator as unknown as Record<string, unknown>).serviceWorker;
  });

  it('shows Extensions in the tabs and opens the registry screen', async () => {
    const user = userEvent.setup();
    route([[]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    // Both navs listing the tab is what puts it one click away on desktop
    // (sidebar) and phone (bottom bar) alike.
    expect(within(screen.getByRole('navigation', { name: 'Primary' })).getByRole('button', { name: 'Extensions' })).toBeDefined();
    await user.click(tabs().getByRole('button', { name: 'Extensions' }));
    expect(await screen.findByRole('heading', { name: 'Extensions' })).toBeDefined();
    expect(screen.getByRole('region', { name: 'Search the registries' })).toBeDefined();
  });

  it('drops the bottom tabs and header in fullscreen and brings them back', () => {
    phone(390, 844);
    route([[]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    const shell = document.querySelector('.v-shell') as HTMLElement;
    const header = document.querySelector('header') as HTMLElement;
    const bar = screen.getByRole('navigation', { name: 'Tabs' });
    expect(bar.className).toContain('flex');
    expect(header.className).toContain('flex');
    act(() => requestFs(true));
    expect(shell.className).toContain('v-shell-fs');
    expect(bar.className.split(/\s+/)).toContain('hidden');
    expect(header.className.split(/\s+/)).toContain('hidden');
    // Keyboard down in fullscreen: the bottom row keeps clear of the
    // home indicator, and the notch side insets stay on the shell.
    expect(shell.className).toContain('pb-[env(safe-area-inset-bottom)]');
    expect(shell.className).toContain('pl-[env(safe-area-inset-left)]');
    expect(shell.className).toContain('pr-[env(safe-area-inset-right)]');
    act(() => requestFs(false));
    expect(shell.className).not.toContain('v-shell-fs');
    expect(bar.className.split(/\s+/)).not.toContain('hidden');
    expect(header.className.split(/\s+/)).not.toContain('hidden');
  });

  it('keeps the phone chrome on a phone in landscape: no sidebar or status bar, tabs shown', async () => {
    phone(844, 390);
    route([[]], true);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    expect(screen.queryByRole('navigation', { name: 'Primary' })).toBeNull();
    expect(screen.queryByRole('separator', { name: 'Resize the sidebar' })).toBeNull();
    const bar = screen.getByRole('navigation', { name: 'Tabs' });
    // 844px is past md; the bar must not be hidden by the breakpoint.
    expect(bar.className).not.toContain('md:hidden');
    // Let a host-info poll land: the status bar still never mounts.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(screen.queryByRole('contentinfo', { name: 'Machine status' })).toBeNull();
    expect(screen.queryByText('MACHINE')).toBeNull();
  });

  it('keeps the desktop sidebar and status bar on a desktop', async () => {
    route([[]], true);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    expect(screen.getByRole('navigation', { name: 'Primary' })).toBeDefined();
    expect(screen.getByRole('navigation', { name: 'Tabs' }).className).toContain('md:hidden');
    expect(await screen.findByText('MACHINE')).toBeDefined();
  });

  it('scrolls the focused field into view when the keyboard shrinks the visible height', () => {
    const vv = phone(390, 844);
    route([[]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    const main = document.querySelector('main') as HTMLElement;
    const field = document.createElement('input');
    main.appendChild(field);
    field.focus();
    const reveal = vi.fn();
    field.scrollIntoView = reveal;
    act(() => vv.keyboard(336));
    expect(reveal).toHaveBeenCalledWith({ block: 'nearest' });
    // Keyboard going away (the height grows) does not scroll again.
    reveal.mockClear();
    act(() => vv.keyboard(0));
    expect(reveal).not.toHaveBeenCalled();
    main.removeChild(field);
  });

  it('leaves the terminal textarea alone when the keyboard comes up', () => {
    const vv = phone(390, 844);
    route([[]]);
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    const main = document.querySelector('main') as HTMLElement;
    const term = document.createElement('div');
    term.className = 'xterm';
    const ta = document.createElement('textarea');
    term.appendChild(ta);
    main.appendChild(term);
    ta.focus();
    const reveal = vi.fn();
    ta.scrollIntoView = reveal;
    act(() => vv.keyboard(336));
    expect(reveal).not.toHaveBeenCalled();
    main.removeChild(term);
  });
});

// phone makes useMobile read true (coarse pointer, a touch point, a
// phone-sized edge) at w x h and installs a visualViewport whose
// keyboard(px) shrinks the visible height by px (0 puts it away).
// The fakes are undone in afterEach.
function phone(w: number, h: number): { keyboard: (px: number) => void } {
  const target = new EventTarget();
  let height = h;
  const vv = {
    get height() { return height; },
    get width() { return w; },
    addEventListener: target.addEventListener.bind(target),
    removeEventListener: target.removeEventListener.bind(target),
  };
  vi.stubGlobal('innerWidth', w);
  vi.stubGlobal('innerHeight', h);
  vi.stubGlobal('visualViewport', vv);
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: q === '(pointer: coarse)',
    media: q,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  }));
  Object.defineProperty(navigator, 'maxTouchPoints', { configurable: true, value: 5 });
  phoneUndo.push(() => {
    delete (navigator as unknown as Record<string, unknown>).maxTouchPoints;
  });
  return {
    keyboard: (px) => {
      height = h - px;
      target.dispatchEvent(new Event('resize'));
    },
  };
}
