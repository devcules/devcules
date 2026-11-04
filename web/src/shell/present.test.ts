import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { confetti, isPresenting, setPresenting, togglePresenting } from './present';

// jsdom has no Fullscreen API: stand one in that keeps
// document.fullscreenElement and fires fullscreenchange like a browser.
let fsEl: Element | null = null;
const root = document.documentElement;
function fsTo(el: Element | null) {
  fsEl = el;
  document.dispatchEvent(new Event('fullscreenchange'));
}

describe('presentation mode', () => {
  let request: ReturnType<typeof vi.fn>;
  let exit: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    fsEl = null;
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, get: () => fsEl });
    request = vi.fn(() => Promise.resolve());
    exit = vi.fn(() => Promise.resolve());
    Object.defineProperty(root, 'requestFullscreen', { configurable: true, value: request });
    Object.defineProperty(document, 'exitFullscreen', { configurable: true, value: exit });
  });
  afterEach(() => {
    setPresenting(false);
    delete (document as { fullscreenElement?: unknown }).fullscreenElement;
    delete (root as { requestFullscreen?: unknown }).requestFullscreen;
    delete (document as { exitFullscreen?: unknown }).exitFullscreen;
  });

  it('toggles the class on <html>, announces it, and asks for fullscreen', () => {
    const seen: boolean[] = [];
    const on = (e: Event) => seen.push((e as CustomEvent<{ on: boolean }>).detail.on);
    window.addEventListener('devcules:present', on);
    expect(isPresenting()).toBe(false);
    togglePresenting();
    expect(isPresenting()).toBe(true);
    expect(root.classList.contains('devcules-present')).toBe(true);
    expect(request).toHaveBeenCalledOnce();
    togglePresenting();
    expect(isPresenting()).toBe(false);
    expect(seen).toEqual([true, false]);
    // Not in fullscreen, so nothing to exit.
    expect(exit).not.toHaveBeenCalled();
    window.removeEventListener('devcules:present', on);
  });

  it('the state lives on <html>, so it persists across readers until turned off', () => {
    setPresenting(true);
    setPresenting(true);
    expect(isPresenting()).toBe(true);
    expect(root.classList.contains('devcules-present')).toBe(true);
    setPresenting(false);
    expect(isPresenting()).toBe(false);
  });

  it('leaving presentation exits the fullscreen it entered', () => {
    setPresenting(true);
    fsTo(root);
    setPresenting(false);
    expect(exit).toHaveBeenCalledOnce();
  });

  // DESK-19: the browser eats the Esc that leaves real fullscreen, so the
  // page only hears fullscreenchange.
  it('leaving real fullscreen leaves presentation', () => {
    setPresenting(true);
    fsTo(root);
    expect(isPresenting()).toBe(true);
    fsTo(null);
    expect(isPresenting()).toBe(false);
    expect(exit).not.toHaveBeenCalled();
  });

  it('a fullscreen presentation did not enter does not end it', () => {
    setPresenting(true);
    // Fullscreen was refused; a video goes fullscreen and back.
    const video = document.createElement('video');
    fsTo(video);
    fsTo(null);
    expect(isPresenting()).toBe(true);
    // Leaving fullscreen while not presenting is a no-op.
    setPresenting(false);
    fsTo(root);
    fsTo(null);
    expect(isPresenting()).toBe(false);
  });

  it('turning presentation off first, then fullscreen ending, does not flip it back', () => {
    setPresenting(true);
    fsTo(root);
    setPresenting(false);
    fsTo(null);
    expect(isPresenting()).toBe(false);
    setPresenting(true);
    fsTo(null);
    expect(isPresenting()).toBe(true);
  });

  it('a refused fullscreen request is swallowed', async () => {
    request.mockImplementation(() => Promise.reject(new Error('denied')));
    setPresenting(true);
    await Promise.resolve();
    expect(isPresenting()).toBe(true);
  });

  it('works where the Fullscreen API is missing', () => {
    delete (root as { requestFullscreen?: unknown }).requestFullscreen;
    setPresenting(true);
    expect(isPresenting()).toBe(true);
  });

  it('confetti fires the overlay event', () => {
    const party = vi.fn();
    window.addEventListener('devcules:confetti', party);
    confetti();
    expect(party).toHaveBeenCalledOnce();
    window.removeEventListener('devcules:confetti', party);
  });
});
