import { test } from "node:test";
import assert from "node:assert/strict";
import { getStartedSteps, nextStep, showGetStarted } from "./getStarted.ts";

test("none done: all three steps open, the first is live, the section shows", () => {
  const s = getStartedSteps(0, 0, 0);
  assert.deepEqual(s, [false, false, false]);
  assert.equal(showGetStarted(s), true);
  assert.equal(nextStep(s), 0);
});

test("some done: done steps are marked, the next one not done is live", () => {
  const s = getStartedSteps(2, 1, 0);
  assert.deepEqual(s, [true, true, false]);
  assert.equal(showGetStarted(s), true);
  assert.equal(nextStep(s), 2);
});

test("a library but no workflow: the workflow step is live, not the worker", () => {
  const s = getStartedSteps(1, 0, 1);
  assert.deepEqual(s, [true, false, true]);
  assert.equal(nextStep(s), 1);
});

test("all done: the section hides and no step is live", () => {
  const s = getStartedSteps(1, 1, 3);
  assert.deepEqual(s, [true, true, true]);
  assert.equal(showGetStarted(s), false);
  assert.equal(nextStep(s), null);
});

test("counts above zero count as done; a worker counts only when on", () => {
  assert.deepEqual(getStartedSteps(5, 2, 0), [true, true, false]);
});
