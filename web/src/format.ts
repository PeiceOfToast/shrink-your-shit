// Plain-words formatting shared by the screens.
const GB = 1e9;
export function bytes(n: number): string {
  if (n === 0) return "0 B"; // the design's word for nothing ("0 KB" was wrong)
  if (n >= 1e12) return (n / 1e12).toFixed(n >= 1e13 ? 0 : 1) + " TB";
  if (n >= GB) return (n / GB).toFixed(n >= 1e10 ? 0 : 1) + " GB";
  if (n >= 1e6) return Math.round(n / 1e6) + " MB";
  return Math.round(n / 1e3) + " KB";
}
export const count = (n: number) => n.toLocaleString("en-US");
// an ffmpeg graphics encoder (hevc_qsv) by the name the Workers screen gives its type
export const encoderName = (e: string) => ([["_qsv", "Intel Quick Sync"], ["_nvenc", "NVIDIA"], ["_amf", "AMD"], ["_vaapi", "VAAPI"], ["_videotoolbox", "Apple"]].find(([k]) => e.endsWith(k))?.[1] ?? e);
export const clock = (t: string | Date) => new Date(t).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
export const clockS = (t: Date) => t.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" });
// a library's last scan (Libraries and the dashboard): "9:40 AM today", "Yesterday 9:40 AM", "Nov 21 9:40 AM", or "Not yet"
export function lastScan(t: string | null): string {
  if (!t) return "Not yet";
  const d = new Date(t), day = (x: Date) => x.toDateString(), y = new Date(Date.now() - 864e5);
  return day(d) === day(new Date()) ? clock(d) + " today" : day(d) === day(y) ? "Yesterday " + clock(d) : d.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + clock(d);
}
// a day ("2026-11-20") as "Nov 20": the Dashboard and the Libraries page name the same kept-original deadline this way
export const dayName = (d: string) => new Date(d + "T12:00:00").toLocaleDateString(undefined, { day: "numeric", month: "short" }).replace(" ", "\u00a0"); // "Nov 20" never splits
// a time with its day when it isn't today (Needs attention rows): "1:52 AM", "yesterday 1:52 AM", "Nov 21, 1:52 AM"
export function dayClock(t: string | Date, now = new Date()): string {
  const d = new Date(t), y = new Date(now.getTime() - 864e5), day = (x: Date) => x.toDateString();
  if (day(d) === day(now)) return clock(d);
  return day(d) === day(y) ? "yesterday " + clock(d) : d.toLocaleDateString("en-US", { month: "short", day: "numeric" }) + ", " + clock(d);
}
export function elapsed(s: number): string { // 12:08, 1:02:03
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60), p = (x: number) => String(x).padStart(2, "0");
  return h ? `${h}:${p(m)}:${p(sec)}` : `${m}:${p(sec)}`;
}
// durations read '4 min 12 s' and '0.1 s' everywhere (the design's phone note)
export const stepTime = (ms: number) => { const s = ms / 1000; return s < 1 ? s.toFixed(1) + " s" : s < 60 ? Math.round(s) + " s" : Math.floor(s / 60) + " min " + (Math.round(s) % 60) + " s"; };
export const plural = (n: number, one: string, many: string) => `${count(n)} ${n === 1 ? one : many}`;
// A number of seconds in words, design's way (SCREENS V9): "30 seconds", "1 minute", "5 minutes", "1 hour".
export function duration(s: number): string {
  const [n, unit] = s % 3600 === 0 ? [s / 3600, "hour"] : s % 60 === 0 ? [s / 60, "minute"] : [s, "second"];
  return n + " " + unit + (n === 1 ? "" : "s");
}
