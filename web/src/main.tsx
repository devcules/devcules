import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './app';
import { initTheme } from './theme/themes';
import './main.css';

// Pin the stored theme before the first paint: the attribute sets
// the palette and the (optional) glass, so nothing flashes the
// default first. main.css must stay after this import so its own
// boot rules compose with the tokens, not around them.
initTheme();

const root = document.getElementById('root');
if (!root) {
  throw new Error('missing #root element');
}
createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

// The service worker only shows push notices and routes taps (see
// public/sw.js); it caches nothing. Browsers without one skip it.
if ('serviceWorker' in navigator && window.isSecureContext) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}
