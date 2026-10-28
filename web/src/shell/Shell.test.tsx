import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import Shell from './Shell';

// jsdom shows both responsive navs at once (no breakpoints), so the
// sidebar and the tab bar carry different labels and tests scope to
// one of them.
function tabs() {
  return within(screen.getByRole('navigation', { name: 'Tabs' }));
}

describe('Shell', () => {
  it('renders the sidebar and tab bar pair', () => {
    render(<Shell username="admin" onSignedOut={vi.fn()} onSessionExpired={vi.fn()} />);
    expect(screen.getByRole('navigation', { name: 'Primary' })).toBeDefined();
    expect(screen.getByRole('navigation', { name: 'Tabs' })).toBeDefined();
  });

  it('opens on a calm Home and switches tabs', async () => {
    const user = userEvent.setup();
    window.fetch = vi.fn(async () => Response.json({ terminals: [] }, { status: 200 })) as typeof fetch;
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
});
