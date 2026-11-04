import { useEffect, useRef } from 'react';

// Confetti is a full-screen, click-through canvas that bursts on the
// `devcules:confetti` event (inbox zero, a shipped demo, or the
// palette). No library: ~150 particles, gravity, a 2.4 s life, and
// nothing at all under reduced motion.
type P = { x: number; y: number; vx: number; vy: number; r: number; c: string; s: number; w: number };
const COLORS = ['#c6ff3d', '#ff4fd8', '#3df5ff', '#8b5cff', '#ffd166', '#ffffff'];

export default function Confetti() {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    let raf = 0;
    let parts: P[] = [];
    let until = 0;
    const draw = (t: number) => {
      const cv = ref.current;
      if (!cv) return;
      const ctx = cv.getContext('2d');
      if (!ctx) return;
      ctx.clearRect(0, 0, cv.width, cv.height);
      for (const p of parts) {
        p.vy += 0.18;
        p.vx *= 0.99;
        p.x += p.vx;
        p.y += p.vy;
        p.r += p.s;
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate(p.r);
        ctx.fillStyle = p.c;
        ctx.fillRect(-p.w / 2, -p.w / 4, p.w, p.w / 2);
        ctx.restore();
      }
      parts = parts.filter((p) => p.y < cv.height + 40);
      if (t < until && parts.length) {
        raf = requestAnimationFrame(draw);
      } else {
        ctx.clearRect(0, 0, cv.width, cv.height);
        parts = [];
      }
    };
    const burst = () => {
      if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
      const cv = ref.current;
      if (!cv) return;
      cv.width = window.innerWidth;
      cv.height = window.innerHeight;
      const n = 160;
      for (let i = 0; i < n; i++) {
        const fromLeft = i % 2 === 0;
        parts.push({
          x: fromLeft ? 0 : cv.width,
          y: cv.height * 0.7,
          vx: (fromLeft ? 1 : -1) * (4 + Math.random() * 9),
          vy: -(8 + Math.random() * 10),
          r: Math.random() * Math.PI,
          s: (Math.random() - 0.5) * 0.3,
          c: COLORS[i % COLORS.length],
          w: 6 + Math.random() * 8,
        });
      }
      until = performance.now() + 2400;
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(draw);
    };
    window.addEventListener('devcules:confetti', burst);
    return () => {
      window.removeEventListener('devcules:confetti', burst);
      cancelAnimationFrame(raf);
    };
  }, []);
  return <canvas ref={ref} aria-hidden="true" className="pointer-events-none fixed inset-0 z-[70]" />;
}
