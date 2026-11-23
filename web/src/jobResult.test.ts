// The drawer's wording (dev: `npm test`, Node 22.6+ with --experimental-strip-types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { afterHeader, codecName, fileFacts, newFileVideo, queuedLine, ruleName, savedPercent, stoppedTitle, subtitleCount, videoDetail, whyHeading, workerLabel } from "./jobResult.ts";

test("a kept job's Result column says the new file was not kept", () => {
  assert.equal(afterHeader(true), "New file (not kept)");
});

test("a done job's Result column stays After", () => {
  assert.equal(afterHeader(false), "After");
});

test("a workflow changed after queueing shows the version line", () => {
  assert.equal(queuedLine({ version: 4, queuedVersion: 3 }), "The workflow changed after this job was queued (v3 → v4).");
});

test("no queuedVersion, no line", () => {
  assert.equal(queuedLine({ version: 4 }), null);
  assert.equal(queuedLine(undefined), null);
});

test("a workflow's own stop is headed 'Stopped by its workflow'", () => {
  assert.equal(whyHeading("workflow_stopped", true), "Stopped by its workflow");
  assert.equal(whyHeading("workflow_stopped", false), "Stopped by its workflow");
});

test("a stop's hover is not given the prefix twice", () => {
  assert.equal(stoppedTitle("Stopped by this workflow: x"), "Stopped by this workflow: x");
  assert.equal(stoppedTitle("stopped by this workflow: x"), "stopped by this workflow: x");
  assert.equal(stoppedTitle("Converted 3 times, still too big"), "Stopped by its workflow: Converted 3 times, still too big");
});

test("any other failure stays 'Why it failed' (or 'Why' without a failure block)", () => {
  assert.equal(whyHeading("ffmpeg_error", true), "Why it failed");
  assert.equal(whyHeading("", true), "Why it failed");
  assert.equal(whyHeading("ffmpeg_error", false), "Why");
});

test("newFileVideo: the codec in capitals, then the size, fps and bit depth, without the audio and length", () => {
  assert.equal(newFileVideo("hevc 1280x720 25.000 fps 8-bit, 0 audio, 0 subtitles, 0:00:20"), "HEVC 1280x720 25.000 fps 8-bit");
});

test("the codec reads the same on both sides: H.264, not H264", () => {
  assert.equal(newFileVideo("h264 1280x720 25.000 fps 8-bit, 0 audio, 0 subtitles, 0:00:20"), "H.264 1280x720 25.000 fps 8-bit");
  assert.equal(videoDetail("h264", { Width: 1280, Height: 720, frameRate: 25, bitDepth: 8 }), newFileVideo("h264 1280x720 25.000 fps 8-bit, 0 audio"));
});

test("workerLabel: the built-in worker is This computer, another worker keeps its name, none is a dash", () => {
  assert.equal(workerLabel("builtin"), "This computer");
  assert.equal(workerLabel("builtin-2"), "This computer");
  assert.equal(workerLabel("nas-1"), "nas-1");
  assert.equal(workerLabel(null), "—");
  assert.equal(workerLabel(undefined), "—");
});

test("ruleName: named only when the job's own workflow decided it", () => {
  assert.equal(ruleName({ id: "w1" }, "Movies to HEVC"), "Movies to HEVC");
  assert.equal(ruleName(undefined, "Movies to HEVC"), null);
  assert.equal(ruleName({ id: "w1" }, ""), null);
});

test("videoDetail: codec, size, fps and bit depth, the same shape on both sides", () => {
  assert.equal(videoDetail("h264", { Width: 1280, Height: 720, frameRate: 25, bitDepth: 8 }), "H.264 1280x720 25.000 fps 8-bit");
  assert.equal(videoDetail("hevc", { Width: 1920, Height: 1080, frameRate: 23.976, bitDepth: 10 }), "HEVC 1920x1080 23.976 fps 10-bit");
  assert.equal(videoDetail("h264", undefined), "H.264");
  assert.equal(videoDetail(undefined, undefined), null);
});

test("savedPercent: a whole percentage, rounded", () => {
  assert.equal(savedPercent(1000, 500), 50);
  assert.equal(savedPercent(18.9e6, 8.5e6), 55); // 55.02 → 55
  assert.equal(savedPercent(1000, 1100), -10); // a bigger file: negative
});

test("fileFacts: the subtitle count and the length from a check line; a part it lacks is left out", () => {
  assert.deepEqual(fileFacts("hevc 1280x720 25.000 fps 8-bit, 0 audio, 2 subtitles, 0:00:20"), { subtitles: 2, length: "0:00:20" });
  assert.deepEqual(fileFacts("h264 640x360 24 fps 8-bit, 1 audio, 0 subtitles, 1:02:03.5"), { subtitles: 0, length: "1:02:03.5" });
  assert.deepEqual(fileFacts("hevc 1280x720, 0 audio"), null);
  assert.equal(fileFacts(undefined), null);
});

test("codecName: the server's words for mpeg4, mpeg2video and prores; an unknown codec in capitals", () => {
  assert.equal(codecName("mpeg4"), "MPEG-4");
  assert.equal(codecName("mpeg2video"), "MPEG-2");
  assert.equal(codecName("prores"), "ProRes");
  assert.equal(codecName("h264"), "H.264");
  assert.equal(codecName("H264"), "H.264");
  assert.equal(codecName("theora"), "THEORA");
  assert.equal(newFileVideo("mpeg4 720x576 25.000 fps 8-bit, 1 audio, 0 subtitles, 0:00:20"), "MPEG-4 720x576 25.000 fps 8-bit");
  assert.equal(newFileVideo("prores 1920x1080 23.976 fps 10-bit, 2 audio, 0 subtitles, 0:01:00"), "ProRes 1920x1080 23.976 fps 10-bit");
});

test("subtitleCount: the subtitle streams of a probe; nothing when the server sent no list", () => {
  assert.equal(subtitleCount([{ type: "video" }, { type: "audio" }, { type: "subtitle" }, { type: "subtitle" }]), 2);
  assert.equal(subtitleCount([{ type: "video" }, { type: "audio" }]), 0);
  assert.equal(subtitleCount(undefined), undefined);
});
