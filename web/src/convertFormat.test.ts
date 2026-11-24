// #697: the format edit on a Convert step: the 10-bit reset and the AV1 rate rule.
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatEdit } from "./convertFormat.ts";

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
  assert.equal(r.said, "Switched to Quality only: AV1 can't use a bitrate. Your bitrate settings are kept if you switch back.");
});

test("a non-format edit leaves the status line alone", () => {
  const r = formatEdit({ format: "hevc", bitDepth: "10" }, "bitDepth", "8");
  assert.equal(r.said, undefined);
});
