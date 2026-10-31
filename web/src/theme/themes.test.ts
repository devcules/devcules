import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { THEMES, THEME_KEY, applyTheme, currentTheme, initTheme, setTheme } from './themes';

// The registry is the one source of truth: ids unique, the fallback
// an entry itself. setTheme persists and applies; the boot hook
// reads the persisted id back. Storage-blocked never throws.
describe('theme registry', () => {
  it('has unique ids, and arc is the fallback', () => {
    const ids = THEMES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids[0]).toBe('arc');
  });

  it('every theme carries a 3-colour swatch and a scheme', () => {
    for (const t of THEMES) {
      expect(t.swatch, t.id).toHaveLength(3);
      for (const hex of t.swatch) {
        expect(hex).toMatch(/^#[0-9a-f]{6}$/i);
      }
      expect(['dark', 'light']).toContain(t.scheme);
    }
  });
});

describe('currentTheme / setTheme', () => {
  beforeEach(() => {
    window.localStorage.removeItem(THEME_KEY);
  });
  afterEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-theme');
  });

  it('defaults to arc with nothing stored', () => {
    expect(currentTheme()).toBe('arc');
  });

  it('ignores a stored id that is no longer a theme', () => {
    window.localStorage.setItem(THEME_KEY, 'deleted-theme');
    expect(currentTheme()).toBe('arc');
  });

  it('setTheme pins data-theme, the scheme and localStorage', () => {
    setTheme('meta-hackathon');
    expect(document.documentElement.getAttribute('data-theme')).toBe('meta-hackathon');
    expect(document.documentElement.style.colorScheme).toBe('dark');
    expect(window.localStorage.getItem(THEME_KEY)).toBe('meta-hackathon');
    expect(currentTheme()).toBe('meta-hackathon');
  });

  it('setTheme notifies listeners with the id', () => {
    const ids: string[] = [];
    function on(e: Event): void {
      ids.push((e as CustomEvent<{ id: string }>).detail.id);
    }
    window.addEventListener('devcules:theme-change', on);
    setTheme('slate');
    window.removeEventListener('devcules:theme-change', on);
    expect(ids).toEqual(['slate']);
  });

  it('initTheme boots the stored theme', () => {
    window.localStorage.setItem(THEME_KEY, 'midnight');
    initTheme();
    expect(document.documentElement.getAttribute('data-theme')).toBe('midnight');
  });

  it('applyTheme updates the theme-color meta to the base swatch', () => {
    const meta = document.createElement('meta');
    meta.setAttribute('name', 'theme-color');
    document.head.appendChild(meta);
    applyTheme('midnight');
    expect(meta.getAttribute('content')).toBe('#0a0f24');
    document.head.removeChild(meta);
  });
});
