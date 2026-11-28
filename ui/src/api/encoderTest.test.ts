// Test the encoders: the run id, the finished check the page polls with, and the worker's test state on the mock.
import { test } from "node:test";
import assert from "node:assert/strict";
import { testEncoders, getWorker, getWorkers, runAction } from "./mock.ts";
import { ApiError } from "./errors.ts";
import { disablesEncoderTest, testFinished, waitUntil } from "../pages/encoderTest.ts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

test("testing the encoders answers a run id, not a line", async () => {
  const [w] = await getWorkers();
  const r = await testEncoders(w.id);
  assert.equal(typeof r.runId, "string");
  assert.ok(r.runId.length > 0);
  assert.equal("line" in r, false);
});

test("a worker that does not exist refuses the test with worker_not_found", async () => {
  await assert.rejects(testEncoders("no-such-worker"), (e: unknown) => e instanceof ApiError && e.code === "worker_not_found");
});

test("the worker carries the run until it finishes; the page's check is done only for that run", async () => {
  const [w] = await getWorkers();
  const { runId } = await testEncoders(w.id);
  const finished = await waitUntil(async () => testFinished(await getWorker(w.id), runId), { tries: 40, delayMs: 100, sleep });
  assert.equal(finished, true);
  const t = (await getWorker(w.id) as unknown as { test: { runId: string; running: boolean } }).test;
  assert.equal(t.runId, runId);
  assert.equal(t.running, false);
  assert.equal(testFinished(await getWorker(w.id), "some-other-run"), false);
});

test("a test still running is not finished; a worker that never ran (null) is not finished", () => {
  assert.equal(testFinished({ test: { runId: "r1", running: true, finishedAt: null } }, "r1"), false);
  assert.equal(testFinished({ test: { runId: "r1", running: false, finishedAt: "2026-11-28T07:00:00Z" } }, "r1"), true);
  assert.equal(testFinished({ test: null }, "r1"), false);
});

test("waitUntil stops as soon as the check is done, and gives up after its tries", async () => {
  let n = 0;
  assert.equal(await waitUntil(async () => ++n === 2, { tries: 5, delayMs: 0, sleep: async () => {} }), true);
  assert.equal(n, 2);
  assert.equal(await waitUntil(async () => false, { tries: 3, delayMs: 0, sleep: async () => {} }), false);
});

test("the Dashboard's test row shows Started, not a line, when the test starts", async () => {
  const [w] = await getWorkers();
  assert.deepEqual(await runAction("POST", `/api/v2/workers/${w.id}/capabilities/test`), {});
});

test("a machine with no encoder detector turns the test off; a busy worker leaves it on", () => {
  assert.equal(disablesEncoderTest("encoder_test_unavailable"), true);
  assert.equal(disablesEncoderTest("worker_busy"), false);
  assert.equal(disablesEncoderTest("worker_offline"), false);
  assert.equal(disablesEncoderTest(undefined), false);
});

test("the worker page turns the test off only through that decision, and disables the button with it", async () => {
  const { readFileSync } = await import("node:fs");
  const page = readFileSync(new URL("../pages/WorkerPage.tsx", import.meta.url), "utf8");
  assert.match(page, /if \(e instanceof ApiError && disablesEncoderTest\(e\.code\)\) setEncodersOff\(true\);/);
  assert.match(page, /disabled=\{busy \|\| encodersOff\}>Test the encoders/);
});
