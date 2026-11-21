// The drawer's wording (dev: `npm test`, Node 22.6+ with --experimental-strip-types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { afterHeader, queuedLine } from "./jobResult.ts";

test("a kept job's Result column says the new file was not kept", () => {
  assert.equal(afterHeader(true), "New file (not kept)");
});

test("a done job's Result column stays After", () => {
  assert.equal(afterHeader(false), "After");
});

test("a workflow changed after queueing shows the version line", () => {
  assert.equal(queuedLine({ version: 4, queuedVersion: 3 }), "The workflow changed after this job was queued (v3 → v4).");
});

test("no queuedVersion, no line", () => {
  assert.equal(queuedLine({ version: 4 }), null);
  assert.equal(queuedLine(undefined), null);
});
