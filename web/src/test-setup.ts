import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

// Component tests import from vitest explicitly (no globals), so the
// automatic DOM cleanup never arms: unmount every render here instead
// of leaking one Shell per test into the next query.
afterEach(() => {
  cleanup();
});
