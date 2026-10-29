import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './app';
import './main.css';

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
