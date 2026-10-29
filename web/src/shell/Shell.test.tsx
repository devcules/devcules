import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Shell from './Shell';

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

// route answers the approvals poll with pending (a list per call, the
// last repeating) and everything else with an empty terminal list.
function route(pending: unknown[][]): void {
  let n = 0;
  window.fetch = vi.fn(async (input: string | URL | Request) => {
    const url = input.toString();
    if (url.startsWith('/api/approvals?')) {
      const list = pending[Math.min(n++, pending.length - 1)];
      return Response.json({ approvals: list, pending: list.length }, { status: 200 });
    }
    return Response.json({ terminals: [] }, { status: 200 });
  }) as typeof fetch;
}

afterEach(() => {
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
    expect(screen.getByText('Nothing needs you.')).toBeDefined();
    await user.click(tabs().getByRole('button', { name: 'Terminals' }));
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
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
    expect(await screen.findByRole('heading', { name: '1 approval needs you' })).toBeDefined();
    expect(tabs().getByRole('button', { name: 'Home, 1 approval waiting' })).toBeDefined();
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
    const card = await screen.findByRole('listitem', { name: 'Recursive delete from Claude Code on laptop' });
    expect(card.className).toContain('border-2');
    delete (navigator as unknown as Record<string, unknown>).serviceWorker;
  });
});
