import { useState } from 'react';

// Clipboard API needs a secure context (https or localhost); this
// app's plain-http Tailscale pages don't have one, so writeText is
// undefined and a naive navigator.clipboard?.writeText no-ops
// silently. The textarea path is what "copy" degrades to there.
export function copyText(text: string): boolean {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => {});
    return true;
  }
  if (typeof document === 'undefined') {
    return false;
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:absolute;left:-9999px;top:0';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  }
  ta.remove();
  return ok;
}

// CopyButton is the small ghost button every one-line command gets.
// A short toast sitcks to the button's edge in the theme's accent.
export default function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  function onClick(): void {
    if (copyText(text)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    }
  }
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="min-h-11 rounded-md px-3 text-body text-text"
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  );
}
