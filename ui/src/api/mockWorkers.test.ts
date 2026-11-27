import { test } from "node:test";
import assert from "node:assert/strict";
import { drainAllWorkers, FINISH_MS, getWorkers, getWorker, pauseWorker, startAllWorkers, workersSwitch } from "./mockWorkers.ts";
import { getSummary } from "./mock.ts";
import { SUMMARY } from "./fixtures/index.ts";

test("draining turns the switch off and counts the files still finishing", async () => {
  await drainAllWorkers();
  assert.deepEqual(workersSwitch(), { paused: true, finishing: SUMMARY.converting.running });
  const list = await getWorkers();
  assert.ok(list.length > 0);
  assert.ok(list.every((w) => w.pausedGlobally === true));
  const s = await getSummary();
  assert.equal(s.workers.paused, true);
  assert.equal(s.workers.finishing, SUMMARY.converting.running);
  assert.equal(s.converting.running, SUMMARY.converting.running);
  await startAllWorkers();
});

test("starting the switch again clears the finishing count and resumes every worker", async () => {
  await drainAllWorkers();
  await startAllWorkers();
  assert.deepEqual(workersSwitch(), { paused: false, finishing: 0 });
  const list = await getWorkers();
  assert.ok(list.every((w) => w.pausedGlobally === false));
  const s = await getSummary();
  assert.equal(s.workers.paused, false);
  assert.equal(s.workers.finishing, 0);
});

test("when the finishing files are done, no worker still shows a file in Now", async () => {
  assert.ok((await getWorkers()).some((w) => w.now), "the mock has a worker on a file");
  await drainAllWorkers();
  assert.ok((await getWorkers()).some((w) => w.now), "still finishing: the files show");
  await new Promise((resolve) => setTimeout(resolve, FINISH_MS + 200));
  assert.equal((await getWorkers()).filter((w) => w.now).length, 0);
  await startAllWorkers();
});

test("a worker paused by hand stays paused through a global drain and start", async () => {
  const target = (await getWorkers()).find((w) => w.state !== "offline");
  assert.ok(target, "the mock has an online worker");
  await pauseWorker(target.id, false);
  await drainAllWorkers();
  await startAllWorkers();
  const after = await getWorker(target.id);
  assert.equal(after.state, "paused");
  assert.equal(after.pausedGlobally, false);
});
