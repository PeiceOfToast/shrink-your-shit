// F7: the encoder speed lines. Node runs these (npm test); the React block is a plain list over these two functions.
import { test } from "node:test";
import assert from "node:assert/strict";
import { speedLine, speedsOf } from "./encoderSpeeds.ts";

const all = [
  { worker: "nas", encoder: "hevc_qsv", speed: 1.4, samples: 7 },
  { worker: "this", encoder: "libx265", speed: 0.61, samples: 1 },
  { worker: "this", encoder: "hevc_qsv", speed: 3.25, samples: 42, fps: 118.4 },
];

test("a worker's entries come fastest first, and only its own", () => {
  assert.deepEqual(speedsOf(all, "this").map((s) => s.encoder), ["hevc_qsv", "libx265"]);
  assert.deepEqual(speedsOf(all, "nas").map((s) => s.encoder), ["hevc_qsv"]);
});

test("a worker with no entries gets an empty list, so the block is hidden", () => {
  assert.deepEqual(speedsOf(all, "laptop"), []);
});

test("the line shows encoder, speed, fps and runs", () => {
  assert.equal(speedLine(all[2]), "hevc_qsv · 3.25x · 118 fps · 42 runs");
});

test("no fps text when the server has none", () => {
  assert.equal(speedLine(all[1]), "libx265 · 0.61x · 1 run");
  assert.ok(!speedLine(all[0]).includes("fps"));
});

test("a zero fps is never shown", () => {
  assert.ok(!speedLine({ ...all[2], fps: 0 }).includes("fps"));
});

test("a fractional fps under 10 keeps its decimal, so it is not shown as 0", () => {
  assert.equal(speedLine({ ...all[2], fps: 0.3 }), "hevc_qsv · 3.25x · 0.3 fps · 42 runs");
  assert.equal(speedLine({ ...all[2], fps: 7.5 }), "hevc_qsv · 3.25x · 7.5 fps · 42 runs");
});

test("fps 10 and up is a whole number", () => {
  assert.equal(speedLine(all[2]), "hevc_qsv · 3.25x · 118 fps · 42 runs");
  assert.equal(speedLine({ ...all[2], fps: 9.96 }), "hevc_qsv · 3.25x · 10 fps · 42 runs");
});

test("no line ever contains ' 0 fps'", () => {
  for (const fps of [0, 0.04, 0.1, 0.3, 1, 9.94, 9.96, 118.4]) {
    assert.ok(!speedLine({ ...all[2], fps }).includes(" 0 fps"), String(fps));
  }
});

test("one run is singular, more is plural", () => {
  assert.ok(speedLine(all[1]).endsWith("1 run"));
  assert.ok(speedLine(all[0]).endsWith("7 runs"));
});
