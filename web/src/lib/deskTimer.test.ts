import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fmtClock, isFinished, pauseTimer, readTimer, remainingMs, resumeTimer, startTimer, TIMER_KEY, writeTimer } from './deskTimer';

describe('deskTimer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-01T12:00:00Z'));
    localStorage.clear();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('start sets an absolute end and runs', () => {
    const t = startTimer(25);
    expect(t.running).toBe(true);
    expect(remainingMs(t)).toBe(25 * 60000);
  });

  it('leave-and-come-back keeps the end, so an elapsed timer stays elapsed', () => {
    const t = startTimer(25);
    writeTimer(t);
    vi.setSystemTime(new Date('2026-11-01T12:10:00Z'));
    const back = readTimer();
    expect(back).not.toBeNull();
    expect(remainingMs(back)).toBe(15 * 60000);
  });

  it('pause stores remaining, resume re-bases the end from now', () => {
    let t = startTimer(25);
    vi.setSystemTime(new Date('2026-11-01T12:05:00Z'));
    t = pauseTimer(t);
    expect(remainingMs(t)).toBe(20 * 60000);
    expect(isFinished(t)).toBe(false);
    vi.setSystemTime(new Date('2026-11-01T12:50:00Z')); // 45 min later, still 20 left while paused
    expect(remainingMs(t)).toBe(20 * 60000);
    t = resumeTimer(t);
    expect(remainingMs(t)).toBe(20 * 60000);
  });

  it('finish reports once the end passes', () => {
    const t = startTimer(5);
    expect(isFinished(t)).toBe(false);
    vi.setSystemTime(new Date('2026-11-01T12:06:00Z'));
    expect(isFinished(t)).toBe(true);
  });

  it('writeTimer(null) clears the store', () => {
    writeTimer(startTimer(5));
    expect(localStorage.getItem(TIMER_KEY)).not.toBeNull();
    writeTimer(null);
    expect(localStorage.getItem(TIMER_KEY)).toBeNull();
  });

  it('readTimer tolerates junk', () => {
    localStorage.setItem(TIMER_KEY, '{not json');
    expect(readTimer()).toBeNull();
    localStorage.setItem(TIMER_KEY, '{"totalMs":"nope"}');
    expect(readTimer()).toBeNull();
  });

  it('fmtClock rounds up and pads', () => {
    expect(fmtClock(0)).toBe('00:00');
    expect(fmtClock(61000)).toBe('01:01');
    expect(fmtClock(25 * 60000 + 500)).toBe('25:01');
  });

  it('startTimer never returns a zero or negative length', () => {
    expect(startTimer(0).totalMs).toBeGreaterThan(0);
  });
});
