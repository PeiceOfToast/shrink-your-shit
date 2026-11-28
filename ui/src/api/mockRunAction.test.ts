// The mock's Needs attention action, through runAction: a known job is done, an unknown one is refused the way the server
// refuses it (job_not_found), and a mix shows the line the real client shows.
import { test } from "node:test";
import assert from "node:assert/strict";
import { runAction } from "./mock.ts";
import { ApiError } from "./errors.ts";

const retry = (ids: string[]) => runAction("POST", "/api/v2/jobs/actions", { action: "retry", ids });

test("a retry of a known job resolves with nothing to show", async () => {
  assert.deepEqual(await retry(["j11"]), {});
});

test("a retry of an unknown job rejects with the server's job_not_found", async () => {
  await assert.rejects(retry(["missing-job"]), (e: unknown) => e instanceof ApiError && e.code === "job_not_found" && e.message === "That job doesn't exist.");
});

test("a retry of a known and an unknown job shows the line that says what could not be retried", async () => {
  assert.deepEqual(await retry(["j11", "missing-job"]), { line: "1 retrying. 1 couldn't be: That job doesn't exist." });
});
