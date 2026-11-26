import { test } from "node:test";
import assert from "node:assert/strict";
import { convertingEmpty, headlineMore, libraryRight, shareText } from "./dashRowText.ts";

const bytes = (n: number) => n + " B";
const at = (t: string) => "at " + t;

test("empty Converting now: workers off says how to start", () => {
  assert.equal(convertingEmpty(false, 0, "2026-11-25T14:00:00Z", at), "Nothing is converting. Turn workers on to start.");
  assert.equal(convertingEmpty(false, 5, null, at), "Nothing is converting. Turn workers on to start.");
});

test("empty Converting now: workers on, nothing waiting, a next scan known names the time", () => {
  assert.equal(convertingEmpty(true, 0, "2026-11-25T14:00:00Z", at), "Nothing is converting. Nothing is waiting; next scan at 2026-11-25T14:00:00Z");
});

test("empty Converting now: otherwise the plain line", () => {
  assert.equal(convertingEmpty(true, 3, "2026-11-25T14:00:00Z", at), "Nothing is converting.");
  assert.equal(convertingEmpty(true, 0, null, at), "Nothing is converting.");
  assert.equal(convertingEmpty(true, 0, undefined, at), "Nothing is converting.");
});

test("share text: whole percent from 10, one decimal from 0.1, under 0.1 below that", () => {
  assert.equal(shareText(25), "25%");
  assert.equal(shareText(0.4), "0.4%");
  assert.equal(shareText(0.04), "under 0.1%");
  assert.equal(shareText(9.95), "9.9%");
  assert.equal(shareText(9.96), "9.9%");
  assert.equal(shareText(0.368), "0.4%");
  assert.equal(shareText(9.5), "9.5%");
  assert.equal(shareText(10), "10%");
});

test("library right side: saved gives the green figure and the share of the total", () => {
  // saved 25, size now 75: total 100, 25% of 100
  assert.deepEqual(libraryRight({ saved: 25, waitingToFree: 40, bytesNow: 75 }, "Nov 28", bytes), { gain: "25 B", rest: " saved · 25% of 100 B" });
  // saved 4, size now 996: total 1000, 0.4% of 1000
  assert.deepEqual(libraryRight({ saved: 4, waitingToFree: 0, bytesNow: 996 }, null, bytes), { gain: "4 B", rest: " saved · 0.4% of 1000 B" });
});

test("library right side: nothing saved, waiting to free shows the smaller bytes and the day", () => {
  assert.deepEqual(libraryRight({ saved: 0, waitingToFree: 410, bytesNow: 900 }, "Nov 28", bytes), { gain: "", rest: "410 B smaller · frees from Nov 28" });
  assert.deepEqual(libraryRight({ saved: 0, waitingToFree: 410, bytesNow: 900 }, null, bytes), { gain: "", rest: "410 B smaller" });
});

test("headline first item: freed already with a day says more, frees from the day", () => {
  assert.equal(headlineMore("410 B", 5, "Nov 28"), "410 B more, frees from Nov 28");
});

test("headline first item: nothing freed yet with a day says frees from the day", () => {
  assert.equal(headlineMore("410 B", 0, "Nov 28"), "410 B frees from Nov 28");
});

test("headline first item: no day keeps the bare more / frees", () => {
  assert.equal(headlineMore("410 B", 5, null), "410 B more");
  assert.equal(headlineMore("410 B", 0, null), "410 B frees");
});

test("library right side: nothing saved and nothing waiting says so", () => {
  assert.deepEqual(libraryRight({ saved: 0, waitingToFree: 0, bytesNow: 900 }, "Nov 28", bytes), { gain: "", rest: "Nothing saved yet" });
});
