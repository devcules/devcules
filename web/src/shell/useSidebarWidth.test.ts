import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { readSidebarWidth, SIDEBAR_DEFAULT, SIDEBAR_MAX, SIDEBAR_MIN, useSidebarWidth } from './useSidebarWidth';

// jsdom's default window is 1024 wide, so MAIN_MIN (360) leaves 664 for
// the sidebar: the cap in these tests is SIDEBAR_MAX.
beforeEach(() => {
  window.localStorage.clear();
  window.innerWidth = 1400;
});

afterEach(() => {
  window.innerWidth = 1024;
});

describe('useSidebarWidth', () => {
  it('starts at the shipped width when nothing is saved', () => {
    const { result } = renderHook(() => useSidebarWidth());
    expect(result.current.width).toBe(SIDEBAR_DEFAULT);
  });

  it('restores a saved width', () => {
    window.localStorage.setItem('devcules.sidebar.width', '320');
    expect(readSidebarWidth()).toBe(320);
  });

  it('clamps a saved width into the allowed range', () => {
    window.localStorage.setItem('devcules.sidebar.width', '9999');
    expect(readSidebarWidth()).toBe(SIDEBAR_MAX);
    window.localStorage.setItem('devcules.sidebar.width', '5');
    expect(readSidebarWidth()).toBe(SIDEBAR_MIN);
    window.localStorage.setItem('devcules.sidebar.width', 'not a number');
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT);
  });

  it('never lets the sidebar squeeze the main area below its minimum', () => {
    // A 600px window leaves 240 for the column (600 - 360 main). A saved
    // 480 is cut back to that, not to SIDEBAR_MIN: the point is that the
    // main area keeps its room, not that the column bottoms out.
    window.innerWidth = 600;
    expect(readSidebarWidth()).toBe(240);
    window.localStorage.setItem('devcules.sidebar.width', '480');
    expect(readSidebarWidth()).toBe(240);
    // Below what MAIN_MIN leaves, the column bottoms out instead of
    // going negative.
    window.innerWidth = 400;
    expect(readSidebarWidth()).toBe(SIDEBAR_MIN);
  });

  it('nudge moves and saves the width, clamped', () => {
    const { result } = renderHook(() => useSidebarWidth());
    act(() => result.current.nudge(40));
    expect(result.current.width).toBe(SIDEBAR_DEFAULT + 40);
    expect(window.localStorage.getItem('devcules.sidebar.width')).toBe(String(SIDEBAR_DEFAULT + 40));
    act(() => result.current.nudge(-10000));
    expect(result.current.width).toBe(SIDEBAR_MIN);
  });

  it('reset returns to the shipped width and saves it', () => {
    const { result } = renderHook(() => useSidebarWidth());
    act(() => result.current.nudge(80));
    act(() => result.current.reset());
    expect(result.current.width).toBe(SIDEBAR_DEFAULT);
    expect(window.localStorage.getItem('devcules.sidebar.width')).toBe(String(SIDEBAR_DEFAULT));
  });

  it('a blocked localStorage never breaks the width', () => {
    const get = window.localStorage.getItem.bind(window.localStorage);
    window.localStorage.getItem = () => {
      throw new Error('denied');
    };
    try {
      expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT);
    } finally {
      window.localStorage.getItem = get;
    }
  });
});
