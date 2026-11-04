import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ErrorBoundary, { TileCrashed } from './ErrorBoundary';
import Tile from '../screens/desk/Tile';

function Boom({ when = true }: { when?: boolean }) {
  if (when) throw new Error('kaboom');
  return <span>fine</span>;
}

// React dev re-dispatches a caught render error as a window 'error' event,
// which jsdom prints as an uncaught stack. The throws below are on purpose.
const quiet = (e: Event) => e.preventDefault();

describe('ErrorBoundary', () => {
  beforeEach(() => window.addEventListener('error', quiet));
  afterEach(() => {
    window.removeEventListener('error', quiet);
    vi.restoreAllMocks();
  });

  it('renders children when nothing throws', () => {
    render(<ErrorBoundary><Boom when={false} /></ErrorBoundary>);
    expect(screen.getByText('fine')).toBeTruthy();
  });

  it('shows the full-page fallback instead of an empty root when a child throws', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { container } = render(<ErrorBoundary><Boom /></ErrorBoundary>);
    expect(container.innerHTML).not.toBe('');
    expect(screen.getByRole('alert').textContent).toContain('Something went wrong');
    expect(screen.getByRole('alert').textContent).toContain('kaboom');
    expect(screen.getByRole('button', { name: 'reload' })).toBeTruthy();
  });

  it('uses a custom fallback and reset mounts the children again', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    let throwNow = true;
    function Flaky() {
      if (throwNow) throw new Error('once');
      return <span>back</span>;
    }
    render(<ErrorBoundary fallback={(_e, reset) => <TileCrashed reset={reset} />}><Flaky /></ErrorBoundary>);
    expect(screen.getByRole('alert').textContent).toContain('this tile crashed');
    throwNow = false;
    fireEvent.click(screen.getByRole('button', { name: 'reload' }));
    expect(screen.getByText('back')).toBeTruthy();
  });

  it('a crashing tile body leaves its header and its sibling tiles rendered', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <div>
        <Tile title="broken" onRefresh={() => {}}><Boom /></Tile>
        <Tile title="ok"><span>sibling body</span></Tile>
      </div>,
    );
    expect(screen.getByRole('alert').textContent).toContain('this tile crashed');
    expect(screen.getByRole('button', { name: 'refresh broken' })).toBeTruthy();
    expect(screen.getByText('sibling body')).toBeTruthy();
  });
});
