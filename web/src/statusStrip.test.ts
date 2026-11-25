import { test } from "node:test";
import assert from "node:assert/strict";
import { filesFact, lastDoneFact, nextScanFact, nothingFact, stateWord, workersFact } from "./statusStrip.ts";

const NOW = new Date("2026-11-25T12:00:00Z");
const dayStub = (t: string) => "TIME:" + t;
const atStub = (t: string) => "AT:" + t;

test("the state word: not responding beats offline, busy is converting, nothing on is off", () => {
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }, { name: "B", status: "stalled" }]), { word: "Not responding", tone: "bad" });
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }, { name: "B", status: "offline" }]), { word: "Some workers offline", tone: "warn" });
  assert.deepEqual(stateWord([{ name: "A", status: "busy" }]), { word: "Converting", tone: "ok" });
  assert.deepEqual(stateWord([{ name: "A", status: "online" }]), { word: "Idle", tone: "muted" });
  assert.deepEqual(stateWord([{ name: "A", status: "paused" }]), { word: "Off", tone: "muted" });
  assert.deepEqual(stateWord([]), { word: "Off", tone: "muted" });
});

test("the workers fact counts the busy ones and names the first worker that is not ready", () => {
  const w = [{ name: "Desk", status: "busy" }, { name: "Laptop", status: "offline" }, { name: "Box", status: "busy" }];
  assert.deepEqual(workersFact(w), { text: "2 of 3 workers busy", notReady: "Laptop is offline" });
  assert.deepEqual(workersFact([{ name: "Box", status: "stalled" }, { name: "Laptop", status: "offline" }]), { text: "0 of 2 workers busy", notReady: "Box is not responding and 1 more" });
  assert.deepEqual(workersFact([{ name: "Desk", status: "busy" }]), { text: "1 of 1 worker busy", notReady: null });
  assert.equal(workersFact([]), null);
});

test("the last-done fact: absent leaves it out, null says no file done yet, a time is ago", () => {
  assert.equal(lastDoneFact(undefined, NOW, dayStub), undefined);
  assert.equal(lastDoneFact(null, NOW, dayStub), "no file done yet");
  assert.equal(lastDoneFact(new Date(NOW.getTime() - 4 * 6e4).toISOString(), NOW, dayStub), "last file done 4 min ago");
  assert.equal(lastDoneFact(new Date(NOW.getTime() - 10e3).toISOString(), NOW, dayStub), "last file done just now");
  assert.match(lastDoneFact(new Date(NOW.getTime() - 3 * 36e5).toISOString(), NOW, dayStub)!, /^last file done TIME:/);
});

test("the next-scan fact: absent or null leaves it out; a time is shown by the clock", () => {
  assert.equal(nextScanFact(undefined, atStub), undefined);
  assert.equal(nextScanFact(null, atStub), undefined);
  assert.equal(nextScanFact("2026-11-26T02:00:00Z", atStub), "next scan AT:2026-11-26T02:00:00Z");
});

test("Nothing needs you shows only when the Needs attention card has nothing", () => {
  assert.equal(nothingFact(true), "Nothing needs you");
  assert.equal(nothingFact(false), undefined);
});

test("the files fact is plural and counted", () => {
  assert.equal(filesFact(1284), "1,284 files waiting");
  assert.equal(filesFact(1), "1 file waiting");
});
