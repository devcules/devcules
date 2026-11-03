import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useRunners } from './useRunners';

// useRunners_test — the "Couldn't reach the server" regression. With
// two runners linked an untargeted terminal call is a 400; the screens
// resolve a target from this list to avoid it. The hook must report
// only live runners, and say unreachable only when the server is.

function Probe({ onSessionExpired }: { onSessionExpired?: () => void }) {
  const r = useRunners(onSessionExpired);
  return <span>{JSON.stringify(r)}</span>;
}

function stub(fn: () => Response): void {
  window.fetch = vi.fn(async () => fn()) as typeof fetch;
}

describe('useRunners', () => {
  it('lists only the runners that are currently linked', async () => {
    stub(() =>
      Response.json({
        targets: [
          { target: 'unbound', runner: '', linked: true, mode: '' },
          { target: 'runner:box', runner: 'box', linked: true, mode: 'critical' },
          { target: 'runner:gone', runner: 'gone', linked: false, mode: 'off' },
        ],
        modes: [],
        rules: [],
        timeoutSeconds: 60,
      }),
    );
    render(<Probe />);
    // "gone" is remembered but not linked: offering it would only
    // produce a 404 once the screen opened a terminal there.
    expect(await screen.findByText(/"runners":\["box"\]/)).toBeDefined();
  });

  it('sorts the names so the default pick is stable', async () => {
    stub(() =>
      Response.json({
        targets: [
          { target: 'runner:zeta', runner: 'zeta', linked: true, mode: '' },
          { target: 'runner:alpha', runner: 'alpha', linked: true, mode: '' },
        ],
        modes: [],
        rules: [],
        timeoutSeconds: 60,
      }),
    );
    render(<Probe />);
    expect(await screen.findByText(/"runners":\["alpha","zeta"\]/)).toBeDefined();
  });

  it('reports unreachable when the settings call fails', async () => {
    stub(() => new Response('nope', { status: 500 }));
    render(<Probe />);
    expect(await screen.findByText(/"state":"unreachable"/)).toBeDefined();
  });

  it('routes an expired session to sign-in instead of erroring', async () => {
    const expired = vi.fn();
    stub(() => new Response('', { status: 401 }));
    render(<Probe onSessionExpired={expired} />);
    await vi.waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
