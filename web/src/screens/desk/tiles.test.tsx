import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ago, NewsTile } from './tiles';

// The tile's age line. These pin the bug the news feed hit: the server
// formatted an unparsed date as the Go zero time, which is a perfectly
// valid RFC3339 string, so the tile rendered "Fortune · 739923d ago".
describe('ago', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function at(iso: string) {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(iso));
  }

  it('reads minutes, hours and days', () => {
    at('2026-11-03T12:00:00Z');
    expect(ago('2026-11-03T11:30:00Z')).toBe('30m');
    expect(ago('2026-11-03T07:00:00Z')).toBe('5h');
    expect(ago('2026-11-01T12:00:00Z')).toBe('2d');
  });

  it('shows nothing when there is no timestamp', () => {
    expect(ago(undefined)).toBe('');
    expect(ago('')).toBe('');
  });

  it('shows nothing for a placeholder date instead of a huge age', () => {
    at('2026-11-03T12:00:00Z');
    // Go's zero time, the exact string the server used to emit.
    expect(ago('0001-01-01T00:00:00Z')).toBe('');
    expect(ago('1970-01-01T00:00:00Z')).toBe('');
    expect(ago('1999-12-31T23:59:59Z')).toBe('');
  });

  it('shows nothing for text that is not a date', () => {
    expect(ago('yesterday')).toBe('');
    expect(ago('not-a-date')).toBe('');
  });

  it('does not report a negative age for a future stamp', () => {
    at('2026-11-03T12:00:00Z');
    // A slightly-ahead clock is skew, not "in -2m".
    expect(ago('2026-11-03T12:02:00Z')).toBe('0m');
  });
});

// The tile reads the API, so this pins what a viewer actually saw: a
// story whose date is unusable must not leave a dangling " ·  ago".
describe('NewsTile', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const widget = { id: 'n1', type: 'security' } as unknown as Parameters<typeof NewsTile>[0]['widget'];

  function serve(items: unknown[]) {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ configured: true, items })));
  }

  it('shows the source and age of a dated story', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2026-11-03T12:00:00Z'));
    serve([{ title: 'A fresh headline', url: 'https://example.test/a', source: 'Fortune', published: '2026-11-03T10:00:00Z' }]);

    render(<NewsTile widget={widget} syncKey={0} arranging={false} onRemove={vi.fn()} onSettings={vi.fn()} onUnauthorized={vi.fn()} />);

    expect(await screen.findByText('A fresh headline')).toBeTruthy();
    expect(screen.getByText('Fortune · 2h ago')).toBeTruthy();
  });

  it('leaves the source alone when the date is unusable', async () => {
    serve([{ title: 'An undated headline', url: 'https://example.test/b', source: 'Fortune', published: '0001-01-01T00:00:00Z' }]);

    render(<NewsTile widget={widget} syncKey={0} arranging={false} onRemove={vi.fn()} onSettings={vi.fn()} onUnauthorized={vi.fn()} />);

    expect(await screen.findByText('An undated headline')).toBeTruthy();
    await waitFor(() => expect(screen.getByText('Fortune')).toBeTruthy());
    expect(screen.queryByText(/ago/)).toBeNull();
  });
});
