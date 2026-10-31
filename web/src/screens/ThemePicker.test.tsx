import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';
import ThemePicker from './ThemePicker';
import { THEMES, THEME_KEY } from '../theme/themes';

// The picker pins and persists the choice. No server round-trip, so
// no fetch mocks — the tests drive the real component with real
// storage, then clean up.
afterEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
});

describe('ThemePicker', () => {
  it('lists every theme as a radio', () => {
    render(<ThemePicker />);
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(THEMES.length);
    for (const t of THEMES) {
      expect(screen.getByDisplayValue(t.id)).toBeTruthy();
    }
  });

  it('checks the stored theme on mount', () => {
    window.localStorage.setItem(THEME_KEY, 'slate');
    render(<ThemePicker />);
    expect(screen.getByDisplayValue<HTMLInputElement>('slate').checked).toBe(true);
  });

  it('pick applies at once and persists', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    await user.click(screen.getByDisplayValue('meta-hackathon'));
    expect(document.documentElement.getAttribute('data-theme')).toBe('meta-hackathon');
    expect(window.localStorage.getItem(THEME_KEY)).toBe('meta-hackathon');
  });
});
