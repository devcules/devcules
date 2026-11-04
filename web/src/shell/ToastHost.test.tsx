import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ToastHost from './ToastHost';
import { toast } from '../lib/toast';

// DESK-15: the host lived inside DeskPage, so a toast raised on Settings
// (Secrets' "Stored X") had no listener. It now stands alone, mounted
// beside the shell; this renders it with no Desk at all.
describe('ToastHost', () => {
  afterEach(() => { vi.useRealTimers(); });

  it('shows a toast raised with no Desk mounted, then fades it', () => {
    vi.useFakeTimers();
    render(<ToastHost />);
    act(() => { toast('Stored TAVILY_KEY'); });
    expect(screen.getByText('Stored TAVILY_KEY')).toBeTruthy();
    act(() => { toast('Copy failed', 'bad'); });
    expect(screen.getByText('Copy failed').className).toMatch(/bg-red-500/);
    act(() => { vi.advanceTimersByTime(2300); });
    expect(screen.queryByText('Stored TAVILY_KEY')).toBeNull();
    expect(screen.queryByText('Copy failed')).toBeNull();
  });
});
