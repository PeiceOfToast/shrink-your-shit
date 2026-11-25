// The Dashboard tiles' sparklines as bars, one per day (user 11-25; design-direction-tile-bars). Day slot = W / n; the bar is
// 70% of the slot, centred, at least 1 px. A day not recorded (null) is a 2 px tick on the baseline, never a zero bar. A
// recorded zero draws no bar; its tip says "0 B saved", as the value would.

export type SparkBar = { i: number; x: number; y: number; w: number; h: number };

// Bars and ticks for a W x H box. The tallest recorded day reaches 2 px from the top; the baseline sits 2 px above the bottom, on the baseline line (the line draws over the bars' feet).
export function sparkBars(values: (number | null)[], W: number, H: number): { bars: SparkBar[]; ticks: number[] } {
  const n = values.length, base = H - 2, top = 2;
  const max = Math.max(1, ...values.map((v) => v ?? 0));
  const slot = W / Math.max(1, n), w = Math.max(1, slot * 0.7);
  const bars: SparkBar[] = [], ticks: number[] = [];
  values.forEach((v, i) => {
    if (v == null) return void ticks.push(i);
    const h = ((base - top) * v) / max;
    if (h > 0) bars.push({ i, x: i * slot + (slot - w) / 2, y: base - h, w, h });
  });
  return { bars, ticks };
}

// A bar's outline: the top corners rounded to at most 2 px, square below 3 px wide (the design's rule).
export function barPath({ x, y, w, h }: SparkBar): string {
  const r = w >= 3 ? Math.min(2, h, w / 2) : 0, b = y + h;
  if (!r) return `M${x},${b}V${y}H${x + w}V${b}Z`;
  return `M${x},${b}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${b}Z`;
}

// The tip's value for one day: "3.2 GB saved" (or "smaller" when kept), "14 files converted", or "not recorded".
export const sparkValue = (value: number | null, text: (n: number) => string) => (value == null ? "not recorded" : text(value));
