import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { Series } from "../api/types.ts";

// The draft's 30-day stacked bars (dashboard.html draw()): same geometry, measured from the container width.
const DAYS = 30;
const H = 64;
const TOP = 6;
const BASE = H - 6;

export function Bars({ series }: { series: Series[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const [W, setW] = useState(600);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setW(el.clientWidth || 600);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const bw = W / DAYS;
  const barW = Math.min(14, bw * 0.7);
  const sums = Array.from({ length: DAYS }, (_, i) => series.reduce((a, s) => a + (s.v[i] ?? 0), 0));
  const max = Math.max(...sums, 1);
  const rects: ReactNode[] = [];

  for (let i = 0; i < DAYS; i++) {
    const x = i * bw + (bw - barW) / 2;
    if (series[0].v[i] == null) {
      rects.push(<rect key={`${i}-empty`} x={x} y={BASE - 2} width={barW} height={2} fill="var(--border)" />);
      continue;
    }
    let y = BASE;
    series.forEach((s, k) => {
      const h = ((BASE - TOP) * (s.v[i] ?? 0)) / max;
      if (h <= 0) return;
      y -= h;
      rects.push(
        <rect
          key={`${i}-${k}`}
          x={x}
          y={y}
          width={barW}
          height={Math.max(1, h - (k ? 1 : 0))}
          rx={k === series.length - 1 ? 2 : 0}
          fill={`var(--${s.c})`}
        />,
      );
    });
  }

  return (
    <div className="bars" ref={ref}>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="last 30 days">
        <line x1={0} x2={W} y1={BASE} y2={BASE} stroke="var(--strong)" />
        {rects}
      </svg>
    </div>
  );
}
