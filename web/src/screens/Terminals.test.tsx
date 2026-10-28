import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import Terminals from './Terminals';

// The xterm view is mocked out: its rendering belongs to e2e, and
// jsdom has no layout for the fitter. What matters here is the
// list/new/close-confirm flow around it.
vi.mock('./TerminalView', () => ({
  default: ({ id, onBack }: { id: string; onBack: () => void }) => (
    <div>
      <span>terminal-view {id}</span>
      <button type="button" onClick={onBack}>
        Back to terminals
      </button>
    </div>
  ),
}));

function get(status: number, body: unknown = null): void {
  const response = body === null ? new Response(null, { status }) : Response.json(body, { status });
  window.fetch = vi.fn(async () => response) as typeof fetch;
}

const one = { terminals: [{ term: '0123456789abcdef', created: 1 }] };

describe('Terminals', () => {
  it('shows the calm empty state with no terminals', async () => {
    get(200, { terminals: [] });
    render(<Terminals onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
  });

  it('shows the offline state when no runner is linked', async () => {
    get(503);
    render(<Terminals onSessionExpired={vi.fn()} />);
    expect(await screen.findByText(/No runner is linked/)).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    get(401);
    const expired = vi.fn();
    render(<Terminals onSessionExpired={expired} />);
    await screen.findByText('No terminals yet.').catch(() => null);
    expect(expired).toHaveBeenCalled();
  });

  it('opens a new terminal and goes back to the list', async () => {
    const user = userEvent.setup();
    get(200, { terminals: [] });
    render(<Terminals onSessionExpired={vi.fn()} />);
    await screen.findByText('No terminals yet.');

    get(201, { id: '0123456789abcdef' });
    await user.click(screen.getByRole('button', { name: 'New terminal' }));
    expect(await screen.findByText('terminal-view 0123456789abcdef')).toBeDefined();

    get(200, { terminals: [] });
    await user.click(screen.getByRole('button', { name: 'Back to terminals' }));
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
  });

  it('closes a terminal only after confirming', async () => {
    const user = userEvent.setup();
    get(200, one);
    render(<Terminals onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Open terminal 01234567' }));

    // Opening is reversible from the list; closing asks first.
    get(200, one);
    await user.click(screen.getByRole('button', { name: 'Back to terminals' }));
    await user.click(await screen.findByRole('button', { name: 'Close terminal 01234567' }));
    expect(screen.getByRole('button', { name: 'Confirm close terminal 01234567' })).toBeDefined();

    get(204);
    get(200, { terminals: [] });
    await user.click(screen.getByRole('button', { name: 'Confirm close terminal 01234567' }));
    expect(await screen.findByText('No terminals yet.')).toBeDefined();
  });

  it('keeps the terminal when the close is cancelled', async () => {
    const user = userEvent.setup();
    get(200, one);
    render(<Terminals onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Open terminal 01234567' }));
    get(200, one);
    await user.click(screen.getByRole('button', { name: 'Back to terminals' }));
    await user.click(await screen.findByRole('button', { name: 'Close terminal 01234567' }));
    await user.click(screen.getByRole('button', { name: 'Keep terminal' }));
    expect(
      within(screen.getByRole('list')).getByRole('button', { name: 'Open terminal 01234567' }),
    ).toBeDefined();
  });
});
