import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

let user: string | null = 'admin';
vi.mock('./api', () => ({ me: vi.fn(() => Promise.resolve(user)) }));
vi.mock('./screens/SignIn', () => ({ default: () => <p>sign in</p> }));
// The shell stands in as a probe: what App handed it on each render.
const shellProps: Array<{ username: string; onSessionExpired: () => void }> = [];
vi.mock('./shell/Shell', () => ({
  default: (p: { username: string; onSessionExpired: () => void }) => {
    shellProps.push(p);
    return <p>shell {p.username}</p>;
  },
}));

import App from './app';
import { toast } from './lib/toast';

function comeBack(): void {
  document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(() => {
  user = 'admin';
  shellProps.length = 0;
});

describe('App session', () => {
  it('does not re-render the shell when a visibility re-check finds the same user', async () => {
    render(<App />);
    await screen.findByText('shell admin');
    const renders = shellProps.length;
    const expired = shellProps[renders - 1].onSessionExpired;
    await act(async () => {
      comeBack();
      await Promise.resolve();
    });
    await act(async () => {
      window.dispatchEvent(new Event('online'));
      await Promise.resolve();
    });
    expect(shellProps.length).toBe(renders);
    expect(shellProps[shellProps.length - 1].onSessionExpired).toBe(expired);
  });

  it('still routes to sign-in on a session expiry', async () => {
    render(<App />);
    await screen.findByText('shell admin');
    act(() => shellProps[shellProps.length - 1].onSessionExpired());
    expect(screen.getByText('sign in')).toBeDefined();
  });

  it('shows sign-in when the session lapsed while away', async () => {
    render(<App />);
    await screen.findByText('shell admin');
    user = null;
    await act(async () => {
      comeBack();
      await Promise.resolve();
    });
    expect(await screen.findByText('sign in')).toBeDefined();
  });

  it('re-renders the shell for a different user', async () => {
    render(<App />);
    await screen.findByText('shell admin');
    user = 'other';
    await act(async () => {
      comeBack();
      await Promise.resolve();
    });
    expect(await screen.findByText('shell other')).toBeDefined();
  });

  // DESK-15: the toast host sits beside the shell, so a toast raised on
  // any screen (the Shell here is a stub with no Desk) is seen.
  it('shows a toast on a signed-in screen with no Desk mounted', async () => {
    render(<App />);
    await screen.findByText('shell admin');
    act(() => { toast('Stored TAVILY_KEY'); });
    expect(screen.getByText('Stored TAVILY_KEY')).toBeDefined();
  });
});
