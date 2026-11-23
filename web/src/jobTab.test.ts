import { test } from "node:test";
import assert from "node:assert/strict";
import { tabForState } from "./jobTab.ts";

test("a done job's row is in the Completed tab", () => {
  assert.equal(tabForState("completed"), "completed");
});

test("a failed job's row is in the Failed tab", () => {
  assert.equal(tabForState("failed"), "failed");
});

test("skipped and cancelled jobs are in the Skipped tab", () => {
  assert.equal(tabForState("skipped"), "skipped");
  assert.equal(tabForState("cancelled"), "skipped");
});

test("a running or unknown job has no tab to look in", () => {
  assert.equal(tabForState("running"), null);
  assert.equal(tabForState(undefined), null);
});
