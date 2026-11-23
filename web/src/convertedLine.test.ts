import { test } from "node:test";
import assert from "node:assert/strict";
import { convertedLine } from "./convertedLine.ts";

const day = (d: string, recorded: boolean, completed = 0) => ({ day: d, recorded, completed });

test("fewer than two recorded days: no line (the empty design line shows instead)", () => {
  assert.equal(convertedLine([day("a", false), day("b", true, 5)]), null);
  assert.equal(convertedLine([]), null);
});

test("two or more recorded days: the average completed per day, rounded", () => {
  assert.equal(convertedLine([day("a", false), day("b", true, 60), day("c", true, 81), day("d", true, 70)]), 70);
});

test("unrecorded days are left out of the average", () => {
  assert.equal(convertedLine([day("a", true, 10), day("b", true, 20), day("c", false)]), 15);
});
