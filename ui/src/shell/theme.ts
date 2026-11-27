import { accentVars } from "../format/accent.ts";

// Theme: system, dark or light. The install's choice is saved on the server (settings > appearance); this browser
// caches it so the first paint is right. Any failure falls back to system.
export type ThemeMode = "system" | "dark" | "light";
const KEY = "syp-theme";

export function readTheme(): ThemeMode {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "dark" || v === "light") return v;
  } catch {
    // private window or blocked storage
  }
  return "system";
}

export function applyTheme(mode: ThemeMode): void {
  if (mode === "system") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", mode);
  try {
    localStorage.setItem(KEY, mode);
  } catch {
    // storage unavailable; the choice lasts for this page only
  }
}

// The accent, per theme, as CSS variables on the page (see app.css). Cached per browser so the first paint has it.
const ACCENT_KEY = "syp-accent";
const ACCENT_VARS = ["--acc-dark", "--acc-light", "--on-dark", "--on-light"];

export function readAccent(): string | null {
  try {
    const v = localStorage.getItem(ACCENT_KEY);
    if (v && /^#[0-9a-f]{6}$/i.test(v)) return v;
  } catch {
    // storage unavailable
  }
  return null;
}

// Applies the install's appearance: theme and accent. Status colours are never touched.
export function applyAppearance(a: { theme: ThemeMode; accent: string | null }): void {
  applyTheme(a.theme);
  const style = document.documentElement.style;
  for (const k of ACCENT_VARS) style.removeProperty(k);
  const v = accentVars(a.accent);
  if (v) for (const [k, val] of Object.entries(v.vars)) style.setProperty(k, val);
  try {
    if (a.accent) localStorage.setItem(ACCENT_KEY, a.accent);
    else localStorage.removeItem(ACCENT_KEY);
  } catch {
    // storage unavailable; the choice lasts for this page only
  }
}

export function nextTheme(mode: ThemeMode): ThemeMode {
  return mode === "system" ? "dark" : mode === "dark" ? "light" : "system";
}
