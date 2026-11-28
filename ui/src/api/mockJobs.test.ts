// Convert again (API: a finished job makes a new job; the old one keeps its record).
import assert from "node:assert/strict";
import { test } from "node:test";
import { convertAgain, getJob, getJobEvents, getJobHistory, runJobsAction } from "./mock.ts";
import { JOBS } from "./fixtures/jobs.ts";
import { ApiError } from "./errors.ts";
import { jobDetail } from "./fixtures/jobDetail.ts";

test("convert again on a done job makes a new job with a new id, and the old job is unchanged", async () => {
  const done = JOBS.find((j) => j.state === "done");
  assert.ok(done, "the fixtures have a done job");
  const { jobId } = await convertAgain(done.id);
  assert.notEqual(jobId, done.id);
  const fresh = await getJob(jobId);
  assert.equal(fresh.job.state, "waiting");
  assert.equal(fresh.job.result.outcome, null);
  assert.equal((await getJob(done.id)).job.state, "done");
});

test("convert again on a job that isn't finished is refused", async () => {
  const open = JOBS.find((j) => j.state === "waiting" || j.state === "running" || j.state === "failed");
  assert.ok(open, "the fixtures have an unfinished job");
  await assert.rejects(convertAgain(open.id), (e: unknown) => e instanceof ApiError && e.code === "not_finished");
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

