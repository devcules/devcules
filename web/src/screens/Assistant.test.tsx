import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { TerminalError, TerminalSummary } from '../api';

// The poll's answers, queued per call: a list or an error string.
const polls: Array<{ terminals: TerminalSummary[] } | TerminalError> = [];
vi.mock('../api', () => ({
  listTerminals: vi.fn(() => Promise.resolve(polls.length > 1 ? polls.shift() : polls[0])),
  listAgents: vi.fn(() => Promise.resolve({ agents: [] })),
  closeTerminal: vi.fn(),
  openTerminal: vi.fn(),
  startAgent: vi.fn(),
  renameTerminal: vi.fn(),
}));
vi.mock('./useRunners', () => ({
  useRunners: () => ({ state: 'ready', runners: ['mac'], host: 'mac' }),
}));
// The pane itself is not under test: count its mounts and unmounts.
const mounts = { up: 0, down: 0 };
vi.mock('./TerminalView', async () => {
  const { useEffect } = await import('react');
  return {
    default: function Pane({ id }: { id: string }) {
      useEffect(() => {
        mounts.up += 1;
        return () => {
          mounts.down += 1;
        };
      }, []);
      return <div aria-label="pane">{id}</div>;
    },
  };
});

import Assistant from './Assistant';

const term: TerminalSummary = { term: 'aaaaaaaaaaaaaaaa', created: 1, name: 'shell-a' };

beforeEach(() => {
  polls.length = 0;
  mounts.up = 0;
  mounts.down = 0;
});
afterEach(() => {
  vi.useRealTimers();
});

describe('Assistant', () => {
  it('keeps the open pane mounted through one failed poll', async () => {
    polls.push({ terminals: [term] }, 'unreachable', { terminals: [term] });
    const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    render(<Assistant onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'shell-a' }));
    expect(screen.getByLabelText('pane').textContent).toBe(term.term);
    expect(mounts).toEqual({ up: 1, down: 0 });

    // The 5 s poll answers 502 once.
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.getByRole('alert').textContent).toMatch(/Couldn't reach the server/);
    expect(screen.getByLabelText('pane')).toBeDefined();
    expect(mounts).toEqual({ up: 1, down: 0 });

    // The next good poll clears the alert; the pane never remounted.
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.queryByRole('alert')).toBeNull();
    expect(mounts).toEqual({ up: 1, down: 0 });
  });

  it('still reports an unreachable server when no list ever loaded', async () => {
    polls.push('unreachable');
    render(<Assistant onSessionExpired={vi.fn()} />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/Couldn't reach the server/);
  });
});
