// The Dashboard's bar charts share one keyboard and tip model (design-direction-tile-bars): the sparkline in a tile and the
// 30-day bars in a card. Pure, so the rules are tested without a DOM; the component owns the focus and the tip.

// The day a key moves to: ←/→ one day (clamped), Home/End the first and last day, Escape hides the tip (focus stays).
// null is a key the charts don't use.
export function keyTarget(key: string, cur: number, n: number): number | "hide" | null {
  const last = n - 1, at = (i: number) => Math.min(last, Math.max(0, i));
  switch (key) {
    case "ArrowRight": return at(cur + 1);
    case "ArrowLeft": return at(cur - 1);
    case "Home": return 0;
    case "End": return last;
    case "Escape": return "hide";
    default: return null;
  }
}

export type Row = { label: string; text: string; color?: string };

// One day's rows for the tip and the live text: each part with its value, "none" for a recorded zero (as DayBars' tip read),
// one "not recorded" row for a day the server didn't record.
export function dayRows(recorded: boolean, parts: { label: string; value: number; color?: string }[], fmt: (n: number) => string): Row[] {
  if (!recorded) return [{ label: "", text: "not recorded" }];
  return parts.map((p) => ({ label: p.label, text: p.value ? fmt(p.value) : "none", color: p.color }));
}

// The polite live text: "Nov 21, Freed 1.9 GB, Waiting in the trash 0.6 GB, Frees from Nov 28".
export function dayLine(day: string, rows: Row[], extra?: string | null): string {
  return [day, ...rows.map((r) => (r.label ? r.label + " " + r.text : r.text)), ...(extra ? [extra] : [])].join(", ");
}
