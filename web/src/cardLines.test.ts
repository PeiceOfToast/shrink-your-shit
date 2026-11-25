import { test } from "node:test";
import assert from "node:assert/strict";
import { filesFoot, spaceFoot, spaceHeadline } from "./cardLines.ts";

const bytes = (n: number) => (n === 0 ? "0 B" : n >= 1e3 ? Math.round(n / 1e3) + " KB" : n + " B");
const count = (n: number) => n.toLocaleString("en-US");

test("Space saved's headline is the freed bytes, or Nothing saved yet at zero", () => {
  assert.equal(spaceHeadline(0, bytes), "Nothing saved yet");
  assert.equal(spaceHeadline(-5, bytes), "Nothing saved yet");
  assert.equal(spaceHeadline(2048, bytes), "2 KB freed");
});

test("the Space foot names the 30-day total", () => {
  assert.equal(spaceFoot(0, bytes), "0 B in 30 days");
  assert.equal(spaceFoot(2048, bytes), "2 KB in 30 days");
});

test("the Files foot counts done, skipped and failed with thousands separators", () => {
  assert.equal(filesFoot(4812, 38, 9, count), "4,812 done · 38 skipped · 9 failed in 30 days");
});
