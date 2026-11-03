import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import DeskSources from './DeskSources';

type Seen = { method: string; body: string };

// serve answers GET and PUT /api/desk/sources and records the PUTs.
function serve(get: { status: number; body?: unknown }, put = 204): Seen[] {
  const seen: Seen[] = [];
  window.fetch = vi.fn(async (_input: string | URL | Request, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    seen.push({ method, body: typeof init?.body === 'string' ? init.body : '' });
    if (method === 'PUT') {
      return new Response(null, { status: put });
    }
    return get.body === undefined ? new Response(null, { status: get.status }) : Response.json(get.body, { status: get.status });
  }) as typeof fetch;
  return seen;
}

afterEach(() => {
  vi.restoreAllMocks();
});

const stored = {
  location: 'Berlin',
  ics_feeds: [{ label: 'Work', url: 'https://cal.example/w.ics' }],
  has_tavily: true,
  has_ticket_token: true,
  ticket_provider: 'jira',
};

describe('DeskSources', () => {
  it('edits and saves, keeping stored tokens write-only', async () => {
    const user = userEvent.setup();
    const seen = serve({ status: 200, body: stored });
    const onSaved = vi.fn();
    const onClose = vi.fn();
    render(<DeskSources onClose={onClose} onSaved={onSaved} />);
    const where = await screen.findByDisplayValue('Berlin');
    expect(screen.getAllByText('(stored — leave blank to keep)')).toHaveLength(2);
    await user.clear(where);
    await user.type(where, 'Paris');
    // A second, empty feed is dropped on save.
    await user.click(screen.getByRole('button', { name: '+ another feed' }));
    expect(screen.getAllByLabelText('feed ics url')).toHaveLength(2);
    await user.type(screen.getAllByLabelText('feed label')[0], ' main');
    await user.type(screen.getByPlaceholderText('AI releases'), 'go');
    // Removing the stored Tavily key, then changing our mind on the token.
    // These buttons sit inside their field's label, so they go by text.
    await user.click(screen.getByText('remove stored key'));
    await user.click(screen.getByText('remove stored token'));
    await user.click(screen.getAllByText('keep it after all')[1]);
    await user.click(screen.getByRole('button', { name: 'save sources' }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(onClose).toHaveBeenCalled();
    const body = JSON.parse(seen.find((s) => s.method === 'PUT')?.body ?? '{}');
    expect(body.location).toBe('Paris');
    expect(body.ics_feeds).toEqual([{ label: 'Work main', url: 'https://cal.example/w.ics' }]);
    expect(body.news_tech_query).toBe('go');
    expect(body.tavily_clear).toBe(true);
    expect(body.ticket_clear).toBeUndefined();
    expect(body.tavily_key).toBe('');
  });

  it('shows each tracker its own fields', async () => {
    const user = userEvent.setup();
    serve({ status: 200, body: {} });
    render(<DeskSources onClose={vi.fn()} onSaved={vi.fn()} />);
    const tracker = await screen.findByLabelText('Tracker');
    expect(screen.getByPlaceholderText('https://you.atlassian.net')).toBeDefined();
    expect(screen.getByText(/without it the feeds use free public search/)).toBeDefined();
    await user.selectOptions(tracker, 'asana');
    expect(screen.getByPlaceholderText('1234567890')).toBeDefined();
    expect(screen.queryByPlaceholderText('https://you.atlassian.net')).toBeNull();
    await user.selectOptions(tracker, 'linear');
    expect(screen.getByPlaceholderText('ENG')).toBeDefined();
    // No stored tokens: no remove buttons.
    expect(screen.queryByText(/remove stored/)).toBeNull();
  });

  it('says when saving fails, and closes on cancel or the backdrop', async () => {
    const user = userEvent.setup();
    serve({ status: 200, body: stored }, 500);
    const onClose = vi.fn();
    render(<DeskSources onClose={onClose} onSaved={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'save sources' }));
    expect((await screen.findByRole('alert')).textContent).toBe('save failed — try again');
    await user.click(screen.getByRole('button', { name: 'cancel' }));
    await user.click(screen.getByRole('dialog', { name: 'Desk sources' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('opens empty when the server is down', async () => {
    window.fetch = vi.fn(async () => Promise.reject(new TypeError('down'))) as typeof fetch;
    render(<DeskSources onClose={vi.fn()} onSaved={vi.fn()} />);
    expect((await screen.findByRole('alert')).textContent).toBe('server unreachable');
    expect(screen.getAllByLabelText('feed ics url')).toHaveLength(1);
  });

  it('routes an expired session to sign-in, on load and on save', async () => {
    serve({ status: 401 });
    const expired = vi.fn();
    render(<DeskSources onClose={vi.fn()} onSaved={vi.fn()} onUnauthorized={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
    expect(screen.getByText('loading…')).toBeDefined();
  });

  it('routes a session that expires before saving', async () => {
    const user = userEvent.setup();
    serve({ status: 200, body: {} }, 401);
    const expired = vi.fn();
    render(<DeskSources onClose={vi.fn()} onSaved={vi.fn()} onUnauthorized={expired} />);
    await user.click(await screen.findByRole('button', { name: 'save sources' }));
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
