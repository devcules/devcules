// ErrorBoundary keeps one render-time throw from unmounting the whole
// React tree (a blank white page). React only offers boundaries as class
// components. Two uses: main.tsx wraps <App/> with the full-page
// fallback, and Tile wraps each tile body with a small inline one, so a
// crashing widget shows "this tile crashed · reload" and the rest of the
// desk keeps working.

import { Component, type ReactNode } from 'react';

type Props = {
  children: ReactNode;
  // Rendered instead of children after a throw. `reset` clears the error
  // and mounts the children again.
  fallback?: (error: Error, reset: () => void) => ReactNode;
};

type State = { error: Error | null };

export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: unknown): State {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  // No componentDidCatch: React itself logs a caught render error with its
  // component stack (dev and prod), and the lint bans console here.

  reset = () => this.setState({ error: null });

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    if (this.props.fallback) return this.props.fallback(error, this.reset);
    return <AppCrashed error={error} />;
  }
}

// The full-page fallback: plain markup and theme tokens only, so it still
// renders when the thing that broke is the app's own chrome.
function AppCrashed({ error }: { error: Error }) {
  return (
    <div role="alert" className="flex min-h-dvh flex-col items-center justify-center gap-3 bg-base p-6 text-center text-text">
      <strong className="text-body">Something went wrong.</strong>
      <span className="max-w-md break-words text-tab text-muted">{error.message}</span>
      <button type="button" className="min-h-11 rounded bg-accent px-4 text-body text-on-accent" onClick={() => window.location.reload()}>
        reload
      </button>
    </div>
  );
}

// TileCrashed is the inline fallback a tile body shows.
export function TileCrashed({ reset }: { reset: () => void }) {
  return (
    <div role="alert" className="text-body text-muted">
      this tile crashed ·{' '}
      <button type="button" className="d-tap min-h-7 rounded px-1 text-accent underline hover:text-text" onClick={reset}>reload</button>
    </div>
  );
}
