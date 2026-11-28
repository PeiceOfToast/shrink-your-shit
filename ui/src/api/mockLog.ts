// The mock System log (page 12 "Data and API"). Lines are the app-wide events of system/logging.md §3.
import type { LogEvent, LogKind, LogLevel, LogPage, LogParams, LogSeen } from "./types.ts";
import { noLibrariesYet, onForcedStateChange, respond } from "./mockState.ts";
import { minsAgo } from "./fixtures/clock.ts";
import { clockPrecise } from "../format/time.ts";
import { notifySummaryChanged } from "./summaryBus.ts";

export const LOG_PAGE_SIZE = 20;

// GET /api/v2/log/sources: a fixed list from the server, so a new worker appears without a page change.
export const LOG_SOURCES = ["Scanner", "This computer", "NAS box", "Office PC", "Web", "Updates", "Notifications", "Health check"];

type Draft = {
  ago: number;
  level: LogLevel;
  kind: LogKind;
  source: string;
  message: string;
  jobId?: string;
  libraryId?: string;
  workerId?: string;
  nextAction?: string;
  repeat?: { count: number; firstAgo: number; lastAgo: number };
};

const d = (ago: number, level: LogLevel, kind: LogKind, source: string, message: string, rest: Partial<Draft> = {}): Draft => ({
  ago,
  level,
  kind,
  source,
  message,
  ...rest,
});

// Oldest first. Times are minutes before the mock loaded, as the other fixtures are.
const DRAFTS: Draft[] = [
  d(300, "info", "note", "Updates", "Shrink Your Shit started. Version 1.4.0."),
  d(298, "info", "note", "Scanner", "Scan started: Movies.", { libraryId: "movies" }),
  d(296, "info", "note", "Scanner", "Scan finished: Movies. 3 new files.", { libraryId: "movies" }),
  d(285, "info", "note", "Web", "Signed in from 10.10.2.52."),
  d(270, "warn", "note", "Web", "Sign-in failed from 10.10.2.99. The password didn't match."),
  d(250, "info", "note", "Scanner", "Scan started: TV Shows.", { libraryId: "tv" }),
  d(240, "warn", "warning", "Scanner", "Can't read /media/tv/Archive: permission denied.", {
    libraryId: "tv",
    repeat: { count: 41, firstAgo: 240, lastAgo: 200 },
  }),
  d(220, "info", "note", "Scanner", "Scan finished: TV Shows. 12 new files, 1 changed.", { libraryId: "tv" }),
  d(200, "info", "queued", "Scanner", "Queued 12 files from TV Shows.", { libraryId: "tv" }),
  d(180, "info", "note", "NAS box", "NAS box joined.", { workerId: "nas-box" }),
  d(150, "warn", "requeued", "NAS box", "NAS box stopped answering. Its job went back to the queue.", {
    workerId: "nas-box",
    jobId: "j2",
  }),
  d(140, "error", "error", "This computer", "Wedding.Raw.2018.mov failed at Convert video: the file couldn't be read.", {
    workerId: "this-computer",
    jobId: "j3",
    nextAction: "Check the file, then retry it from Jobs.",
  }),
  d(120, "info", "note", "Health check", "Health check passed."),
  d(100, "error", "notified", "Notifications", "Couldn't send the notification to Discord.", {
    nextAction: "Check the webhook address in Settings › Notifications.",
  }),
  d(90, "info", "note", "Web", "Settings changed: Processing."),
  d(80, "warn", "note", "Office PC", "Office PC went offline.", { workerId: "office-pc" }),
  d(60, "info", "notified", "Notifications", "Notification sent: 14 files converted."),
  d(45, "info", "note", "Updates", "Update check: 1.4.0 is the newest version."),
  d(40, "warn", "warning", "Scanner", "The Home videos share isn't connected. Nothing from it is scanned until it is.", {
    libraryId: "home-videos",
  }),
  d(30, "error", "error", "Scanner", "Scan of Home videos stopped: the share isn't connected.", {
    libraryId: "home-videos",
    nextAction: "Reconnect the share from Libraries.",
  }),
  d(25, "debug", "note", "Scanner", "Checked 1,204 files in Movies.", { libraryId: "movies" }),
  d(20, "error", "error", "NAS box", "The.Bear.S03E02.1080p.WEB-DL.mkv failed: the new file was shorter than the original.", {
    workerId: "nas-box",
    jobId: "j11",
    nextAction: "The original is kept. Retry it from Jobs.",
  }),
  d(10, "error", "error", "Health check", "Health check failed: NAS box shares. The NAS box has been unreachable since 22:10."),
  d(5, "info", "note", "Web", "Signed in from 10.10.2.52."),
  d(2, "info", "queued", "Scanner", "Queued 3 files from Movies.", { libraryId: "movies" }),
];

function toEvent(x: Draft, seq: number): LogEvent {
  return {
    seq,
    ts: minsAgo(x.ago),
    level: x.level,
    kind: x.kind,
    source: x.source,
    message: x.message,
    jobId: x.jobId ?? null,
    libraryId: x.libraryId ?? null,
    workerId: x.workerId ?? null,
    ...(x.nextAction ? { nextAction: x.nextAction } : {}),
    ...(x.repeat ? { repeat: { count: x.repeat.count, firstAt: minsAgo(x.repeat.firstAgo), lastAt: minsAgo(x.repeat.lastAgo) } } : {}),
  };
}

const BASE: LogEvent[] = DRAFTS.map((x, i) => toEvent(x, i + 1));
// Lines that arrive while the page follows the log.
const LIVE: Draft[] = [
  d(0, "info", "note", "Scanner", "Scan finished: TV Shows. 0 new files.", { libraryId: "tv" }),
  d(0, "error", "error", "Health check", "Health check failed: NAS box shares. The NAS box has been unreachable since 22:10."),
  d(0, "info", "note", "This computer", "Converting started.", { workerId: "this-computer" }),
];
let streamed: LogEvent[] = [];
let nextSeq = BASE.length + 1;

const all = (): LogEvent[] => [...BASE, ...streamed];

// The filter every line goes through. The level is skipped when counting, so each level shows its own count.
function keep(e: LogEvent, p: LogParams, skipLevel: boolean): boolean {
  if (e.level === "debug" && !p.detail) return false;
  if (p.since != null && e.seq <= p.since) return false;
  if (!skipLevel && p.level && e.level !== p.level) return false;
  if (p.source && e.source !== p.source) return false;
  if (p.library && e.libraryId !== p.library) return false;
  if (p.worker && e.workerId !== p.worker) return false;
  if (p.from && Date.parse(e.ts) < Date.parse(p.from)) return false;
  if (p.to && Date.parse(e.ts) > Date.parse(p.to)) return false;
  if (p.q && !e.message.toLowerCase().includes(p.q.toLowerCase())) return false;
  return true;
}

export function getLog(params: LogParams = {}): Promise<LogPage> {
  return respond(() => {
    if (noLibrariesYet()) return { items: [], counts: { info: 0, warn: 0, error: 0 } };
    const counts = { info: 0, warn: 0, error: 0 };
    for (const e of all()) {
      if (keep(e, params, true) && e.level !== "debug") counts[e.level]++;
    }
    const hit = all().filter((e) => keep(e, params, false) && (params.before == null || e.seq < params.before));
    const page = hit.slice(-(params.limit ?? LOG_PAGE_SIZE));
    // As the server does: the cursor is the oldest raw line of a full page, so the next older page starts before it.
    return page.length === (params.limit ?? LOG_PAGE_SIZE) ? { items: page, counts, nextBefore: page[0].seq } : { items: page, counts };
  });
}

export function getLogSources(): Promise<string[]> {
  return respond(() => LOG_SOURCES);
}

// GET /api/v2/log/download: the lines matching the filter, as text in the Job log's download format.
export function getLogDownload(params: LogParams = {}): Promise<string> {
  return respond(() => {
    const lines = all()
      .filter((e) => keep(e, params, false))
      .map((e) => `${clockPrecise(e.ts)} ${e.level.toUpperCase()} ${e.source} ${e.message}${e.repeat ? ` (the same ${e.repeat.count - 1} more times)` : ""}`);
    return lines.join("\n") + "\n";
  });
}

// The events stream while the page follows the log: one new line every few seconds.
export function subscribeAppEvents(cb: (e: LogEvent) => void): () => void {
  let i = 0;
  const timer = setInterval(() => {
    const x = LIVE[i++ % LIVE.length];
    const e = { ...toEvent({ ...x, ago: 0 }, nextSeq++), ts: new Date().toISOString() };
    streamed = [...streamed, e];
    cb(e);
  }, 4000);
  return () => clearInterval(timer);
}

// POST /api/v2/log/seen {seq}: opening the page clears the status line's new errors up to that seq.
// The seen line starts at the 22:08 health line (seq 13), so the fixture errors after it (22:28 on) are the new ones.
const START_SEEN = 13;
let seenSeq = START_SEEN;
export function getSeenSeq(): number {
  return seenSeq;
}
export function markLogSeen(seq: number): Promise<LogSeen> {
  return respond(() => {
    seenSeq = Math.max(seenSeq, seq);
    notifySummaryChanged();
    return { seenSeq };
  });
}

// The time of the line the log was last opened past (Summary.seenAt). Null while seenSeq is 0.
export function getSeenAt(): string | null {
  const seq = getSeenSeq();
  return seq ? all().find((e) => e.seq === seq)?.ts ?? null : null;
}

// The status line's "N new errors": error lines after the last seen seq.
export function newErrorCount(): number {
  return all().filter((e) => e.level === "error" && e.seq > seenSeq).length;
}

onForcedStateChange(() => {
  seenSeq = START_SEEN;
});
