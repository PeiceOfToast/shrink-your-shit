// The first-run wizard's step indicator states (design start-step1..3).
import { test } from "node:test";
import assert from "node:assert/strict";
import { stepStates } from "./startSteps.ts";

test("step 1 in front: the first is current, the other two to do", () => {
  assert.deepEqual(stepStates(1), ["current", "todo", "todo"]);
});

test("step 2 in front: the library is done", () => {
  assert.deepEqual(stepStates(2), ["done", "current", "todo"]);
});

test("step 3 in front: the first two are done", () => {
  assert.deepEqual(stepStates(3), ["done", "done", "current"]);
});
