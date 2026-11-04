import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Confetti from './Confetti';

// jsdom has no 2D canvas and no animation frames: stand in a recording
// context and a manual frame queue so each frame can be stepped.
type Ctx = { clearRect: ReturnType<typeof vi.fn>; fillRect: ReturnType<typeof vi.fn>; save: () => void; restore: () => void; translate: () => void; rotate: () => void; fillStyle: string };
let ctx: Ctx | null;
let frames: Array<FrameRequestCallback>;
let reduced: boolean;
let clock: number;

function step(t: number) {
  clock = t;
  const run = frames.splice(0);
  for (const f of run) f(t);
}

describe('Confetti', () => {
  beforeEach(() => {
    frames = [];
    reduced = false;
    clock = 0;
    ctx = { clearRect: vi.fn(), fillRect: vi.fn(), save: vi.fn(), restore: vi.fn(), translate: vi.fn(), rotate: vi.fn(), fillStyle: '' };
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation((() => ctx) as never);
    vi.stubGlobal('requestAnimationFrame', (f: FrameRequestCallback) => { frames.push(f); return frames.length; });
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    vi.spyOn(performance, 'now').mockImplementation(() => clock);
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: reduced && q.includes('reduce'), media: q, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const burst = () => act(() => { window.dispatchEvent(new CustomEvent('devcules:confetti')); });

  it('renders a click-through, hidden canvas', () => {
    const { container } = render(<Confetti />);
    const cv = container.querySelector('canvas')!;
    expect(cv.getAttribute('aria-hidden')).toBe('true');
    expect(cv.className).toContain('pointer-events-none');
  });

  it('draws nothing at all under reduced motion', () => {
    reduced = true;
    render(<Confetti />);
    burst();
    expect(frames).toHaveLength(0);
    expect(ctx!.fillRect).not.toHaveBeenCalled();
  });

  it('bursts, animates, and clears when its life ends', () => {
    const { container } = render(<Confetti />);
    burst();
    const cv = container.querySelector('canvas')!;
    expect(cv.width).toBe(window.innerWidth);
    expect(cv.height).toBe(window.innerHeight);
    expect(frames).toHaveLength(1);
    step(16);
    expect(ctx!.fillRect).toHaveBeenCalledTimes(160);
    // Still alive, so another frame is queued.
    expect(frames).toHaveLength(1);
    // Past the 2.4 s life: one last clear and no new frame.
    step(2500);
    expect(frames).toHaveLength(0);
    expect(ctx!.clearRect).toHaveBeenCalled();
    // A fresh burst starts again from nothing.
    burst();
    step(2600);
    expect(ctx!.fillRect.mock.calls.length).toBeGreaterThanOrEqual(480);
  });

  it('stops once every particle has fallen off the screen', () => {
    render(<Confetti />);
    burst();
    // Far fewer than 2.4 s of frames; gravity takes them all below.
    let n = 0;
    while (frames.length && n < 2000) { step(1 + n * 0.001); n++; }
    expect(frames).toHaveLength(0);
    expect(n).toBeLessThan(2000);
  });

  it('skips a frame without a 2D context, and works without matchMedia', () => {
    vi.stubGlobal('matchMedia', undefined);
    render(<Confetti />);
    burst();
    ctx = null;
    step(16);
    expect(frames).toHaveLength(0);
  });

  it('a frame after unmount does nothing, and unmount stops listening', () => {
    const { unmount } = render(<Confetti />);
    burst();
    const pending = frames.splice(0);
    unmount();
    pending.forEach((f) => f(16));
    expect(ctx!.fillRect).not.toHaveBeenCalled();
    burst();
    expect(frames).toHaveLength(0);
  });
});
