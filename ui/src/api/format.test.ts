import { test } from "node:test";
import assert from "node:assert/strict";
import { formatBytes, formatCount, formatGB } from "./format.ts";

test("formatCount groups thousands", () => {
  assert.equal(formatCount(1284), "1,284");
  assert.equal(formatCount(4812), "4,812");
});

test("formatGB keeps one decimal", () => {
  assert.equal(formatGB(36.44), "36.4 GB");
});

test("formatBytes rounds large sizes", () => {
  assert.equal(formatBytes(412e9), "412 GB");
  assert.equal(formatBytes(61e9), "61 GB");
});
