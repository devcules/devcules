import { useEffect, useState } from 'react';

// useMobile is the single JS flag that drives the whole mobile story,
// the way Kaithos does it: no CSS media queries on the terminal path,
// because a coarse pointer alone is unreliable (emulators, iPads).
// Mobile means a coarse pointer AND at least one touch point AND a
// phone-sized edge (portrait width or landscape height). The value is
// also mirrored to the `devcules-mobile` class on <html>/<body> by
// useMobileBodyClass so plain CSS can join in (the scroll lock).
export function useMobile(): boolean {
  const [mobile, setMobile] = useState(readMobile);
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return undefined; // test/SSR DOM: stays desktop, nothing to attach
    }
    const mq = window.matchMedia('(pointer: coarse)');
    const update = () => setMobile(readMobile());
    update();
    mq.addEventListener('change', update);
    window.addEventListener('resize', update);
    window.addEventListener('orientationchange', update);
    return () => {
      mq.removeEventListener('change', update);
      window.removeEventListener('resize', update);
      window.removeEventListener('orientationchange', update);
    };
  }, []);
  return mobile;
}

function readMobile(): boolean {
  // jsdom and SSR lack matchMedia / visualViewport; treat them as
  // desktop (the strip hides, no scroll lock) so tests and first paint
  // never crash on the phone path.
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return false;
  }
  const coarse = window.matchMedia('(pointer: coarse)').matches;
  const touch = navigator.maxTouchPoints > 0;
  const phoneSized = window.innerWidth <= 820 || window.innerHeight <= 500;
  return coarse && touch && phoneSized;
}

// useMobileBodyClass mirrors the flag onto <html>/<body> as
// `devcules-mobile`, which gates the page scroll lock in main.css.
export function useMobileBodyClass(mobile: boolean): void {
  useEffect(() => {
    const el = document.documentElement;
    const body = document.body;
    el.classList.toggle('devcules-mobile', mobile);
    body.classList.toggle('devcules-mobile', mobile);
    return () => {
      el.classList.remove('devcules-mobile');
      body.classList.remove('devcules-mobile');
    };
  }, [mobile]);
}

// useVisibleHeight tracks the *visible* height — the piece of the page
// not covered by the URL bar or, crucially, the software keyboard.
// `100dvh` does NOT shrink past the iOS keyboard; visualViewport.height
// does, by exactly the keyboard's height. So the terminal page column
// is sized from this and the pane reflows to stay above the keys.
// Only `resize` is listened to (never `scroll`, which fires every touch
// frame), and the commit is direction-aware: shrink (keys appearing)
// lands at once so the prompt lifts before the first keystroke; grow
// (bar/keys retreating) debounces 150 ms against URL-bar flicker.
export function useVisibleHeight(): number {
  const [height, setHeight] = useState(() => (typeof window === 'undefined' ? 800 : window.innerHeight));
  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const vv = window.visualViewport;
    if (!vv) {
      const onResize = () => setHeight(window.innerHeight);
      window.addEventListener('resize', onResize);
      return () => window.removeEventListener('resize', onResize);
    }
    let last = vv.height;
    let timer: number | null = null;
    const onResize = () => {
      const next = vv.height;
      if (next < last - 2) {
        // Keyboard appearing: commit now.
        if (timer !== null) {
          window.clearTimeout(timer);
          timer = null;
        }
        last = next;
        setHeight(next);
      } else if (next > last + 2) {
        // Growing: debounce — the iOS URL bar jitters during scroll.
        last = next;
        if (timer !== null) {
          window.clearTimeout(timer);
        }
        timer = window.setTimeout(() => setHeight(vv.height), 150);
      }
    };
    vv.addEventListener('resize', onResize);
    return () => {
      vv.removeEventListener('resize', onResize);
      if (timer !== null) {
        window.clearTimeout(timer);
      }
    };
  }, []);
  return height;
}
