// The first-run guard's open pages (dev: `npm test`, Node 22.6+ with --experimental-strip-types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { openBeforeFirstRun } from "./firstRun.ts";

test("the Change links' pages and the wizard's own pages are open before the first run", () => {
  assert.equal(openBeforeFirstRun("/libraries/1"), true);
  assert.equal(openBeforeFirstRun("/workflows/1"), true);
  assert.equal(openBeforeFirstRun("/libraries/new"), true);
  assert.equal(openBeforeFirstRun("/start"), true);
  assert.equal(openBeforeFirstRun("/signin"), true);
});

test("/workflows/new is not a route, and /jobs waits for the first run", () => {
  assert.equal(openBeforeFirstRun("/workflows/new"), false);
  assert.equal(openBeforeFirstRun("/jobs"), false);
  assert.equal(openBeforeFirstRun("/"), false);
});
