// Presentation mode: the shell chrome (header, sidebar, tab bar,
// status bar) folds away and the current screen takes the whole
// viewport, with a small LIVE badge. One class on <html>, so any
// screen gets it for free; Esc or the palette turns it off.

const CLS = 'devcules-present';

export function isPresenting(): boolean {
  return document.documentElement.classList.contains(CLS);
}

// Leaving real fullscreen (the browser's own Esc, which never reaches
// the page as a keydown, or its "exit full screen" UI) leaves
// presentation too, so the chrome never stays hidden in a window. Only a
// fullscreen that presentation itself entered counts: a video going
// fullscreen and back must not end the demo.
let fsOwned = false;
let fsWatching = false;

function onFullscreenChange(): void {
  const root = document.documentElement;
  if (document.fullscreenElement === root && isPresenting()) { fsOwned = true; return; }
  if (!document.fullscreenElement && fsOwned) {
    fsOwned = false;
    if (isPresenting()) setPresenting(false);
  }
}

function watchFullscreen(): void {
  if (fsWatching) return;
  fsWatching = true;
  document.addEventListener('fullscreenchange', onFullscreenChange);
}

export function setPresenting(on: boolean): void {
  if (on) watchFullscreen();
  else fsOwned = false;
  document.documentElement.classList.toggle(CLS, on);
  window.dispatchEvent(new CustomEvent('devcules:present', { detail: { on } }));
  if (on) {
    // Best effort: real fullscreen when the browser allows it.
    void document.documentElement.requestFullscreen?.().catch(() => undefined);
  } else if (document.fullscreenElement) {
    void document.exitFullscreen?.().catch(() => undefined);
  }
}

export function togglePresenting(): void {
  setPresenting(!isPresenting());
}

// confetti fires the celebration overlay (shell/Confetti).
export function confetti(): void {
  window.dispatchEvent(new CustomEvent('devcules:confetti'));
}
