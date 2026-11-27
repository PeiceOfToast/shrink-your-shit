// The live wiring (shell and job log). Behind the events flag: the app calls these only when isReal("events").
import { openEvents, type EventsOptions, type Frame } from "./events.ts";
import { refreshAll } from "./refresh.ts";

type Opener = (o: EventsOptions) => () => void;

// The shell's stream: a summary frame reloads the status line; job, scan and library frames refresh every open page;
// a resync (or a reconnect after a stale stream) does both.
export function startLiveWire(opts: { reloadSummary: () => void; open?: Opener }): () => void {
  const open = opts.open ?? openEvents;
  return open({
    onFrame: (f: Frame) => {
      if (f.kind === "summary") opts.reloadSummary();
      else if (f.kind.startsWith("job.") || f.kind.startsWith("scan.") || f.kind === "library.changed") refreshAll();
    },
    onResync: () => {
      opts.reloadSummary();
      refreshAll();
    },
  });
}

// One job's stream, while its log is open and it runs: every log line reloads the job (progress is live only, so it
// doesn't reload anything).
export function startJobWire(opts: { job: string; reload: () => void; open?: Opener }): () => void {
  const open = opts.open ?? openEvents;
  return open({
    job: opts.job,
    onFrame: (f: Frame) => {
      if (f.kind !== "progress") opts.reload();
    },
    onResync: opts.reload,
  });
}
