// Appearance (DESIGN/v2/THEMES.md, "Settings > Appearance"): kept in this browser in Phase 1 and applied to <html> as
// the data-* attributes and --font-scale that tokens.css reads.
import { useSyncExternalStore } from "react";

export type Theme = "dark" | "light" | "system";
export type Accent = "blue" | "teal" | "amber" | "mono";
export type Density = "comfortable" | "compact";
export type TextSize = 0.9 | 1 | 1.15 | 1.3;
export type Motion = "system" | "reduce";
export interface Appearance {
  theme: Theme;
  accent: Accent;
  density: Density;
  textSize: TextSize;
  motion: Motion;
}

export const THEMES: [Theme, string, string][] = [["dark", "Dark", "i-moon"], ["light", "Light", "i-sun"], ["system", "System", "i-panel"]];
// The swatches show each accent's dark-theme colour (the values tokens.css sets for data-accent).
export const ACCENTS: [Accent, string, string][] = [["blue", "Blue and violet", "#6C8CFF"], ["teal", "Teal", "#3CC8C0"], ["amber", "Amber", "#F0AF4D"], ["mono", "Mono", "#C9D3E0"]];
export const DENSITIES: [Density, string][] = [["comfortable", "Comfortable"], ["compact", "Compact"]];
export const TEXT_SIZES: [TextSize, string][] = [[0.9, "90%"], [1, "100%"], [1.15, "115%"], [1.3, "130%"]];
export const MOTIONS: [Motion, string][] = [["system", "Follow my system"], ["reduce", "Reduce motion"]];
export const DEFAULTS: Appearance = { theme: "dark", accent: "blue", density: "comfortable", textSize: 1, motion: "system" };

const KEY = "syp-appearance";
const one = <T,>(v: unknown, list: [T, ...unknown[]][], d: T): T => (list.some((x) => x[0] === v) ? (v as T) : d);

// What is stored is untrusted (another version, a hand edit): each field falls back to its default on its own.
export function load(): Appearance {
  let raw: Record<string, unknown> = {};
  try {
    raw = JSON.parse(localStorage.getItem(KEY) || "{}") ?? {};
  } catch {
    /* no storage or bad JSON: the defaults */
  }
  return {
    theme: one(raw.theme, THEMES, DEFAULTS.theme),
    accent: one(raw.accent, ACCENTS, DEFAULTS.accent),
    density: one(raw.density, DENSITIES, DEFAULTS.density),
    textSize: one(raw.textSize, TEXT_SIZES, DEFAULTS.textSize),
    motion: one(raw.motion, MOTIONS, DEFAULTS.motion),
  };
}

// Shows a (possibly unsaved) appearance on the page.
export function preview(a: Appearance) {
  const d = document.documentElement;
  const attr = (k: string, v: string | null) => (v == null ? d.removeAttribute(k) : d.setAttribute(k, v));
  attr("data-theme", a.theme === "system" ? null : a.theme); // System: no attribute, tokens.css follows the OS
  attr("data-accent", a.accent === "blue" ? null : a.accent);
  attr("data-density", a.density === "compact" ? "compact" : null);
  attr("data-motion", a.motion === "reduce" ? "reduced" : null);
  d.style.setProperty("--font-scale", String(a.textSize));
}

let saved = load();
const listeners = new Set<() => void>();
preview(saved);

// Keeps an appearance: this browser remembers it, and the whole app shows it.
export function keep(a: Appearance) {
  saved = a;
  try {
    localStorage.setItem(KEY, JSON.stringify(a));
  } catch {
    /* private window or full storage: it still applies until the page is reloaded */
  }
  preview(a);
  listeners.forEach((f) => f());
}

export function useAppearance(): Appearance {
  return useSyncExternalStore(
    (f) => (listeners.add(f), () => listeners.delete(f)),
    () => saved,
  );
}

// The rail's collapsed state is a layout choice, kept beside the appearance.
export function railCollapsed(): boolean {
  try {
    return localStorage.getItem("syp-rail") === "collapsed";
  } catch {
    return false;
  }
}
export function keepRail(collapsed: boolean) {
  try {
    localStorage.setItem("syp-rail", collapsed ? "collapsed" : "open");
  } catch {
    /* not remembered */
  }
}
