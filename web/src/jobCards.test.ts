import { test } from "node:test";
import assert from "node:assert/strict";
import { etaWords, runFields, runningFor, startedAt } from "./jobCards.ts";
import type { Progress } from "./types/jobs.ts";

const base: Progress = { stage: "Converting video", percent: 48, workflowPercent: 40, elapsedS: 120, speed: null, etaS: null, fps: null, bitrateKbps: null };

test("etaWords reads seconds as words", () => {
  assert.equal(etaWords(30), "under a minute");
  assert.equal(etaWords(60), "about 1 min");
  assert.equal(etaWords(725), "about 12 min");
});

test("etaWords: from 90 min the time reads in hours, to the nearest half hour under 10 h", () => {
  assert.equal(etaWords(5340), "about 89 min"); // 89 min: still minutes
  assert.equal(etaWords(5400), "about 1.5 h"); // 90 min
  assert.equal(etaWords(26400), "about 7.5 h"); // 7 h 20 min rounds to 7.5
  assert.equal(etaWords(35400), "about 10 h"); // 9 h 50 min is nearest 10 h
});

test("etaWords: from 10 h the time reads in whole hours", () => {
  assert.equal(etaWords(36000), "about 10 h");
  assert.equal(etaWords(50400), "about 14 h");
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

test("startedAt: now minus the elapsed seconds, and nothing without elapsed time", () => {
  const now = new Date("2026-11-23T11:30:00Z");
  assert.equal(startedAt(1080, now)?.toISOString(), "2026-11-23T11:12:00.000Z");
  assert.equal(startedAt(0, now)?.getTime(), now.getTime());
  assert.equal(startedAt(null, now), null);
  assert.equal(startedAt(undefined, now), null);
});

test("runFields: a speed under 0.05 is shown as <0.1×, not 0.0×", () => {
  assert.equal(runFields({ ...base, speed: 0.02 }).speed, "<0.1×");
  assert.equal(runFields({ ...base, speed: 0.06 }).speed, "0.1×");
});

test("runFields: a zero speed is not shown as one", () => {
  assert.equal(runFields({ ...base, speed: 0 }).speed, "—");
});
