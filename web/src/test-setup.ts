import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

// Component tests import from vitest explicitly (no globals), so the
// automatic DOM cleanup never arms: unmount every render here instead
// of leaking one Shell per test into the next query.
afterEach(() => {
  cleanup();
});
// jsdom has no matchMedia or ResizeObserver: the mobile terminal path
// (useMobile + the strip's host) reads both. Stub enough for the
// untouched desktop code path; per-test owners override what they need.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}
