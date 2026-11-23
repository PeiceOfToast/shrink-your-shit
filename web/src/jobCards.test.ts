import { test } from "node:test";
import assert from "node:assert/strict";
import { etaWords, runFields, runningFor } from "./jobCards.ts";
import type { Progress } from "./types/jobs.ts";

const base: Progress = { stage: "Converting video", percent: 48, workflowPercent: 40, elapsedS: 120, speed: null, etaS: null, fps: null, bitrateKbps: null };

test("etaWords reads seconds as words", () => {
  assert.equal(etaWords(30), "under a minute");
  assert.equal(etaWords(60), "about 1 min");
  assert.equal(etaWords(725), "about 12 min");
});

test("runFields: a value not sent reads as a dash", () => {
  assert.deepEqual(runFields(base), { step: "Converting video", fps: "—", speed: "—", timeLeft: "—", bitrate: "—" });
});

test("runFields: sent values are formatted", () => {
  const f = runFields({ ...base, speed: 2.66, etaS: 725, bitrateKbps: 5400.4 });
  assert.deepEqual(f, { step: "Converting video", fps: "—", speed: "2.7×", timeLeft: "about 12 min", bitrate: "5,400 kbps" });
});

test("runFields: frames per second is one decimal when sent, and a dash when not", () => {
  assert.equal(runFields({ ...base, fps: 23.976 }).fps, "24.0");
  assert.equal(runFields({ ...base, fps: 0 }).fps, "—");
});

test("runningFor: elapsed time as the side note", () => {
  assert.equal(runningFor(45), "running under a minute");
  assert.equal(runningFor(738), "running 12 min");
});

test("runFields: a zero speed is not shown as one", () => {
  assert.equal(runFields({ ...base, speed: 0 }).speed, "—");
});
