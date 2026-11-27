// The accent colour, per theme. One picked colour; each theme uses the nearest shade that reads at 4.5:1 on its cards
// (lighter in dark, darker in light). Text on an accent button is white or near-black, whichever reads better.
// Done, warning and error colours never use this.

export const PRESETS: { name: string; hex: string }[] = [
  { name: "Green", hex: "#22C55E" },
  { name: "Teal", hex: "#14B8A6" },
  { name: "Blue", hex: "#3B82F6" },
  { name: "Indigo", hex: "#6366F1" },
  { name: "Violet", hex: "#8B5CF6" },
  { name: "Pink", hex: "#EC4899" },
  { name: "Slate", hex: "#64748B" },
];

export const CARD = { dark: "#10161D", light: "#FFFFFF" } as const;
const ON_DARK = "#06130B";
const WHITE = "#FFFFFF";
const MIN_CONTRAST = 4.5;

type Rgb = [number, number, number];

function toRgb(hex: string): Rgb {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function toHex(c: Rgb): string {
  return "#" + c.map((v) => Math.round(v).toString(16).padStart(2, "0")).join("").toUpperCase();
}

function luminance(hex: string): number {
  const [r, g, b] = toRgb(hex).map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// The text colour on an accent: white or near-black, whichever reads better.
export function textOn(accent: string): string {
  return contrast(accent, WHITE) >= contrast(accent, ON_DARK) ? WHITE : ON_DARK;
}

// The accent for one theme, moved toward white (dark) or black (light) in small steps until it reads on the card.
export function readableAccent(hex: string, theme: "dark" | "light"): { color: string; moved: boolean } {
  const target: Rgb = theme === "dark" ? [255, 255, 255] : [0, 0, 0];
  const card = CARD[theme];
  let c = toRgb(hex);
  let moved = false;
  for (let step = 0; step < 100 && contrast(toHex(c), card) < MIN_CONTRAST; step++) {
    c = c.map((v, i) => v + (target[i] - v) * 0.05) as Rgb;
    moved = true;
  }
  return { color: toHex(c), moved };
}

// The CSS variables for a chosen accent, or null for the default green (the stylesheet's own values).
export function accentVars(hex: string | null): { vars: Record<string, string>; movedDark: boolean; movedLight: boolean } | null {
  if (!hex) return null;
  const dark = readableAccent(hex, "dark");
  const light = readableAccent(hex, "light");
  return {
    vars: {
      "--acc-dark": dark.color,
      "--acc-light": light.color,
      "--on-dark": textOn(dark.color),
      "--on-light": textOn(light.color),
    },
    movedDark: dark.moved,
    movedLight: light.moved,
  };
}

export function isHex(v: string): boolean {
  return /^#[0-9a-f]{6}$/i.test(v);
}
