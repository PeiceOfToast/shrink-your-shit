// The live wiring (shell and job log). Behind the events flag: the app calls these only when isReal("events").
import { openEvents, type EventsOptions, type Frame } from "./events.ts";
import { refreshAll } from "./refresh.ts";

type Opener = (o: EventsOptions) => () => void;
type Schedule = (fn: () => void, ms: number) => unknown;

// Live refreshes run at most once per window: a burst (a scan's many frames) makes one load, not one per frame. The
// first call in a window schedules the run; calls until it runs are folded into it.
export const REFRESH_WINDOW_MS = 1000;
export function coalesce(fn: () => void, ms = REFRESH_WINDOW_MS, schedule: Schedule = (f, m) => setTimeout(f, m)): () => void {
  let pending = false;
  return () => {
    if (pending) return;
    pending = true;
    schedule(() => {
      pending = false;
      fn();
    }, ms);
  };
}

// The shell's stream: a summary frame reloads the status line; job, scan and library frames refresh every open page;
// a resync (or a reconnect after a stale stream) does both.
export function startLiveWire(opts: { reloadSummary: () => void; open?: Opener; schedule?: Schedule }): () => void {
  const open = opts.open ?? openEvents;
  const summarySoon = coalesce(opts.reloadSummary, REFRESH_WINDOW_MS, opts.schedule);
  const pagesSoon = coalesce(refreshAll, REFRESH_WINDOW_MS, opts.schedule);
  return open({
    onFrame: (f: Frame) => {
      if (f.kind === "summary") summarySoon();
      else if (f.kind.startsWith("job.") || f.kind.startsWith("scan.") || f.kind === "library.changed") pagesSoon();
    },
    onResync: () => {
      summarySoon();
      pagesSoon();
    },
  });
}

// One job's stream, while its log is open and it runs: every log line reloads the job (progress is live only, so it
// doesn't reload anything).
export function startJobWire(opts: { job: string; reload: () => void; open?: Opener; schedule?: Schedule }): () => void {
  const open = opts.open ?? openEvents;
  const soon = coalesce(opts.reload, REFRESH_WINDOW_MS, opts.schedule);
  return open({
    job: opts.job,
    onFrame: (f: Frame) => {
      if (f.kind !== "progress") soon();
    },
    onResync: soon,
  });
}
