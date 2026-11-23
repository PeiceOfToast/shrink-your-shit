import { test } from "node:test";
import assert from "node:assert/strict";
import { bytes, clock, dayClock } from "./format.ts";

test("zero is 0 B, not 0 KB", () => {
  assert.equal(bytes(0), "0 B");
  assert.equal(bytes(2000), "2 KB");
});

test("dayClock: a time today has no day; yesterday and older name the day", () => {
  const now = new Date(2026, 10, 23, 12, 0); // 23 Nov 2026, local
  const at = (d: number, h: number) => new Date(2026, 10, d, h, 52);
  assert.equal(dayClock(at(23, 1), now), clock(at(23, 1)));
  assert.equal(dayClock(at(22, 1), now), "yesterday " + clock(at(22, 1)));
  assert.equal(dayClock(at(21, 1), now), "Nov 21, " + clock(at(21, 1)));
});
