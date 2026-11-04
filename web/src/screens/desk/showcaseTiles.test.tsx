import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AuditEntry } from '../../api';

// The showcase tiles read the same API helpers as the rest of the app;
// here each helper answers from a plan the test sets.
const api = vi.hoisted(() => ({
  listTerminals: vi.fn(),
  listMachines: vi.fn(),
  listApprovals: vi.fn(),
  listProviders: vi.fn(),
  listAudit: vi.fn(),
}));
vi.mock('../../api', () => api);

import { FeedTile, PulseTile, ShipTile, VibeTile } from './showcaseTiles';

const widget = { id: 'w1', type: 'vibe' };
const base = { widget, syncKey: 0, arranging: false };

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const entry = (seq: number, msAgo: number, action = 'auth.login', target?: string): AuditEntry => ({
  seq,
  at: ago(msAgo),
  actor: 'admin',
  action,
  ...(target ? { target } : {}),
});

function healthy(over: Partial<Record<keyof typeof api, unknown>> = {}) {
  const plan: Record<keyof typeof api, unknown> = {
    listTerminals: { terminals: [{ id: 't1' }, { id: 't2' }] },
    listMachines: { machines: [{ online: true }, { online: false }, { online: true }] },
    listApprovals: { approvals: [], pending: 0 },
    listProviders: [{ id: 'a', ready: true }, { id: 'b', ready: false }],
    listAudit: { entries: [entry(1, 30_000, 'terminal.open', 'shell'), entry(2, 120_000, 'custom.thing_done')] },
    ...over,
  };
  for (const k of Object.keys(api) as (keyof typeof api)[]) {
    api[k].mockReset();
    api[k].mockResolvedValue(plan[k]);
  }
}

const led = (title: string) =>
  screen.getByRole('region', { name: title }).querySelector('.d-led')?.className ?? '';

beforeEach(() => healthy());
afterEach(() => {
  vi.useRealTimers();
});

describe('VibeTile', () => {
  it('shows the live headline numbers', async () => {
    render(<VibeTile {...base} />);
    expect(await screen.findByText('agents are cooking 🔥')).toBeTruthy();
    expect(screen.getByText('2').nextElementSibling?.textContent).toBe('terminals live');
    expect(screen.getByText('2/3')).toBeTruthy();
    expect(screen.getByText('1/2')).toBeTruthy();
    expect(led('vibe check')).toContain('d-led-live');
    // the marquee names the events, with known verbs and fallbacks
    expect(screen.getByText(/spun up a terminal · shell/)).toBeTruthy();
    expect(screen.getByText(/custom thing done/)).toBeTruthy();
  });

  it('says calm with nothing running and counts pending approvals', async () => {
    healthy({ listTerminals: { terminals: [] }, listAudit: { entries: [] } });
    const { unmount } = render(<VibeTile {...base} />);
    expect(await screen.findByText('calm. suspiciously calm. 🧘')).toBeTruthy();
    unmount();

    healthy({ listApprovals: { approvals: [], pending: 1 } });
    const open = vi.fn();
    render(<VibeTile {...base} onOpenApprovals={open} />);
    expect(await screen.findByText('1 thing need your thumbs 👀')).toBeTruthy();
    fireEvent.click(screen.getByText('approvals waiting').closest('button')!);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('pluralises several pending approvals', async () => {
    healthy({ listApprovals: { approvals: [], pending: 3 } });
    render(<VibeTile {...base} />);
    expect(await screen.findByText('3 things need your thumbs 👀')).toBeTruthy();
  });

  // DESK-18: an unreachable server is an error, never "0 terminals live".
  it('shows an error, not zeros, when the server is unreachable', async () => {
    healthy({ listTerminals: 'unreachable', listMachines: 'unreachable', listApprovals: 'unreachable', listProviders: 'unreachable', listAudit: 'unreachable' });
    render(<VibeTile {...base} />);
    expect(await screen.findByText("can't reach the server")).toBeTruthy();
    expect(led('vibe check')).toContain('d-led-error');
    expect(screen.queryByText('calm. suspiciously calm. 🧘')).toBeNull();
    expect(screen.queryByText('0')).toBeNull();
    expect(screen.getAllByText('–')).toHaveLength(4);
  });

  it('an unlinked runner (offline) is a real zero, not an error', async () => {
    healthy({ listTerminals: 'offline', listMachines: 'offline' });
    render(<VibeTile {...base} />);
    expect(await screen.findByText('0/0')).toBeTruthy();
    expect(led('vibe check')).toContain('d-led-live');
    expect(screen.queryByText(/can't reach the server/)).toBeNull();
  });

  it('keeps the last good numbers when a later poll fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    render(<VibeTile {...base} />);
    expect(await screen.findByText('2/3')).toBeTruthy();
    healthy({ listTerminals: 'unreachable' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });
    expect(await screen.findByText("can't reach the server · last known numbers")).toBeTruthy();
    expect(screen.getByText('2/3')).toBeTruthy();
    expect(led('vibe check')).toContain('d-led-error');
    // and recovers once the server answers again
    healthy();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });
    await waitFor(() => expect(led('vibe check')).toContain('d-led-live'));
  });

  it('hands an expired session to onUnauthorized', async () => {
    healthy({ listMachines: 'unauthorized' });
    const expired = vi.fn();
    render(<VibeTile {...base} onUnauthorized={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
    expect(screen.getByText('warming up…')).toBeTruthy();
    expect(led('vibe check')).toContain('d-led-loading');
  });
});

describe('FeedTile', () => {
  it('lists the audit chain with ages', async () => {
    healthy({ listAudit: { entries: [entry(1, 5_000, 'approval.deny', 'rm -rf'), entry(2, 2 * 3600_000), entry(3, 3 * 86400_000), entry(4, 0, 'x', undefined)] } });
    render(<FeedTile {...base} />);
    expect(await screen.findByText('blocked a risky step')).toBeTruthy();
    expect(screen.getByText('rm -rf')).toBeTruthy();
    expect(screen.getByText('2h')).toBeTruthy();
    expect(screen.getByText('3d')).toBeTruthy();
    expect(led('agent feed')).toContain('d-led-live');
  });

  it('says so when the chain is empty', async () => {
    healthy({ listAudit: { entries: [] } });
    render(<FeedTile {...base} />);
    expect(await screen.findByText(/no tea yet/)).toBeTruthy();
  });

  it('shows an error, not an empty feed, when unreachable', async () => {
    healthy({ listAudit: 'unreachable' });
    render(<FeedTile {...base} />);
    expect((await screen.findByRole('alert')).textContent).toBe("can't reach the server");
    expect(screen.queryByText(/no tea yet/)).toBeNull();
    expect(led('agent feed')).toContain('d-led-error');
  });

  it('keeps the last rows and tells screen readers they are stale when a later poll fails', async () => {
    const { rerender } = render(<FeedTile {...base} />);
    expect(await screen.findByText('shell')).toBeTruthy();
    expect(screen.queryByText(/showing the last known feed/)).toBeNull();
    healthy({ listAudit: 'unreachable' });
    rerender(<FeedTile {...base} syncKey={1} />);
    const note = await screen.findByText("can't reach the server · showing the last known feed");
    expect(note.getAttribute('aria-live')).toBe('polite');
    expect(note.className).toContain('sr-only');
    expect(screen.getByText('shell')).toBeTruthy();
    healthy();
    rerender(<FeedTile {...base} syncKey={2} />);
    await waitFor(() => expect(screen.queryByText(/showing the last known feed/)).toBeNull());
  });

  it('hands an expired session to onUnauthorized', async () => {
    healthy({ listAudit: 'unauthorized' });
    const expired = vi.fn();
    render(<FeedTile {...base} onUnauthorized={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
    expect(screen.getByText('tuning in…')).toBeTruthy();
  });
});

describe('PulseTile', () => {
  // DESK-23: the buckets are a rolling 24 h, so the label says so.
  it('counts a rolling 24 hours and labels it that way', async () => {
    healthy({ listAudit: { entries: [entry(1, 60_000), entry(2, 90_000), entry(3, 5 * 3600_000), entry(4, 30 * 3600_000), { seq: 5, at: 'nope', actor: 'a', action: 'b' }] } });
    render(<PulseTile {...base} />);
    expect(await screen.findByText('events · 24h')).toBeTruthy();
    expect(screen.queryByText('events today')).toBeNull();
    expect(screen.getByRole('img', { name: '3 events in the last 24 hours' })).toBeTruthy();
    expect(screen.getByText(/^peak /)).toBeTruthy();
    expect(screen.getByTitle('2 events, 0h ago')).toBeTruthy();
    expect(screen.getByTitle('1 event, 5h ago')).toBeTruthy();
    expect(led('pulse · 24h')).toContain('d-led-live');
  });

  it('reads a second page when the first one is full', async () => {
    const page = Array.from({ length: 50 }, (_, i) => entry(100 - i, 60_000));
    api.listAudit.mockReset();
    api.listAudit.mockResolvedValueOnce({ entries: page }).mockResolvedValueOnce({ entries: [entry(10, 60_000)] });
    render(<PulseTile {...base} />);
    expect(await screen.findByRole('img', { name: '51 events in the last 24 hours' })).toBeTruthy();
    expect(api.listAudit).toHaveBeenLastCalledWith(51);
  });

  it('shows an error, not zero events, when unreachable', async () => {
    healthy({ listAudit: 'unreachable' });
    render(<PulseTile {...base} />);
    await waitFor(() => expect(led('pulse · 24h')).toContain('d-led-error'));
    expect(screen.getByText('–')).toBeTruthy();
    expect(screen.queryByText(/^peak /)).toBeNull();
    expect(screen.getByText("can't reach the server").getAttribute('aria-live')).toBe('polite');
  });

  it('keeps the last count and tells screen readers it is stale when a later poll fails', async () => {
    const { rerender } = render(<PulseTile {...base} />);
    await waitFor(() => expect(led('pulse · 24h')).toContain('d-led-live'));
    healthy({ listAudit: 'unreachable' });
    rerender(<PulseTile {...base} syncKey={1} />);
    expect(await screen.findByText("can't reach the server · showing the last known count")).toBeTruthy();
    expect(screen.getByRole('img', { name: '2 events in the last 24 hours' })).toBeTruthy();
    expect(led('pulse · 24h')).toContain('d-led-error');
  });

  it('hands an expired session to onUnauthorized', async () => {
    healthy({ listAudit: 'unauthorized' });
    const expired = vi.fn();
    render(<PulseTile {...base} onUnauthorized={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});

describe('ShipTile', () => {
  const ship = (config: Record<string, unknown>, extra: Record<string, unknown> = {}) =>
    render(<ShipTile {...base} widget={{ id: 's', type: 'ship', title: 'launch', config }} {...extra} />);

  it('counts down with a mood for the time left', () => {
    const { unmount } = ship({ start: ago(3600_000), deadline: new Date(Date.now() + 3600_000 + 30_000).toISOString() });
    expect(screen.getByText('final boss energy ⚡')).toBeTruthy();
    expect(screen.getByText(/^01:00:/)).toBeTruthy();
    unmount();
    ship({ deadline: new Date(Date.now() + 5 * 3600_000).toISOString() });
    expect(screen.getByText('lock in. no side quests.')).toBeTruthy();
  });

  it('has runway for a far deadline and ships at zero', () => {
    const { unmount } = ship({ deadline: new Date(Date.now() + 48 * 3600_000).toISOString() });
    expect(screen.getByText('plenty of runway. keep cooking.')).toBeTruthy();
    unmount();
    ship({ start: ago(7200_000), deadline: ago(1000) });
    expect(screen.getByText('shipped. go touch grass 🌱')).toBeTruthy();
    expect(screen.getByText('100%')).toBeTruthy();
  });

  it('asks for a deadline when the one it has cannot be read, never NaN', () => {
    // The human string bacb1fd was reported to ship; V8 cannot parse it.
    for (const deadline of ['not a date', 'Thu 2026-11-05 3:00 AM CET (UTC+01:00)', '']) {
      const { container, unmount } = ship({ deadline });
      expect(screen.getByRole('status').textContent).toBe('set a deadline');
      expect(screen.getByText('--:--:--')).toBeTruthy();
      expect(screen.getByText('pick one in settings')).toBeTruthy();
      expect(screen.getByText('0%')).toBeTruthy();
      expect(container.textContent).not.toMatch(/NaN|until/);
      expect(container.querySelector('.d-led-off')).toBeTruthy();
      unmount();
    }
  });

  it('a fresh tile with no config counts down the built-in hackathon window', () => {
    vi.useFakeTimers();
    try {
      // Wed 4 Nov 14:00 CET: 13 h before the 03:00 CET deadline, 212 h of the 225 h window gone.
      vi.setSystemTime(new Date('2026-11-04T14:00:00+01:00'));
      const { container } = render(<ShipTile {...base} widget={{ id: 's', type: 'ship' }} />);
      expect(container.textContent).not.toMatch(/NaN|—/);
      expect(screen.getByText('13:00:00')).toBeTruthy();
      expect(screen.getByText('94%')).toBeTruthy();
      expect(screen.getByText('lock in. no side quests.')).toBeTruthy();
      expect(screen.getByText(/^until \S/)).toBeTruthy();
      expect(container.querySelector('.d-led-live')).toBeTruthy();
      act(() => { vi.advanceTimersByTime(1000); });
      expect(screen.getByText('12:59:59')).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it('sets a new deadline from its own settings form', () => {
    const onConfig = vi.fn();
    ship({ deadline: new Date(Date.now() + 48 * 3600_000).toISOString() }, { onConfig });
    fireEvent.click(screen.getByRole('button', { name: 'settings for launch' }));
    const input = document.querySelector('input[name="d"]') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '2030-01-02T03:04' } });
    fireEvent.click(screen.getByRole('button', { name: 'set' }));
    expect(onConfig).toHaveBeenCalledWith(expect.objectContaining({ deadline: new Date('2030-01-02T03:04').toISOString() }));
    expect(document.querySelector('input[name="d"]')).toBeNull();
  });

  it('a date the browser accepts but Date cannot hold is refused in place, never a RangeError', () => {
    // Chromium's datetime-local accepts years up to 275760; past 8.64e15 ms
    // new Date(v) is NaN and toISOString() would throw.
    const onConfig = vi.fn();
    ship({ deadline: new Date(Date.now() + 48 * 3600_000).toISOString() }, { onConfig });
    fireEvent.click(screen.getByRole('button', { name: 'settings for launch' }));
    const input = document.querySelector('input[name="d"]') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '275760-12-31T23:59' } });
    expect(input.value).toBe('275760-12-31T23:59');
    // Clicking `set` does not submit this value in jsdom; submit the form directly.
    fireEvent.submit(input.form as HTMLFormElement);
    expect(onConfig).not.toHaveBeenCalled();
    expect(document.querySelector('input[name="d"]')).toBe(input);
    expect(screen.getByRole('alert').textContent).toBe("that date can't be used");
    expect(input.getAttribute('aria-invalid')).toBe('true');
    // Editing clears the message; a good date then saves and closes.
    fireEvent.change(input, { target: { value: '2030-01-02T03:04' } });
    expect(screen.queryByRole('alert')).toBeNull();
    fireEvent.submit(input.form as HTMLFormElement);
    expect(onConfig).toHaveBeenCalledWith(expect.objectContaining({ deadline: new Date('2030-01-02T03:04').toISOString() }));
    expect(document.querySelector('input[name="d"]')).toBeNull();
  });

  it('an empty form closes without saving, and the default title shows', () => {
    const onConfig = vi.fn();
    render(<ShipTile {...base} widget={{ id: 's', type: 'ship' }} onConfig={onConfig} />);
    fireEvent.click(screen.getByRole('button', { name: 'settings for ship clock' }));
    fireEvent.click(screen.getByRole('button', { name: 'set' }));
    expect(onConfig).not.toHaveBeenCalled();
    expect(document.querySelector('input[name="d"]')).toBeNull();
  });
});
