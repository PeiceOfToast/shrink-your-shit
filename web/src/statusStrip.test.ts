import { test } from "node:test";
import assert from "node:assert/strict";
import { allLibrariesRunning, filesFact, freshFact, lastDoneFact, nextScanFact, nothingFact, stateWord, workersFact, type Fact } from "./statusStrip.ts";

const NOW = new Date("2026-11-25T12:00:00Z");
const dayStub = (t: string) => "TIME:" + t;
const atStub = (t: string) => "AT:" + t;
// the words of a fact, as the page reads them (the value is the bold part)
const flat = (f: Fact | undefined) => (f === undefined ? undefined : typeof f === "string" ? f : f.pre + f.value + f.post);

test("the state word: not responding beats offline, busy is converting, nothing on is off", () => {
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }, { name: "B", status: "stalled" }], 0), { word: "Not responding", tone: "bad" });
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }, { name: "B", status: "offline" }], 0), { word: "Some workers offline", tone: "warn" });
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }], 900), { word: "Converting", tone: "ok" });
  assert.deepEqual(stateWord([{ name: "A", status: "paused" }], 900), { word: "Off", tone: "muted" });
  assert.deepEqual(stateWord([], 900), { word: "Off", tone: "muted" });
});

test("workers on, none busy: Waiting when files are waiting, Idle when nothing is", () => {
  assert.deepEqual(stateWord([{ name: "A", status: "online" }], 1120), { word: "Waiting", tone: "muted" });
  assert.deepEqual(stateWord([{ name: "A", status: "online" }], 0), { word: "Idle", tone: "muted" });
  // a worker that is busy still reads Converting, whatever is waiting
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }, { name: "B", status: "online" }], 1120), { word: "Converting", tone: "ok" });
});

test("a draining worker is still writing its file: Converting, and it counts as busy", () => {
  assert.deepEqual(stateWord([{ name: "A", status: "draining" }], 1), { word: "Converting", tone: "ok" });
  assert.deepEqual(stateWord([{ name: "A", status: "draining" }, { name: "B", status: "online" }], 0), { word: "Converting", tone: "ok" });
  assert.deepEqual(workersFact([{ name: "A", status: "draining" }]), { text: { pre: "", value: "1 of 1", post: " worker busy" }, notReady: null });
});

test("a paused worker is named stopped, unless every worker is paused", () => {
  assert.deepEqual(workersFact([{ name: "Desk", status: "busy" }, { name: "NAS", status: "paused" }]), { text: { pre: "", value: "1 of 2", post: " workers busy" }, notReady: "NAS is stopped" });
  assert.deepEqual(workersFact([{ name: "NAS", status: "paused" }, { name: "Box", status: "offline" }]), { text: { pre: "", value: "0 of 2", post: " workers busy" }, notReady: "NAS is stopped and 1 more" });
  assert.deepEqual(workersFact([{ name: "NAS", status: "paused" }, { name: "Box", status: "paused" }]), { text: { pre: "", value: "0 of 2", post: " workers busy" }, notReady: null });
  assert.equal(stateWord([{ name: "NAS", status: "paused" }, { name: "Box", status: "paused" }], 0).word, "Off");
});

test("the workers fact counts the busy ones (bold value) and names the first worker that is not ready", () => {
  const w = [{ name: "Desk", status: "busy" }, { name: "Laptop", status: "offline" }, { name: "Box", status: "busy" }];
  assert.deepEqual(workersFact(w), { text: { pre: "", value: "2 of 3", post: " workers busy" }, notReady: "Laptop is offline" });
  assert.deepEqual(workersFact([{ name: "Box", status: "stalled" }, { name: "Laptop", status: "offline" }]), { text: { pre: "", value: "0 of 2", post: " workers busy" }, notReady: "Box is not responding and 1 more" });
  assert.deepEqual(workersFact([{ name: "Desk", status: "busy" }]), { text: { pre: "", value: "1 of 1", post: " worker busy" }, notReady: null });
  assert.equal(workersFact([]), null);
});

test("the last-done fact: absent leaves it out, null says no file done yet, a time is ago", () => {
  assert.equal(lastDoneFact(undefined, NOW, dayStub), undefined);
  assert.equal(lastDoneFact(null, NOW, dayStub), "no file done yet");
  assert.deepEqual(lastDoneFact(new Date(NOW.getTime() - 4 * 6e4).toISOString(), NOW, dayStub), { pre: "last file done ", value: "4 min ago", post: "" });
  assert.equal(lastDoneFact(new Date(NOW.getTime() - 10e3).toISOString(), NOW, dayStub), "last file done just now");
  assert.match(flat(lastDoneFact(new Date(NOW.getTime() - 3 * 36e5).toISOString(), NOW, dayStub))!, /^last file done TIME:/);
});

test("the next-scan fact: absent or null leaves it out; a time is shown by the clock", () => {
  assert.equal(nextScanFact(undefined, atStub), undefined);
  assert.equal(nextScanFact(null, atStub), undefined);
  assert.deepEqual(nextScanFact("2026-11-26T02:00:00Z", atStub), { pre: "next scan ", value: "AT:2026-11-26T02:00:00Z", post: "" });
});

test("Nothing needs you shows only when the Needs attention card has nothing", () => {
  assert.equal(nothingFact(true), "Nothing needs you");
  assert.equal(nothingFact(false), undefined);
});

test("the files fact is plural, counted, and the number is the bold value", () => {
  assert.deepEqual(filesFact(1284), { pre: "", value: "1,284", post: " files waiting" });
  assert.equal(flat(filesFact(1)), "1 file waiting");
});

test("a library that can't run means no Nothing needs you", () => {
  assert.equal(allLibrariesRunning(["active", "active"]), true);
  assert.equal(allLibrariesRunning(["active", "paused"]), false);
  assert.equal(allLibrariesRunning(["no_workflow"]), false);
  assert.equal(allLibrariesRunning(["paused_share"]), false);
  assert.equal(allLibrariesRunning([]), true);
});

test("the fresh-install sentence names the missing step; no step (card not shown) gives no sentence", () => {
  assert.equal(freshFact(0), "Add a library to start");
  assert.equal(freshFact(1), "Pick a workflow for your library to start");
  assert.equal(freshFact(2), "Turn on the workers to start");
  assert.equal(freshFact(null), null);
});
// The page passes nextStep only while showGetStarted is true, so an established install with paused workers
// (converted > 0, Get started card hidden) gets no sentence; getStarted.test.ts covers showGetStarted.
