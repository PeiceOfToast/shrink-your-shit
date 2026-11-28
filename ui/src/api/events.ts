// The live stream (contract section 5): one EventSource on /api/v2/events, or ?job=<id> for one job's log. The browser
// resumes with Last-Event-ID; a resync frame means the server can't replay, so the open page refetches.
// Frames: id = seq, event = kind, data = JSON. The stream has no pings we can see, so a stale stream is one with no frame.
import type { Summary } from "./types.ts";
import { setConnection } from "./connection.ts";

export type Frame = { seq: number; kind: string; data: unknown };

// The kinds stage 1 emits (workshop's answer, Q2), plus the job-log kinds on ?job= and the live progress frame.
export const KINDS = [
  "job.queued", "job.state", "job.done", "job.failed",
  "scan.start", "scan.progress", "scan.end", "library.changed",
  "summary", "resync",
  "run_start", "step_start", "step_end", "probe", "decision", "run_end", "progress",
] as const;

// Parses one SSE message. A frame whose data is not JSON is dropped (null) rather than shown.
export function parseFrame(id: string, kind: string, data: string): Frame | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return null;
  }
  const seq = Number(id);
  return { seq: Number.isFinite(seq) ? seq : 0, kind, data: parsed };
}

// Which views a frame refreshes. progress is live only: it is never stored, and it refreshes no page.
export function viewsFor(kind: string): string[] {
  if (kind.startsWith("job.")) return ["jobs", "overview"];
  if (kind.startsWith("scan.") || kind === "library.changed") return ["libraries", "overview"];
  if (kind === "summary") return ["summary"];
  if (kind === "resync") return ["all"];
  return [];
}

// The reconnect rule: no frame or ping for 45 s.
export const STALE_MS = 45_000;
export function isStale(lastFrameAt: number, now: number, limitMs = STALE_MS): boolean {
  return now - lastFrameAt >= limitMs;
}

export type EventSourceLike = {
  addEventListener(name: string, fn: (e: { lastEventId: string; data: string }) => void): void;
  close(): void;
};
type Ctor = new (url: string) => EventSourceLike;

export type EventsOptions = {
  job?: string;
  onFrame: (f: Frame) => void;
  // A resync frame, or a reconnect after a stale stream: the page refetches what it shows.
  onResync: () => void;
  onSummary?: (s: Summary) => void;
  EventSourceCtor?: Ctor;
  clock?: () => number;
  // Schedules the stale check (default: every 5 s). Injected in tests.
  every?: (fn: () => void, ms: number) => () => void;
  // Schedules the reopen after a failed stream (default: once, after 3 s). Injected in tests.
  later?: (fn: () => void, ms: number) => () => void;
};

// Opens the stream and returns close(). A stale stream is closed and reopened, then resynced.
export function openEvents(opts: EventsOptions): () => void {
  const Ctor: Ctor = opts.EventSourceCtor ?? (globalThis.EventSource as unknown as Ctor);
  const clock = opts.clock ?? (() => Date.now());
  const url = "/api/v2/events" + (opts.job ? `?job=${encodeURIComponent(opts.job)}` : "");
  let source: EventSourceLike | null = null;
  let lastFrameAt = clock();
  let closed = false;
  // A failed stream (the server is down: the proxy answers 502) is not retried by the browser, so it is reopened here.
  // Once it opens again after a failure, the page resyncs: it may have missed frames.
  let down = false;
  let cancelRetry: (() => void) | null = null;
  const later = opts.later ?? ((fn, ms) => {
    const t = setTimeout(fn, ms);
    return () => clearTimeout(t);
  });

  const attach = () => {
    const es = new Ctor(url);
    source = es;
    lastFrameAt = clock();
    // The stream opening, a ping or a frame is the server answering: the connection is up again, with no click needed.
    // A ping is liveness only: it resets the stale timer and reaches no page.
    es.addEventListener("open", () => {
      if (closed || source !== es) return;
      setConnection(true);
      if (down) {
        down = false;
        opts.onResync();
      }
    });
    es.addEventListener("error", () => {
      if (closed || source !== es) return;
      down = true;
      es.close();
      source = null;
      cancelRetry = later(() => {
        cancelRetry = null;
        if (!closed) attach();
      }, 3_000);
    });
    es.addEventListener("ping", () => {
      if (closed || source !== es) return;
      lastFrameAt = clock();
      setConnection(true);
    });
    for (const kind of KINDS) {
      es.addEventListener(kind, (e) => {
        if (closed || source !== es) return;
        lastFrameAt = clock();
        setConnection(true);
        if (kind === "resync") {
          opts.onResync();
          return;
        }
        const f = parseFrame(e.lastEventId, kind, e.data);
        if (!f) return;
        if (kind === "summary" && opts.onSummary) opts.onSummary(f.data as Summary);
        opts.onFrame(f);
      });
    }
  };

  attach();
  const stop = (opts.every ?? ((fn, ms) => {
    const t = setInterval(fn, ms);
    return () => clearInterval(t);
  }))(() => {
    if (closed || !source) return;
    if (isStale(lastFrameAt, clock())) {
      source.close();
      attach();
      opts.onResync();
    }
  }, 5_000);

  return () => {
    closed = true;
    stop();
    cancelRetry?.();
    source?.close();
    source = null;
  };
}
