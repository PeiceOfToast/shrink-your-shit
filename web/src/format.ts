// Plain-words formatting shared by the screens.
const GB = 1e9;
export function bytes(n: number): string {
  if (n >= 1e12) return (n / 1e12).toFixed(n >= 1e13 ? 0 : 1) + " TB";
  if (n >= GB) return (n / GB).toFixed(n >= 1e10 ? 0 : 1) + " GB";
  if (n >= 1e6) return Math.round(n / 1e6) + " MB";
  return Math.round(n / 1e3) + " KB";
}
export const count = (n: number) => n.toLocaleString("en-US");
// an ffmpeg graphics encoder (hevc_qsv) by the name the Workers screen gives its type
export const encoderName = (e: string) => ([["_qsv", "Intel Quick Sync"], ["_nvenc", "NVIDIA"], ["_amf", "AMD"], ["_vaapi", "VAAPI"], ["_videotoolbox", "Apple"]].find(([k]) => e.endsWith(k))?.[1] ?? e);
export const clock = (t: string | Date) => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
export const clockS = (t: Date) => t.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
export function elapsed(s: number): string { // 12:08, 1:02:03
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60), p = (x: number) => String(x).padStart(2, "0");
  return h ? `${h}:${p(m)}:${p(sec)}` : `${m}:${p(sec)}`;
}
export const plural = (n: number, one: string, many: string) => `${count(n)} ${n === 1 ? one : many}`;
// A number of seconds in words, design's way (SCREENS V9): "30 seconds", "1 minute", "5 minutes", "1 hour".
export function duration(s: number): string {
  const [n, unit] = s % 3600 === 0 ? [s / 3600, "hour"] : s % 60 === 0 ? [s / 60, "minute"] : [s, "second"];
  return n + " " + unit + (n === 1 ? "" : "s");
}
// Copy text. Plain http (http://IP:8484) has no clipboard API, so the text is selected in a textarea and copied the
// old way (as Jobs' log copy does). True when it worked.
export async function copyText(t: string): Promise<boolean> {
  try { if (navigator.clipboard) { await navigator.clipboard.writeText(t); return true; } } catch { /* refused: fall back */ }
  const back = document.activeElement as HTMLElement | null; // select() moves focus; give it back after
  const a = document.createElement("textarea"); a.value = t; a.readOnly = true; a.style.position = "fixed"; a.style.opacity = "0";
  document.body.appendChild(a); a.select(); let ok = false; try { ok = document.execCommand("copy"); } catch { ok = false; } a.remove(); back?.focus();
  return ok;
}
