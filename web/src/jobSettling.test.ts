import { test } from "node:test";
import assert from "node:assert/strict";
import { SETTLING_WORD, isSettling } from "./jobSettling.ts";

test("a file still changing reads Still changing", () => {
  assert.equal(SETTLING_WORD, "Still changing");
});

test("only a queued row with subState settling is settling", () => {
  assert.equal(isSettling({ state: "queued", subState: "settling" }), true);
  assert.equal(isSettling({ state: "queued", subState: "waiting" }), false);
  assert.equal(isSettling({ state: "queued", subState: "library_disabled" }), false);
  assert.equal(isSettling({ state: "queued", subState: null }), false);
  assert.equal(isSettling({ state: "skipped", subState: "settling" }), false);
});
