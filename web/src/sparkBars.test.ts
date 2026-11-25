import { test } from "node:test";
import assert from "node:assert/strict";
import { barPath, sparkBars, sparkValue } from "./sparkBars.ts";

test("a null day is a tick, not a bar", () => {
  const { bars, ticks } = sparkBars([1, null, 2], 128, 56);
  assert.deepEqual(ticks, [1]);
  assert.deepEqual(bars.map((b) => b.i), [0, 2]);
});

test("a recorded zero draws no bar and no tick", () => {
  const { bars, ticks } = sparkBars([0, 4], 128, 56);
  assert.deepEqual(ticks, []);
  assert.deepEqual(bars.map((b) => b.i), [1]);
});

test("the tallest day reaches 2 px from the top and stands on the baseline at H - 2", () => {
  const { bars } = sparkBars([1, 4, 2], 128, 56);
  const tallest = bars.find((b) => b.i === 1)!;
  assert.equal(tallest.y, 2);
  assert.equal(tallest.y + tallest.h, 54);
  const half = bars.find((b) => b.i === 2)!;
  assert.ok(Math.abs(half.h - (52 * 2) / 4) < 1e-9);
});

test("bar width is 70% of the day slot, centred, at least 1 px", () => {
  const { bars } = sparkBars([1, 1], 128, 56);
  const slot = 64;
  assert.ok(Math.abs(bars[0].w - slot * 0.7) < 1e-9);
  assert.ok(Math.abs(bars[0].x - (slot - slot * 0.7) / 2) < 1e-9);
  const narrow = sparkBars(new Array(500).fill(1), 128, 56).bars[0];
  assert.equal(narrow.w, 1);
});

test("all-zero or all-null series draw nothing scaled", () => {
  assert.deepEqual(sparkBars([0, 0], 128, 56).bars, []);
  assert.deepEqual(sparkBars([null, null], 128, 56).ticks, [0, 1]);
});

test("barPath rounds the top to at most 2 px and is square below 3 px wide", () => {
  const wide = barPath({ i: 0, x: 0, y: 10, w: 8, h: 20 });
  assert.ok(wide.includes("Q0,10 2,10"));
  assert.ok(wide.endsWith("Z"));
  const narrow = barPath({ i: 0, x: 0, y: 10, w: 2, h: 20 });
  assert.ok(!narrow.includes("Q"));
  assert.equal(narrow, "M0,30V10H2V30Z");
});

test("sparkValue reads not recorded for null and the text otherwise, zero included", () => {
  const text = (n: number) => (n === 0 ? "0 B saved" : `${n} B saved`);
  assert.equal(sparkValue(null, text), "not recorded");
  assert.equal(sparkValue(0, text), "0 B saved");
  assert.equal(sparkValue(7, text), "7 B saved");
});
