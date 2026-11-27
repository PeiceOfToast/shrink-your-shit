import { test } from "node:test";
import assert from "node:assert/strict";
import { getBitrateLadder } from "../api/mockFlow.ts";
import { cardFamilies, qualityCrf, qualityMeaning } from "./quality.ts";

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

test("cardFamilies lists each family the workers have once, in a fixed order, and skips no-card workers", () => {
  assert.deepEqual(cardFamilies([]), []);
  assert.deepEqual(cardFamilies([{ gpu: null }, { gpu: null }]), []);
  assert.deepEqual(cardFamilies([{ gpu: { family: "qsv" } }, { gpu: null }, { gpu: { family: "qsv" } }]), ["qsv"]);
  assert.deepEqual(cardFamilies([{ gpu: { family: "nvenc" } }, { gpu: { family: "qsv" } }]), ["qsv", "nvenc"]);
});

test("the Quality line gives one card number per family found", () => {
  const nvidia = { ...ladder, cardOffsets: { ...ladder.cardOffsets, nvenc: { "3": -3 } } };
  const both = qualityMeaning(nvidia, 3, "hevc", ["qsv", "nvenc"], null);
  assert.match(both, /Intel \(QSV\) 22, NVIDIA \(NVENC\) 20;/);
  assert.match(both, /otherwise the processor CRF 23 at 1080p/);
  const qsvOnly = qualityMeaning(nvidia, 3, "hevc", ["qsv"], null);
  assert.match(qsvOnly, /Intel \(QSV\) 22;/);
  assert.doesNotMatch(qsvOnly, /NVENC/);
});

test("the Quality line names no card when none is used or the format is not HEVC", () => {
  assert.equal(qualityMeaning(ladder, 3, "hevc", [], null),"Uses the processor CRF 23 at 1080p (one lower at 720p and below, one higher at 2160p). Lower means a better picture and a bigger file.");
  assert.doesNotMatch(qualityMeaning(ladder, 3, "h264", ["qsv"], null), /card/);
  assert.match(qualityMeaning(ladder, 3, "h264", ["qsv"], null), /^Uses the processor CRF 18 /);
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
