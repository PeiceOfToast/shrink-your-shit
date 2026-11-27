import { test } from "node:test";
import assert from "node:assert/strict";
import { getBitrateLadder } from "../api/mockFlow.ts";
import { qualityCrf } from "./quality.ts";

const ladder = await getBitrateLadder();

test("processor CRF at 1080p is the slider number, plus the format offset", () => {
  assert.equal(qualityCrf(ladder, 3, "hevc", false, "1080"), 23);
  assert.equal(qualityCrf(ladder, 3, "h264", false, "1080"), 18);
  assert.equal(qualityCrf(ladder, 3, "av1", false, null), 32);
});

test("720p is one lower, 2160p one higher, from the row shift", () => {
  assert.equal(qualityCrf(ladder, 3, "hevc", false, "720"), 22);
  assert.equal(qualityCrf(ladder, 3, "hevc", false, "2160"), 24);
  assert.equal(qualityCrf(ladder, 3, "hevc", false, "480"), 22);
});

test("HEVC on the Intel card adds the card offset for the position", () => {
  assert.equal(qualityCrf(ladder, 1, "hevc", true, "1080"), 25);
  assert.equal(qualityCrf(ladder, 3, "hevc", true, "1080"), 22);
  assert.equal(qualityCrf(ladder, 3, "hevc", true, "2160"), 23);
});

test("the card offset is HEVC only", () => {
  assert.equal(qualityCrf(ladder, 3, "h264", true, "1080"), 18);
});
