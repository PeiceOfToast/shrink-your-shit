// The live wiring with a fake stream: the shell reloads the summary on summary frames and refreshes every open page on
// job, scan and library frames and on resync; a job's stream reloads on log lines but not on progress.
import assert from "node:assert/strict";
import { test } from "node:test";
import { startJobWire, startLiveWire } from "./live.ts";
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
const frame = (kind: string): Frame => ({ seq: 1, kind, data: {} });

test("the shell: a summary frame reloads the summary only", () => {
  const o = opener();
  let summary = 0;
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => summary++, open: o.open });
  o.made.opts!.onFrame(frame("summary"));
  assert.equal(summary, 1);
  assert.equal(refresh, 0);
  off();
});

test("the shell: job, scan and library frames refresh every open page; progress does nothing", () => {
  const o = opener();
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => {}, open: o.open });
  for (const k of ["job.done", "job.state", "scan.progress", "library.changed"]) o.made.opts!.onFrame(frame(k));
  assert.equal(refresh, 4);
  o.made.opts!.onFrame(frame("progress"));
  assert.equal(refresh, 4);
  off();
});

test("the shell: a resync reloads the summary and refreshes every open page", () => {
  const o = opener();
  let summary = 0;
  let refresh = 0;
  const off = onRefresh(() => refresh++);
  startLiveWire({ reloadSummary: () => summary++, open: o.open });
  o.made.opts!.onResync();
  assert.equal(summary, 1);
  assert.equal(refresh, 1);
  off();
});

test("a job's log stream reloads on each line, not on progress, and closes with its stop", () => {
  const o = opener();
  let reloads = 0;
  const stop = startJobWire({ job: "j-1", reload: () => reloads++, open: o.open });
  assert.equal(o.made.opts!.job, "j-1");
  o.made.opts!.onFrame(frame("run_start"));
  o.made.opts!.onFrame(frame("progress"));
  o.made.opts!.onResync();
  assert.equal(reloads, 2);
  stop();
  assert.equal(o.made.closed, true);
});
