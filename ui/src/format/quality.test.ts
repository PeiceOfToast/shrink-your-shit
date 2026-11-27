import { test } from "node:test";
import assert from "node:assert/strict";
import { getBitrateLadder } from "../api/mockFlow.ts";
import { qualityCrf } from "./quality.ts";

const ladder = await getBitrateLadder();

test("processor CRF at 1080p is the slider number, plus the format offset", () => {
  assert.equal(qualityCrf(ladder, 3, "hevc", null, "1080"), 23);
  assert.equal(qualityCrf(ladder, 3, "h264", null, "1080"), 18);
  assert.equal(qualityCrf(ladder, 3, "av1", null, null), 32);
});

test("720p is one lower, 2160p one higher, from the row shift", () => {
  assert.equal(qualityCrf(ladder, 3, "hevc", null, "720"), 22);
  assert.equal(qualityCrf(ladder, 3, "hevc", null, "2160"), 24);
  assert.equal(qualityCrf(ladder, 3, "hevc", null, "480"), 22);
});

test("HEVC on the Intel card adds the QSV offset for the position", () => {
  assert.equal(qualityCrf(ladder, 1, "hevc", "qsv", "1080"), 25);
  assert.equal(qualityCrf(ladder, 3, "hevc", "qsv", "1080"), 22);
  assert.equal(qualityCrf(ladder, 3, "hevc", "qsv", "2160"), 23);
});

test("HEVC on an NVIDIA card adds that card's offset, not the QSV one", () => {
  const nvidia = { ...ladder, cardOffsets: { nvenc: { "1": -4, "3": -3 } } };
  assert.equal(qualityCrf(nvidia, 3, "hevc", "nvenc", "1080"), 20);
  assert.equal(qualityCrf(nvidia, 1, "hevc", "nvenc", "1080"), 23);
});

test("a card family the ladder has no offsets for adds nothing", () => {
  assert.equal(qualityCrf(ladder, 3, "hevc", "vaapi", "1080"), 23);
});

test("no card, or the processor, adds no card offset", () => {
  assert.equal(qualityCrf(ladder, 3, "hevc", null, "1080"), 23);
  assert.equal(qualityCrf(ladder, 3, "h264", null, "1080"), 18);
});

test("the card offset is HEVC only", () => {
  assert.equal(qualityCrf(ladder, 3, "h264", "qsv", "1080"), 18);
  assert.equal(qualityCrf({ ...ladder, cardOffsets: { nvenc: { "3": -3 } } }, 3, "h264", "nvenc", "1080"), 18);
});
