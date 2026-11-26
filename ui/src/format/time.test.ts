import { test } from "node:test";
import assert from "node:assert/strict";
import { ago, clock, clockPrecise, dayClock, duration, jobWhen, timeLeft } from "./time.ts";

// Local-time dates, so the tests hold in any zone.
const local = (y: number, mo: number, d: number, h: number, mi: number, s = 0, ms = 0) =>
  new Date(y, mo, d, h, mi, s, ms).toISOString();
const NOW = Date.parse(local(2026, 10, 26, 14, 20));

test("clock is local HH:MM", () => {
  assert.equal(clock(local(2026, 10, 26, 14, 2)), "14:02");
});

test("clockPrecise keeps milliseconds", () => {
  assert.equal(clockPrecise(local(2026, 10, 26, 14, 2, 10, 204)), "14:02:10.204");
});

test("dayClock says today, yesterday, tomorrow, or the date", () => {
  assert.equal(dayClock(local(2026, 10, 26, 13, 10), NOW), "today 13:10");
  assert.equal(dayClock(local(2026, 10, 25, 22, 10), NOW), "yesterday 22:10");
  assert.equal(dayClock(local(2026, 10, 27, 2, 0), NOW), "tomorrow 02:00");
  assert.equal(dayClock(local(2026, 10, 20, 9, 5), NOW), "20 Nov 09:05");
});

test("ago reads just now, minutes, then the day", () => {
  assert.equal(ago(new Date(NOW - 10_000).toISOString(), NOW), "just now");
  assert.equal(ago(new Date(NOW - 4 * 60_000).toISOString(), NOW), "4 min ago");
  assert.equal(ago(local(2026, 10, 25, 22, 10), NOW), "yesterday 22:10");
});

test("duration is m:ss, or h:mm:ss from an hour", () => {
  assert.equal(duration(42_000), "0:42");
  assert.equal(duration(708_000), "11:48");
  assert.equal(duration(7_092_000), "1:58:12");
});

test("timeLeft rounds up to minutes", () => {
  assert.equal(timeLeft(180_000), "3 min left");
  assert.equal(timeLeft(170_000), "3 min left");
  assert.equal(timeLeft(20_000), "under a minute left");
  assert.equal(timeLeft(null), null);
});

test("jobWhen follows the job state", () => {
  const base = { id: "x", file: "f", library: "L", workflow: "w", worker: "", step: null, progress: null, result: { sizeBefore: 1, sizeAfter: null, outcome: null, failedStep: null, reason: null, warnings: 0 } };
  assert.equal(jobWhen({ ...base, state: "running", startedAt: local(2026, 10, 26, 14, 2), queuedAt: null, finishedAt: null, tookMs: null }), "from 14:02");
  assert.equal(jobWhen({ ...base, state: "waiting", startedAt: null, queuedAt: local(2026, 10, 26, 13, 20), finishedAt: null, tookMs: null }), "queued 13:20");
  assert.equal(jobWhen({ ...base, state: "done", startedAt: null, queuedAt: null, finishedAt: null, tookMs: 380_000 }), "took 6:20");
});
