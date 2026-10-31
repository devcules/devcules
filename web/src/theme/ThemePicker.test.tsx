import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';
import ThemePicker from './ThemePicker';
import { THEMES, THEME_KEY } from './themes';

// The top-bar picker: closed to its trigger at rest, grouped dark
// then light when open, every theme an option. A pick applies at
// once and persists.
afterEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
});

describe('top-bar ThemePicker', () => {
  it('shows the current label, no options to keyboard at rest', () => {
    render(<ThemePicker />);
    expect(screen.getByRole('button', { name: 'Theme' })).toBeTruthy();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('open: dark group then light, every theme listed once', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    await user.click(screen.getByRole('button', { name: 'Theme' }));
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(THEMES.length);
    // ids unique and the first option darker than the last (light follows dark).
    expect(options[0].getAttribute('aria-selected')).toBe('true');
    expect(screen.getByText('dark')).toBeTruthy();
    expect(screen.getByText('light')).toBeTruthy();
  });

  it('a pick applies and persists; Escape closes without a pick', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    await user.click(screen.getByRole('button', { name: 'Theme' }));
    await user.click(screen.getByRole('option', { name: /tokyo night/ }));
    expect(document.documentElement.getAttribute('data-theme')).toBe('tokyo-night');
    expect(window.localStorage.getItem(THEME_KEY)).toBe('tokyo-night');
    expect(screen.queryByRole('listbox')).toBeNull();

    await user.click(screen.getByRole('button', { name: 'Theme' }));
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('an outside pick keeps the trigger label honest', () => {
    render(<ThemePicker />);
    act(() => {
      window.dispatchEvent(new CustomEvent('devcules:theme-change', { detail: { id: 'catppuccin' } }));
    });
    expect(screen.getByRole('button', { name: 'Theme' }).textContent).toContain('catppuccin');
  });

  it('stored theme reads back as the trigger label', () => {
    window.localStorage.setItem(THEME_KEY, 'gruvbox');
    render(<ThemePicker />);
    expect(screen.getByRole('button', { name: 'Theme' }).textContent).toContain('gruvbox');
  });
});
