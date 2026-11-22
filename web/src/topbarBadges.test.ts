// M1: the top bar's converting link and the Workers problem count. Node runs these (npm test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { convertingLabel, problemWorkers } from "./topbarBadges.ts";

test("the converting link is hidden at 0, missing or negative", () => {
  assert.equal(convertingLabel(0), null);
  assert.equal(convertingLabel(undefined), null);
  assert.equal(convertingLabel(-1), null);
});

test("the converting link says the count, singular for one", () => {
  assert.equal(convertingLabel(1), "Converting 1 file");
  assert.equal(convertingLabel(2), "Converting 2 files");
  assert.equal(convertingLabel(12), "Converting 12 files");
});

test("no problem count when no worker has a problem", () => {
  assert.equal(problemWorkers(0), null);
  assert.equal(problemWorkers(null), null);
  assert.equal(problemWorkers(undefined), null);
});

test("the problem count is the number of workers, with its words for screen readers", () => {
  assert.deepEqual(problemWorkers(1), { count: 1, words: "1 worker with a problem" });
  assert.deepEqual(problemWorkers(3), { count: 3, words: "3 workers with a problem" });
});
