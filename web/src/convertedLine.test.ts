import { test } from "node:test";
import assert from "node:assert/strict";
import { convertedLine } from "./convertedLine.ts";

const day = (d: string, recorded: boolean, completed = 0, skipped = 0) => ({ day: d, recorded, completed, skipped });

test("fewer than two recorded days: no line (the empty design line shows instead)", () => {
  assert.equal(convertedLine([day("a", false), day("b", true, 5, 1)]), null);
  assert.equal(convertedLine([]), null);
});

test("two or more recorded days: the average per day, and today's figures from the last day", () => {
  const l = convertedLine([day("a", false), day("b", true, 60, 0), day("c", true, 81, 2), day("d", true, 70, 1)]);
  assert.deepEqual(l, { perDay: 70, today: { done: 70, skipped: 1 } });
});

test("the last day not recorded: per day only, no today figures", () => {
  const l = convertedLine([day("a", true, 10), day("b", true, 20), day("c", false)]);
  assert.deepEqual(l, { perDay: 15, today: null });
});
