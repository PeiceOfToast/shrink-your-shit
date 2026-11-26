import type { Job } from "../api/types.ts";

// The one place time text is built. The server (and the mock) sends RFC 3339 UTC strings, durations in
// whole milliseconds; every screen formats them here, in the browser's local time.

const MIN = 60_000;
const DAY = 86_400_000;
const pad = (n: number) => String(n).padStart(2, "0");

const startOfDay = (t: number) => {
  const d = new Date(t);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
};

// "14:02"
export function clock(iso: string): string {
  const d = new Date(iso);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// "14:02:10.204"
export function clockPrecise(iso: string): string {
  const d = new Date(iso);
  return `${clock(iso)}:${pad(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, "0")}`;
}

// "today 14:02", "yesterday 22:10", "tomorrow 02:00", or "16 Nov 14:02" for other days.
export function dayClock(iso: string, now = Date.now()): string {
  const diff = Math.round((startOfDay(Date.parse(iso)) - startOfDay(now)) / DAY);
  const word =
    diff === 0 ? "today"
    : diff === -1 ? "yesterday"
    : diff === 1 ? "tomorrow"
    : new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "short" });
  return `${word} ${clock(iso)}`;
}

// "just now", "4 min ago", then the day and clock for anything older than an hour.
export function ago(iso: string, now = Date.now()): string {
  const min = Math.floor((now - Date.parse(iso)) / MIN);
  if (min < 1) return "just now";
  if (min < 60) return `${min} min ago`;
  return dayClock(iso, now);
}

// "0:42", "11:48", "1:58:12" for an elapsed time in milliseconds.
export function duration(ms: number): string {
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
}

// "3 min left" (rounded up), or null when there is no estimate.
export function timeLeft(ms: number | null): string | null {
  if (ms == null) return null;
  if (ms < MIN) return "under a minute left";
  return `${Math.ceil(ms / MIN)} min left`;
}

// The When column: "from 14:02" while running, "queued 13:20", "today 13:10" when skipped, "took 6:20" when finished.
export function jobWhen(job: Job): string {
  switch (job.state) {
    case "running":
      return job.startedAt ? `from ${clock(job.startedAt)}` : "";
    case "waiting":
      return job.queuedAt ? `queued ${clock(job.queuedAt)}` : "";
    case "skipped":
      return job.finishedAt ? dayClock(job.finishedAt) : "";
    default:
      return job.tookMs != null ? `took ${duration(job.tookMs)}` : "";
  }
}
