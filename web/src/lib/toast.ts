// Copy to clipboard + a tiny toast bus. Any component can call copyText();
// ToastHost (mounted once on the Desk) shows the result. The event name is
// devcules-scoped so two apps on one origin never cross-talk.

export async function copyText(text: string, label = 'Copied'): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text);
    else {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    toast(label);
    return true;
  } catch {
    toast('Copy failed', 'bad');
    return false;
  }
}

export type ToastTone = 'ok' | 'bad';
export type ToastEvent = { message: string; tone: ToastTone };

export function toast(message: string, tone: ToastTone = 'ok'): void {
  try {
    window.dispatchEvent(new CustomEvent<ToastEvent>('devcules:toast', { detail: { message, tone } }));
  } catch {
    /* no window */
  }
}
