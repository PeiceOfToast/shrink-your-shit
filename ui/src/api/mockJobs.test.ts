// Convert again (API: a finished job makes a new job; the old one keeps its record).
import assert from "node:assert/strict";
import { test } from "node:test";
import { convertAgain, getJob } from "./mock.ts";
import { JOBS } from "./fixtures/jobs.ts";
import { ApiError } from "./errors.ts";

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
