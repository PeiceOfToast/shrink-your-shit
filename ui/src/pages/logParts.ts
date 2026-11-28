// Shared by the Job log (page 03) and the System log (page 12): the level words, copying and saving text.
import type { JobStep, LogCounts, LogEvent, LogLine, LogPage, LogStep } from "../api/types.ts";

export type LogLevelName = "info" | "warn" | "error" | "detail";

export const LEVEL_LABEL: Record<LogLevelName, string> = { info: "INFO", warn: "WARN", error: "ERROR", detail: "DETAIL" };

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// Saves text as a .txt file; both logs' downloads use it.
export function saveText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

// The Job log's steps, built from the job's events (GET /jobs/{id}/events). Real events carry no step index, so the
// nth step_start..step_end block is steps[n-1]. Lines before the first step go at the top of step 1; lines after the
// last reached step. step_start and step_end only mark the blocks, and empty messages are skipped. Only the steps that
// were reached come back: no step_start gives none (the log says "Not started yet"), and a run that failed in step 1
// gives step 1 alone.
export function logsFromEvents(steps: JobStep[], events: LogEvent[]): LogStep[] {
  if (steps.length === 0 || !events.some((e) => e.kind === "step_start")) return [];
  const out: LogStep[] = steps.map((s) => ({ ...s, decision: "", command: null, values: [], lines: [] }));
  let block = -1; // becomes 0 at the first step_start
  for (const e of events) {
    if (e.kind === "step_start") {
      block += 1;
      continue;
    }
    if (e.kind === "step_end") continue;
    const step = out[Math.min(Math.max(block, 0), out.length - 1)];
    if (e.kind === "decision") {
      if (e.message) step.decision = e.message;
      continue;
    }
    if (e.kind === "command") {
      if (e.message) step.command = e.message;
      continue;
    }
    if (!e.message) continue;
    const level: LogLine["level"] = e.level === "warn" || e.level === "error" ? e.level : "info";
    step.lines.push({ time: e.ts, level, text: e.message });
  }
  return out.slice(0, Math.min(block + 1, out.length));
}

// The live lines the page adds. The server folds a repeated line into the newest row (its seq moves up, and its repeat says
// how many times and from when). A folded line that matches a line already shown replaces that line, so it shows once.
// The time a line covers: a fold's run, or the one line's moment. A fold read through a shorter or longer window gets a
// different firstAt, but it always overlaps the same run, so overlap is what identifies a fold.
const span = (l: LogEvent) => ({ from: Date.parse(l.repeat?.firstAt ?? l.ts), to: Date.parse(l.repeat?.lastAt ?? l.ts) });

export function addLiveLine(lines: LogEvent[], e: LogEvent): LogEvent[] {
  if (e.repeat) {
    const mine = { from: Date.parse(e.repeat.firstAt), to: Date.parse(e.repeat.lastAt) };
    const at = lines.findIndex((l) => {
      const theirs = span(l);
      return l.seq !== e.seq && l.message === e.message && l.source === e.source && theirs.from <= mine.to && mine.from <= theirs.to;
    });
    // A late live fold of the same run is older than a fold the page already holds: the newer one stays.
    if (at >= 0) return lines[at].seq > e.seq ? lines : lines.map((l, i) => (i === at ? e : l));
  }
  return [...lines, e];
}

// What the page shows: the loaded lines, with the live lines folded in (a fold of a loaded line replaces it).
// A reload already has the live lines it was taken after, so a live line whose seq the page has is not added again.
export function showLines(loaded: LogEvent[], live: LogEvent[]): LogEvent[] {
  return live.reduce((acc, e) => (acc.some((l) => l.seq === e.seq) ? acc : addLiveLine(acc, e)), loaded);
}

// The level badges: the server's counts for this filter. The live lines are not added to them: a count the server gave
// already includes the lines the page has seen, so adding the live ones counts them twice.
export function levelBadges(server: LogCounts, _live: LogEvent[]): LogCounts {
  return server;
}

// The cursor after an older page: the server's nextBefore. It is set only when the raw page was full, so a page that folds
// short still has its cursor. The page must not page by its first row's seq: a folded row's seq is its newest line.
export function olderCursorAfter(page: LogPage): number | null {
  return page.nextBefore ?? null;
}

// The cursor the next older page starts before: the last older page's, or the first page's when no older page is loaded yet.
export function pagingCursor(olderCursor: number | null | undefined, firstPageNextBefore: number | undefined): number | null {
  return olderCursor !== undefined ? olderCursor : (firstPageNextBefore ?? null);
}

// The Load older button: there may be older lines (the last load was not empty), and the list has lines to hang it on.
export function showLoadOlder(hasMore: boolean, shownCount: number): boolean {
  return hasMore && shownCount > 0;
}

// Follow scrolls to the bottom when the view is following and either a new line arrived at the end, or following was just
// turned on (or un-paused): the newest line may not have changed then, but the view must still go to it.
export function followsBottom(prevNewest: number | undefined, newest: number | undefined, follow: boolean, paused: boolean, wasOn: boolean): boolean {
  return follow && !paused && (newest !== prevNewest || !wasOn);
}

// The scrollTop that keeps the same distance from the bottom after lines are added above.
export function keptScrollTop(scrollHeight: number, distanceFromBottom: number): number {
  return scrollHeight - distanceFromBottom;
}

// The counts the badges show: the live read while there is one, else the last load's, else zeros.
export function shownCounts(live: LogCounts | null | undefined, load: LogCounts | null | undefined): LogCounts {
  return live ?? load ?? { info: 0, warn: 0, error: 0 };
}

// Reads the counts every poll while Follow is on. A read still in flight when it is stopped is dropped: a late answer
// would otherwise set the counts of a filter the page has left. Returns the stop.
export function startCountReads(
  read: () => Promise<LogCounts>,
  set: (c: LogCounts) => void,
  every = 4_000,
  schedule: (fn: () => void, ms: number) => unknown = (fn, ms) => setInterval(fn, ms),
  clear: (t: unknown) => void = (t) => clearInterval(t as ReturnType<typeof setInterval>),
): () => void {
  let stopped = false;
  const t = schedule(() => {
    read().then((c) => {
      if (!stopped) set(c);
    }, () => {});
  }, every);
  return () => {
    stopped = true;
    clear(t);
  };
}

