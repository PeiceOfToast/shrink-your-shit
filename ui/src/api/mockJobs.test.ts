// Convert again (API: a finished job runs again as the same job, with a new attempt).
import assert from "node:assert/strict";
import { test } from "node:test";
import { convertAgain, getJob, getJobEvents, getJobHistory, runJobsAction } from "./mock.ts";
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
