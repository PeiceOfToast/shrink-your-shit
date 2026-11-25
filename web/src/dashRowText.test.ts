import { test } from "node:test";
import assert from "node:assert/strict";
import { convertingEmpty, libraryRight } from "./dashRowText.ts";

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

test("library right side: saved shows the saved bytes and the share of the total", () => {
  // saved 25, size now 75: total 100, 25% of 100
  assert.equal(libraryRight({ saved: 25, waitingToFree: 40, bytesNow: 75 }, "Nov 28", bytes), "25 B saved · 25% of 100 B");
});

test("library right side: nothing saved, waiting to free shows the smaller bytes and the day", () => {
  assert.equal(libraryRight({ saved: 0, waitingToFree: 410, bytesNow: 900 }, "Nov 28", bytes), "410 B smaller · frees on Nov 28");
  assert.equal(libraryRight({ saved: 0, waitingToFree: 410, bytesNow: 900 }, null, bytes), "410 B smaller");
});

test("library right side: nothing saved and nothing waiting says so", () => {
  assert.equal(libraryRight({ saved: 0, waitingToFree: 0, bytesNow: 900 }, "Nov 28", bytes), "Nothing saved yet");
});
