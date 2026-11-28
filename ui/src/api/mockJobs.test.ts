// Convert again (API: a finished job runs again as the same job, with a new attempt).
import assert from "node:assert/strict";
import { test } from "node:test";
import { convertAgain, getJob, getJobEvents, getJobHistory, getJobNeighbours, getJobs, runJobsAction } from "./mock.ts";
import { JOBS } from "./fixtures/jobs.ts";
import { ApiError } from "./errors.ts";
import { jobDetail } from "./fixtures/jobDetail.ts";

test("convert again on a done job runs the same job again: same id, waiting, result cleared", async () => {
  const done = JOBS.find((j) => j.state === "done");
  assert.ok(done, "the fixtures have a done job");
  const d = await convertAgain(done.id);
  assert.equal(d.job.id, done.id);
  assert.equal(d.job.state, "waiting");
  assert.equal(d.job.result.outcome, null);
  assert.equal((await getJob(done.id)).job.state, "waiting");
});

test("convert again on a job that isn't finished is refused with the server's code", async () => {
  const open = JOBS.find((j) => j.state === "waiting" || j.state === "running" || j.state === "failed");
  assert.ok(open, "the fixtures have an unfinished job");
  await assert.rejects(convertAgain(open.id), (e: unknown) => e instanceof ApiError && e.code === "not_runnable");
});

test("the job log is flat events, oldest first, from the job's logged steps", async () => {
  const withLog = JOBS.find((j) => jobDetail(j).logs?.length);
  assert.ok(withLog, "the fixtures have a job with a log");
  const events = await getJobEvents(withLog.id);
  assert.equal(events[0].kind, "run_start");
  assert.equal(events[events.length - 1].kind, "run_end");
  assert.ok(events.some((e) => e.kind === "step_end"), "each step ends in step_end");
  assert.deepEqual(events.map((e) => e.seq), events.map((_, i) => i + 1));
});

test("a job with no log has no events; history is the job's history lines", async () => {
  const noLog = JOBS.find((j) => !jobDetail(j).logs);
  if (noLog) assert.deepEqual(await getJobEvents(noLog.id), []);
  const any = JOBS[0];
  assert.deepEqual(await getJobHistory(any.id), jobDetail(any).history);
});

test("events and history for an unknown job say so", async () => {
  await assert.rejects(getJobEvents("nope"), (e: unknown) => e instanceof ApiError && e.code === "job_not_found");
  await assert.rejects(getJobHistory("nope"), (e: unknown) => e instanceof ApiError && e.code === "job_not_found");
});

test("a job action returns the ids that were done and which were refused", async () => {
  assert.deepEqual(await runJobsAction("retry", ["a", "b"]), { done: ["a", "b"], refused: [] });
});


test("Stop leaves the job cancelled, as the server returns it, and Retry puts it back to waiting", async () => {
  const open = JOBS.find((j) => j.state === "waiting")!;
  await runJobsAction("cancel", [open.id]);
  assert.equal((await getJob(open.id)).job.state, "cancelled");
  await runJobsAction("retry", [open.id]);
  assert.equal((await getJob(open.id)).job.state, "waiting");
});

test("a stopped job is left out of every list, as the server does; Retry clears the old result", async () => {
  const open = JOBS.find((j) => j.state === "waiting")!;
  await runJobsAction("cancel", [open.id]);
  for (const state of ["all", "waiting"]) assert.ok(!(await getJobs({ state })).items.some((j) => j.id === open.id), state);
  for (const j of JOBS.filter((x) => x.state !== "cancelled")) {
    const nb = await getJobNeighbours(j.id);
    assert.ok(nb.prev !== open.id && nb.next !== open.id, j.id);
  }
  const failed = JOBS.find((j) => j.state === "failed" && j.result.outcome !== null)!;
  await runJobsAction("retry", [failed.id]);
  const after = (await getJob(failed.id)).job;
  assert.equal(after.state, "waiting");
  assert.equal(after.result.outcome, null);
  assert.equal(after.result.reason, null);
});

test("Move to top puts a waiting job first among the waiting ones and refuses any other job as the server does", async () => {
  const waiting = JOBS.filter((j) => j.state === "waiting");
  const last = waiting[waiting.length - 1];
  assert.ok(waiting.length > 1 && last.id !== waiting[0].id, "the fixtures have several waiting jobs");
  const done = JOBS.find((j) => j.state === "done")!;
  const out = await runJobsAction("top", [last.id, done.id]);
  assert.deepEqual(out.done, [last.id]);
  assert.deepEqual(out.refused, [{ id: done.id, code: "not_movable", message: "Only a waiting job can be moved to the top." }]);
  assert.equal(JOBS.find((j) => j.state === "waiting")!.id, last.id);
});
