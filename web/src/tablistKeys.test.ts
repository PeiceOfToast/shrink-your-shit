import { test } from "node:test";
import assert from "node:assert/strict";
import { nextTab } from "./tablistKeys.ts";

const none = [false, false, false];

test("Right and Left move one tab", () => {
  assert.equal(nextTab("ArrowRight", 0, none), 1);
  assert.equal(nextTab("ArrowLeft", 2, none), 1);
});

test("the arrows wrap at both ends", () => {
  assert.equal(nextTab("ArrowRight", 2, none), 0);
  assert.equal(nextTab("ArrowLeft", 0, none), 2);
});

test("a disabled tab is skipped, forwards and backwards", () => {
  const d = [false, true, false];
  assert.equal(nextTab("ArrowRight", 0, d), 2);
  assert.equal(nextTab("ArrowLeft", 2, d), 0);
});

test("Home and End go to the first and last tab that is enabled", () => {
  assert.equal(nextTab("Home", 2, none), 0);
  assert.equal(nextTab("End", 0, none), 2);
  assert.equal(nextTab("Home", 2, [true, false, false]), 1);
  assert.equal(nextTab("End", 0, [false, false, true]), 1);
});

test("any other key, or no enabled tab to move to, is null", () => {
  assert.equal(nextTab("Enter", 0, none), null);
  assert.equal(nextTab("ArrowRight", 0, [false, true, true]), null);
});
