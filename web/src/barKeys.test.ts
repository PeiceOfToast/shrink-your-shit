import { test } from "node:test";
import assert from "node:assert/strict";
import { dayLine, dayRows, keyTarget } from "./barKeys.ts";

test("arrows move one day and stop at the ends", () => {
  assert.equal(keyTarget("ArrowLeft", 5, 30), 4);
  assert.equal(keyTarget("ArrowRight", 5, 30), 6);
  assert.equal(keyTarget("ArrowLeft", 0, 30), 0);
  assert.equal(keyTarget("ArrowRight", 29, 30), 29);
});

test("Home and End jump to the first and last day", () => {
  assert.equal(keyTarget("Home", 12, 30), 0);
  assert.equal(keyTarget("End", 12, 30), 29);
});

test("Escape hides the tip; other keys are not the chart's", () => {
  assert.equal(keyTarget("Escape", 3, 30), "hide");
  assert.equal(keyTarget("Enter", 3, 30), null);
  assert.equal(keyTarget("a", 3, 30), null);
});

test("the last day is the focus default, so the first arrow from rest lands on it", () => {
  // the component starts from n - 1 when nothing is open; ArrowLeft then goes to the day before it
  assert.equal(keyTarget("ArrowLeft", 29, 30), 28);
});

test("a recorded day lists every part, a zero says none", () => {
  const rows = dayRows(true, [{ label: "Freed", value: 1.9e9 }, { label: "Waiting in the trash", value: 0 }], (n) => (n / 1e9).toFixed(1) + " GB");
  assert.deepEqual(rows, [{ label: "Freed", text: "1.9 GB", color: undefined }, { label: "Waiting in the trash", text: "none", color: undefined }]);
});

test("a day not recorded has one row, not a zero", () => {
  assert.deepEqual(dayRows(false, [{ label: "Done", value: 0 }], String), [{ label: "", text: "not recorded" }]);
});

test("the live line names the day and every part, with the extra last", () => {
  const rows = dayRows(true, [{ label: "Freed", value: 1.9e9 }, { label: "Waiting in the trash", value: 0.6e9 }], (n) => (n / 1e9).toFixed(1) + " GB");
  assert.equal(dayLine("Nov 21", rows, "Frees from Nov 28"), "Nov 21, Freed 1.9 GB, Waiting in the trash 0.6 GB, Frees from Nov 28");
  assert.equal(dayLine("Nov 21", [{ label: "", text: "not recorded" }]), "Nov 21, not recorded");
});
