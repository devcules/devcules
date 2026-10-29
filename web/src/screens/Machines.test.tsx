import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Machines from './Machines';

type Reply = { status: number; body?: unknown };

function reply(r: Reply): Response {
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

// routes answers by "METHOD path", so each flow pins its own calls.
function routes(table: Record<string, Reply>): ReturnType<typeof vi.fn> {
  const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const r = table[key];
    if (!r) {
      throw new Error(`unexpected fetch ${key}`);
    }
    return reply(r);
  });
  window.fetch = fn as typeof fetch;
  return fn;
}

const linked = { machines: ['kitchen-mac'], budget: 40 };

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Machines', () => {
  it('renders the machine list', async () => {
    const table: Record<string, Reply> = {
      'GET /api/machines': { status: 200, body: linked },
      'GET /api/machines-screen?machine=kitchen-mac&t=1': { status: 200 },
    };
    routes(table);
    render(<Machines onSessionExpired={vi.fn()} />);
    expect(await screen.findByText('kitchen-mac')).toBeDefined();
    expect(screen.getByText('linked')).toBeDefined();
    expect(screen.getByText(/40 actions left/)).toBeDefined();
  });

  it('shows the offline message with no machine', async () => {
    routes({ 'GET /api/machines': { status: 503 } });
    render(<Machines onSessionExpired={vi.fn()} />);
    expect(await screen.findByRole('alert')).toBeDefined();
    expect(screen.getByText(/No machine is linked/)).toBeDefined();
    expect(screen.getByRole('status')).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    routes({ 'GET /api/machines': { status: 401 } });
    const expired = vi.fn();
    render(<Machines onSessionExpired={expired} />);
    await screen.findByText(/No machines yet/).catch(() => null);
    expect(expired).toHaveBeenCalled();
  });

  it('grounds a phrase and draws the match markers', async () => {
    const user = userEvent.setup();
    const match = { id: 0, x1: 5, y1: 6, x2: 7, y2: 8, maskW: 3, maskH: 3, clickX: 6, clickY: 7 };
    let groundAnswered = false;
    const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const key = `${init?.method ?? 'GET'} ${input.toString()}`;
      if (key === 'GET /api/machines') {
        return reply({ status: 200, body: linked });
      }
      if (key.startsWith('GET /api/machines-screen')) {
        return reply({ status: 200 });
      }
      if (key === 'POST /api/machines-ground?machine=kitchen-mac') {
        groundAnswered = true;
        return reply({ status: 200, body: { matches: [match] } });
      }
      throw new Error(`unexpected fetch ${key}`);
    });
    window.fetch = fn as typeof fetch;
    render(<Machines onSessionExpired={vi.fn()} />);
    await screen.findByText('kitchen-mac');

    await user.type(screen.getByLabelText('What to find'), 'green Pay button');
    await user.click(screen.getByRole('button', { name: 'Find it' }));
    expect(await screen.findByText(/1 match marked/)).toBeDefined();
    expect(groundAnswered).toBe(true);
    // The marker itself renders in the screen overlay.
    expect(screen.getByLabelText('Grounded match 1')).toBeDefined();
  });

  it('shows the done-verifier note', async () => {
    const user = userEvent.setup();
    const fn = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const key = `${init?.method ?? 'GET'} ${input.toString()}`;
      if (key === 'GET /api/machines') {
        return reply({ status: 200, body: linked });
      }
      if (key.startsWith('GET /api/machines-screen')) {
        return reply({ status: 200 });
      }
      if (key === 'POST /api/machines-verify?machine=kitchen-mac') {
        return reply({
          status: 200,
          body: { png: 'aGVsbG8=', tree: { elements: [] }, note: 'the verifier saw a screen' },
        });
      }
      throw new Error(`unexpected fetch ${key}`);
    });
    window.fetch = fn as typeof fetch;
    render(<Machines onSessionExpired={vi.fn()} />);
    await screen.findByText('kitchen-mac');
    await user.click(screen.getByRole('button', { name: 'Check done' }));
    expect(await screen.findByText('the verifier saw a screen')).toBeDefined();
  });
});
