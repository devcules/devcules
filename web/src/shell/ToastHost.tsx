// ToastHost listens for toast() events from anywhere in the app (copy,
// save, Secrets' "Stored X") and shows a small pill bottom-centre that
// fades itself. It is mounted once, beside the shell in app.tsx, so a
// toast raised on any screen is seen.

import { useEffect, useState } from 'react';
import type { ToastEvent } from '../lib/toast';

export default function ToastHost() {
  const [items, setItems] = useState<Array<{ id: number; message: string; tone: string }>>([]);
  useEffect(() => {
    let id = 0;
    const timers = new Set<number>();
    const on = (e: Event) => {
      const d = (e as CustomEvent<ToastEvent>).detail;
      if (!d) return;
      id += 1;
      const mine = id;
      setItems((p) => [...p, { id: mine, message: d.message, tone: d.tone }]);
      const t = window.setTimeout(() => { timers.delete(t); setItems((p) => p.filter((x) => x.id !== mine)); }, 2200);
      timers.add(t);
    };
    window.addEventListener('devcules:toast', on);
    return () => {
      window.removeEventListener('devcules:toast', on);
      timers.forEach((t) => window.clearTimeout(t));
    };
  }, []);
  // The live region stays mounted so screen readers announce each toast.
  return (
    <div aria-live="polite" className="pointer-events-none fixed inset-x-0 bottom-16 z-[70] flex flex-col items-center gap-1">
      {items.map((t) => (
        <div key={t.id} className={`rounded-full px-4 py-1.5 text-body ${t.tone === 'bad' ? 'bg-red-500 text-white' : 'bg-text text-base'}`}>{t.message}</div>
      ))}
    </div>
  );
}
