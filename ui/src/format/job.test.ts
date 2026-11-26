import { test } from "node:test";
import assert from "node:assert/strict";
import { jobHint, jobResultText, jobResultTone, jobStepText, originalText, sizeText } from "./job.ts";
import { dayOf } from "./time.ts";
import type { Job, JobResult } from "../api/types.ts";

const GB = 1e9;

const result = (over: Partial<JobResult> = {}): JobResult => ({
  sizeBefore: 10 * GB,
  sizeAfter: null,
  outcome: null,
  failedStep: null,
  reason: null,
  warnings: 0,
  ...over,
});

const job = (over: Partial<Job> = {}): Job => ({
  id: "j1",
  state: "done",
  file: "Show S01E01.mkv",
  library: "TV Shows",
  workflow: "shrink-videos",
  worker: "worker-1",
  step: null,
  progress: null,
  queuedAt: null,
  startedAt: null,
  finishedAt: null,
  tookMs: null,
  result: result(),
  ...over,
});

const converting = { index: 3, count: 5, name: "Converting video" };

test("sizeText: one decimal under 10 GB, whole GB above", () => {
  assert.equal(sizeText(5.9 * GB), "5.9 GB");
  assert.equal(sizeText(0.5 * GB), "0.5 GB");
  assert.equal(sizeText(18.4 * GB), "18 GB");
});

test("jobStepText: the step, or the waiting and empty cases", () => {
  assert.equal(jobStepText(job({ state: "running", step: converting })), "Converting video · step 3 of 5");
  assert.equal(jobStepText(job({ state: "waiting" })), "Waiting for a worker");
  assert.equal(jobStepText(job({ state: "done" })), "—");
});

test("jobResultText: smaller, bigger, and warnings", () => {
  const smaller = job({ result: result({ sizeAfter: 5.9 * GB, outcome: "smaller" }) });
  assert.equal(jobResultText(smaller), "41% smaller · 4.1 GB freed");

  const bigger = job({ result: result({ sizeAfter: 10.8 * GB, outcome: "bigger_kept" }) });
  assert.equal(jobResultText(bigger), "8% bigger · original kept");

  const warned = job({ result: result({ sizeAfter: 5.9 * GB, outcome: "smaller", warnings: 1 }) });
  assert.equal(jobResultText(warned), "41% smaller · 4.1 GB freed · 1 warning");
  const warnedTwice = job({ result: result({ sizeAfter: 5.9 * GB, outcome: "smaller", warnings: 2 }) });
  assert.equal(jobResultText(warnedTwice), "41% smaller · 4.1 GB freed · 2 warnings");
});

test("jobResultText: failure shows its reason, or Failed without one", () => {
  assert.equal(jobResultText(job({ state: "failed", result: result({ outcome: "failed", reason: "Disk full" }) })), "Disk full");
  assert.equal(jobResultText(job({ state: "failed", result: result({ outcome: "failed" }) })), "Failed");
});

test("jobResultText: a running job estimates the saving, or shows a dash", () => {
  const estimating = job({ state: "running", result: result({ sizeBefore: 22 * GB, sizeAfter: 4.5 * GB }) });
  assert.equal(jobResultText(estimating), "about 18 GB saving");
  assert.equal(jobResultText(job({ state: "running", result: result() })), "—");
});

test("jobResultTone: ok when smaller, bad when failed, plain otherwise", () => {
  assert.equal(jobResultTone(job({ result: result({ outcome: "smaller" }) })), "ok");
  assert.equal(jobResultTone(job({ state: "failed", result: result({ outcome: "failed" }) })), "bad");
  assert.equal(jobResultTone(job({ result: result({ outcome: "bigger_kept" }) })), "");
});

test("jobHint: the step, then the estimate while running", () => {
  const running = job({ state: "running", step: converting, result: result({ sizeBefore: 22 * GB, sizeAfter: 4.5 * GB }) });
  assert.equal(jobHint(running), "Converting video · step 3 of 5 · about 18 GB saving");
  const done = job({ state: "done", step: converting, result: result({ sizeAfter: 5.9 * GB, outcome: "smaller" }) });
  assert.equal(jobHint(done), "Converting video · step 3 of 5");
});

test("originalText builds the Summary's Original line from action and until", () => {
  const until = new Date(2026, 11, 26, 14, 0).toISOString();
  assert.equal(originalText({ action: "trashed", until }), `In the trash until ${dayOf(until)}`);
  assert.equal(originalText({ action: "trashed", until: null }), "In the trash");
  assert.equal(originalText({ action: "kept", until: null }), "Kept as it was");
  assert.equal(originalText({ action: "not_yet", until: null }), "Not yet");
  assert.equal(originalText({ action: "not_touched", until: null }), "Not touched");
});
