// The live wiring with a fake stream and a fake scheduler: the shell reloads the summary on summary frames and refreshes
// every open page on job, scan and library frames and on resync; a job's stream reloads on log lines but not on
// progress. Refreshes are coalesced: a burst is one load per window.
import assert from "node:assert/strict";
import { test } from "node:test";
import { coalesce, startJobWire, startLiveWire } from "./live.ts";
import { onRefresh } from "./refresh.ts";
import type { EventsOptions, Frame } from "./events.ts";

function opener() {
  const made: { opts: EventsOptions | null; closed: boolean } = { opts: null, closed: false };
  const open = (o: EventsOptions) => {
    made.opts = o;
    return () => {
      made.closed = true;
    };
  };
  return { made, open };
}

// Scheduled runs wait until flush(); the window length is recorded.
function scheduler() {
  const queue: (() => void)[] = [];
  const windows: number[] = [];
  const schedule = (fn: () => void, ms: number) => {
    windows.push(ms);
    queue.push(fn);
  };
  const flush = () => {
    while (queue.length) queue.shift()!();
  };
  return { schedule, flush, windows, pending: () => queue.length };
}

const frame = (kind: string): Frame => ({ seq: 1, kind, data: {} });

test("the shell: a summary frame reloads the summary only, after the window", () => {
  const o = opener();
  const s = scheduler();
  let summary = 0;
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => summary++, open: o.open, schedule: s.schedule });
  o.made.opts!.onFrame(frame("summary"));
  assert.equal(summary, 0, "not yet: the window has not run");
  s.flush();
  assert.equal(summary, 1);
  assert.equal(refresh, 0);
  assert.deepEqual(s.windows, [1000]);
  off();
});

test("a burst of job, scan and library frames is one refresh of the open pages", () => {
  const o = opener();
  const s = scheduler();
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => {}, open: o.open, schedule: s.schedule });
  for (let i = 0; i < 5; i++) o.made.opts!.onFrame(frame(i % 2 ? "scan.progress" : "job.state"));
  o.made.opts!.onFrame(frame("progress"));
  assert.equal(s.pending(), 1, "one window for the burst");
  s.flush();
  assert.equal(refresh, 1);
  off();
});

test("a frame after a window runs gets its own window", () => {
  const o = opener();
  const s = scheduler();
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => {}, open: o.open, schedule: s.schedule });
  o.made.opts!.onFrame(frame("job.done"));
  s.flush();
  o.made.opts!.onFrame(frame("library.changed"));
  s.flush();
  assert.equal(refresh, 2);
  off();
});

test("a resync reloads the summary and refreshes every open page, each once per window", () => {
  const o = opener();
  const s = scheduler();
  let summary = 0;
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => summary++, open: o.open, schedule: s.schedule });
  o.made.opts!.onResync();
  o.made.opts!.onResync();
  s.flush();
  assert.equal(summary, 1);
  assert.equal(refresh, 1);
  off();
});

test("a job's log stream reloads once per window on each line, not on progress; it closes with its stop", () => {
  const o = opener();
  const s = scheduler();
  let reloads = 0;
  const stop = startJobWire({ job: "j-1", reload: () => reloads++, open: o.open, schedule: s.schedule });
  assert.equal(o.made.opts!.job, "j-1");
  o.made.opts!.onFrame(frame("run_start"));
  o.made.opts!.onFrame(frame("step_end"));
  o.made.opts!.onFrame(frame("progress"));
  s.flush();
  assert.equal(reloads, 1, "two lines in one window are one reload");
  stop();
  assert.equal(o.made.closed, true);
});

test("coalesce: calls in a window run once, and a call after the run schedules again", () => {
  const s = scheduler();
  let n = 0;
  const soon = coalesce(() => n++, 1000, s.schedule);
  soon();
  soon();
  soon();
  assert.equal(s.pending(), 1);
  s.flush();
  assert.equal(n, 1);
  soon();
  s.flush();
  assert.equal(n, 2);
});
