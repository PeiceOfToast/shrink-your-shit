import type { Watch } from "./types.ts";

// When a library next scans (pages/09): hourly → the next full hour; daily → the next 02:00; weekly → the next Monday 02:00.
// Local time. Null only while a library is paused, and the caller decides that.
export function nextScanFor(w: Watch, now = new Date()): string {
  const next = new Date(now);
  if (w === "hourly") {
    next.setHours(next.getHours() + 1, 0, 0, 0);
  } else {
    next.setHours(2, 0, 0, 0);
    if (next.getTime() <= now.getTime()) next.setDate(next.getDate() + 1);
    if (w === "weekly") while (next.getDay() !== 1) next.setDate(next.getDate() + 1);
  }
  return next.toISOString();
}
