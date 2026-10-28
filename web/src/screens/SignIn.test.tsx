import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import SignIn from './SignIn';

function post(status: number): void {
  window.fetch = vi.fn(async () => new Response(null, { status })) as typeof fetch;
}

describe('SignIn', () => {
  it('signs in with Tab and Enter, no mouse', async () => {
    const user = userEvent.setup();
    const signedIn = vi.fn();
    post(204);
    render(<SignIn onSignedIn={signedIn} />);
    // autoFocus lands in username; Tab, password, Enter submits.
    await user.tab();
    await user.keyboard('admin');
    await user.tab();
    await user.keyboard('secret');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(signedIn).toHaveBeenCalled());
  });

  it('says the password was wrong on 401, never raw JSON', async () => {
    const user = userEvent.setup();
    post(401);
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.keyboard('admin{Tab}nope{Enter}');
    expect((await screen.findByRole('alert')).textContent).toBe('Wrong username or password.');
  });

  it('says to wait on 429', async () => {
    const user = userEvent.setup();
    post(429);
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.keyboard('admin{Tab}nope{Enter}');
    expect((await screen.findByRole('alert')).textContent).toBe('Too many tries. Wait a minute and try again.');
  });

  it('says the server is unreachable on 5xx, never blames the password', async () => {
    const user = userEvent.setup();
    post(503);
    render(<SignIn onSignedIn={vi.fn()} />);
    await user.keyboard('admin{Tab}nope{Enter}');
    expect((await screen.findByRole('alert')).textContent).toBe(
      "Can't reach Devcules. Check your connection and try again.",
    );
  });
});
