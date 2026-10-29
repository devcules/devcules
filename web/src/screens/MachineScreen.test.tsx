import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MachineScreen from './MachineScreen';

type Reply = { status: number; body?: unknown; headers?: Record<string, string> };

function reply(r: Reply): Response {
  if (r.body instanceof Blob) {
    return new Response(r.body, { status: r.status, headers: r.headers });
  }
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

function serve(handler: (key: string) => Reply | undefined): void {
  window.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const r = handler(key);
    if (!r) {
      throw new Error(`unexpected fetch ${key}`);
    }
    return reply(r);
  }) as typeof fetch;
}

const frame = { status: 200, body: new Blob(['png'], { type: 'image/png' }), headers: { 'X-Devcules-Screen-Scale': '2' } };

beforeEach(() => {
  URL.createObjectURL = vi.fn(() => 'blob:frame');
  URL.revokeObjectURL = vi.fn();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('MachineScreen', () => {
  it('shows the live frame for the named machine', async () => {
    serve((key) => (key === 'GET /api/machines-screen?machine=kitchen-mac' ? frame : undefined));
    render(<MachineScreen machine="kitchen-mac" budget={40} onBack={vi.fn()} onSessionExpired={vi.fn()} />);
    const img = await screen.findByAltText("The machine's live screen");
    expect(img.getAttribute('src')).toBe('blob:frame');
    expect(screen.getByText('40 actions left')).toBeDefined();
  });

  it('says so when the machine is offline', async () => {
    serve((key) => (key.startsWith('GET /api/machines-screen') ? { status: 503 } : undefined));
    render(<MachineScreen machine="kitchen-mac" budget={40} onBack={vi.fn()} onSessionExpired={vi.fn()} />);
    expect(await screen.findByText(/This machine is offline/)).toBeDefined();
  });

  it('grounds a phrase and draws the match markers', async () => {
    const user = userEvent.setup();
    const match = { id: 0, x1: 5, y1: 6, x2: 7, y2: 8, maskW: 3, maskH: 3, clickX: 6, clickY: 7 };
    serve((key) => {
      if (key.startsWith('GET /api/machines-screen')) return frame;
      if (key === 'POST /api/machines-ground?machine=kitchen-mac') return { status: 200, body: { matches: [match] } };
      return undefined;
    });
    render(<MachineScreen machine="kitchen-mac" budget={40} onBack={vi.fn()} onSessionExpired={vi.fn()} />);
    await user.type(screen.getByLabelText('What to find'), 'green Pay button');
    await user.click(screen.getByRole('button', { name: 'Find it' }));
    expect(await screen.findByText(/1 match marked/)).toBeDefined();
    expect(screen.getByLabelText('Grounded match 1')).toBeDefined();
  });

  it('shows the done-verifier note', async () => {
    const user = userEvent.setup();
    serve((key) => {
      if (key.startsWith('GET /api/machines-screen')) return frame;
      if (key === 'POST /api/machines-verify?machine=kitchen-mac') {
        return { status: 200, body: { png: 'aGVsbG8=', tree: { elements: [] }, note: 'the verifier saw a screen' } };
      }
      return undefined;
    });
    render(<MachineScreen machine="kitchen-mac" budget={40} onBack={vi.fn()} onSessionExpired={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Check done' }));
    expect(await screen.findByText('the verifier saw a screen')).toBeDefined();
  });

  it('goes back to the list', async () => {
    const user = userEvent.setup();
    const back = vi.fn();
    serve((key) => (key.startsWith('GET /api/machines-screen') ? frame : undefined));
    render(<MachineScreen machine="kitchen-mac" budget={40} onBack={back} onSessionExpired={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: '← Machines' }));
    expect(back).toHaveBeenCalled();
  });
});
