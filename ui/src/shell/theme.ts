// Theme per browser: system, dark or light. Stored in localStorage; any failure falls back to system.
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

export function nextTheme(mode: ThemeMode): ThemeMode {
  return mode === "system" ? "dark" : mode === "dark" ? "light" : "system";
}
