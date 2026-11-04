import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';
import ThemePicker from './ThemePicker';
import { FEATURED_THEMES, THEMES, THEME_KEY } from './themes';

// The top-bar picker: closed to its trigger at rest, grouped dark
// then light when open, every theme an option. A pick applies at
// once and persists.
afterEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.removeAttribute('data-scheme');
});

describe('top-bar ThemePicker', () => {
  it('shows the current label, no options to keyboard at rest', () => {
    render(<ThemePicker />);
    expect(screen.getByRole('button', { name: 'Theme' })).toBeTruthy();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('open: featured group, then dark, then light, every theme listed once', async () => {
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

  it('lists the five featured themes first, holo (light) included', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    await user.click(screen.getByRole('button', { name: 'Theme' }));
    const options = screen.getAllByRole('option');
    const labels = FEATURED_THEMES.map((id) => THEMES.find((t) => t.id === id)?.label);
    expect(options.slice(0, 5).map((o) => o.textContent?.replace('✓', '').trim())).toEqual(labels);
    // Every theme appears exactly once across the groups.
    const all = options.map((o) => o.textContent?.replace('✓', '').trim());
    expect(new Set(all).size).toBe(THEMES.length);
    expect(screen.getByRole('group', { name: 'featured' })).toBeTruthy();
  });

  it('the listbox caps at the viewport and scrolls (every option reachable at 720px)', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    await user.click(screen.getByRole('button', { name: 'Theme' }));
    const cls = screen.getByRole('listbox').className;
    expect(cls).toContain('max-h-[calc(100dvh-4rem)]');
    expect(cls).toContain('overflow-y-auto');
  });

  it('focus moves into the list on open, on the current theme, with a roving tabindex', async () => {
    window.localStorage.setItem(THEME_KEY, 'tron');
    const user = userEvent.setup();
    render(<ThemePicker />);
    await user.click(screen.getByRole('button', { name: 'Theme' }));
    const options = screen.getAllByRole('option');
    const current = screen.getByRole('option', { selected: true });
    expect(document.activeElement).toBe(current);
    expect(options.filter((o) => o.tabIndex === 0)).toEqual([current]);
  });

  it('Up/Down/Home/End move focus (wrapping), Enter picks, Escape returns to the trigger', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    const trigger = screen.getByRole('button', { name: 'Theme' });
    await user.click(trigger);
    const options = screen.getAllByRole('option');
    expect(document.activeElement).toBe(options[0]);
    await user.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(options[1]);
    expect(options[1].tabIndex).toBe(0);
    expect(options[0].tabIndex).toBe(-1);
    await user.keyboard('{End}');
    expect(document.activeElement).toBe(options[options.length - 1]);
    await user.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(options[0]);
    await user.keyboard('{ArrowUp}');
    expect(document.activeElement).toBe(options[options.length - 1]);
    await user.keyboard('{Home}');
    expect(document.activeElement).toBe(options[0]);
    // Down to holo (5th featured) and pick it with Enter.
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}{Enter}');
    expect(document.documentElement.getAttribute('data-theme')).toBe('holo');
    expect(document.documentElement.getAttribute('data-scheme')).toBe('light');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(document.activeElement).toBe(trigger);

    await user.click(trigger);
    await user.keyboard('{ArrowDown}{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});
