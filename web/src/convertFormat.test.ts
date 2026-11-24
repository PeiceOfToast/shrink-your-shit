// #697: the format edit on a Convert step: the 10-bit reset and the AV1 rate rule.
import { test } from "node:test";
import assert from "node:assert/strict";
import { customEdit, formatEdit, qualityCap } from "./convertFormat.ts";

test("switching an HEVC 10-bit step to H.264 resets bit depth, in the same update, with its line", () => {
  const r = formatEdit({ format: "hevc", bitDepth: "10" }, "format", "h264");
  assert.ok("bitDepth" in r.x);
  assert.equal(r.x.bitDepth, undefined);
  assert.equal(r.said, "Bit depth set to Keep as is: H.264 can't make 10-bit.");
});

test("switching an HEVC 10-bit step to VP9 resets it too, with VP9 named", () => {
  const r = formatEdit({ format: "hevc", bitDepth: "10" }, "format", "vp9");
  assert.ok("bitDepth" in r.x);
  assert.equal(r.said, "Bit depth set to Keep as is: VP9 can't make 10-bit.");
});

test("switching an HEVC 10-bit step to AV1 keeps 10-bit", () => {
  const r = formatEdit({ format: "hevc", bitDepth: "10" }, "format", "av1");
  assert.ok(!("bitDepth" in r.x));
  assert.equal(r.said, "");
});

test("switching an 8-bit step to H.264 keeps 8-bit", () => {
  const r = formatEdit({ format: "hevc", bitDepth: "8" }, "format", "h264");
  assert.ok(!("bitDepth" in r.x));
  assert.equal(r.said, "");
});

test("switching to AV1 from a bitrate setting takes Quality only, with its line", () => {
  const r = formatEdit({ format: "hevc", rateControl: "level" }, "format", "av1");
  assert.equal(r.x.rateControl, "quality");
  assert.equal(r.said, "Switched to Quality only: AV1 can't use a bitrate.");
});

test("picking Quality only clears a bitrate cap in the same update", () => {
  const r = formatEdit({ format: "hevc", rateControl: "level", maxBitrate: "high" }, "rateControl", "quality");
  assert.equal(r.x.maxBitrate, "none");
});

test("switching to AV1 with a cap set clears the cap too", () => {
  const r = formatEdit({ format: "hevc", rateControl: "level", maxBitrate: "medium" }, "format", "av1");
  assert.equal(r.x.rateControl, "quality");
  assert.equal(r.x.maxBitrate, "none");
});

test("Quality only with no cap writes no cap", () => {
  const r = formatEdit({ format: "hevc", rateControl: "level", maxBitrate: "none" }, "rateControl", "quality");
  assert.ok(!("maxBitrate" in r.x));
});

test("picking Bitrate level keeps the cap", () => {
  const r = formatEdit({ format: "hevc", rateControl: "vbr", maxBitrate: "high" }, "rateControl", "level");
  assert.ok(!("maxBitrate" in r.x));
});

test("a non-format edit leaves the status line alone", () => {
  const r = formatEdit({ format: "hevc", bitDepth: "10" }, "bitDepth", "8");
  assert.equal(r.said, undefined);
});

test("the slider on an old flow with a cap clears the cap, so Publish isn't refused", () => {
  const r = formatEdit({ format: "hevc", rateControl: "level_legacy", maxBitrate: "high", quality: 3 }, "quality", 4);
  assert.equal(r.x.rateControl, "quality");
  assert.equal(r.x.maxBitrate, "none");
});

test("More options on an AV1 step with a cap clears the cap (custom path)", () => {
  assert.deepEqual(qualityCap({ format: "av1", maxBitrate: "medium" }, "quality"), { maxBitrate: "none" });
});

test("qualityCap writes nothing when there is no cap or the rate control is not Quality", () => {
  assert.deepEqual(qualityCap({ maxBitrate: "none" }, "quality"), {});
  assert.deepEqual(qualityCap({ maxBitrate: "high" }, "level"), {});
});

test("More options on a capped step writes Quality only and clears the cap", () => {
  assert.deepEqual(customEdit({ format: "hevc", rateControl: "level", maxBitrate: "high" }), { rateControl: "quality", maxBitrate: "none" });
});

test("More options on a step with no cap writes Quality only and no maxBitrate", () => {
  const x = customEdit({ format: "hevc", rateControl: "level", maxBitrate: "none" });
  assert.deepEqual(x, { rateControl: "quality" });
  assert.ok(!("maxBitrate" in x));
});
