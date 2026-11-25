// M3a: the Libraries list's filter rule and progress share. Node runs these (npm test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { allShownSelected, doneShare, filterCounts, inFilter, keepShown, libraryOptions, needsAttention, selectShown } from "./libraryList.ts";
import type { Library } from "./types/v2.ts";

const counts = { files: 0, waiting: 0, running: 0, completed: 0, skipped: 0, failed: 0 };
function lib(p: Partial<Library> & { counts?: Partial<Library["counts"]> } = {}): Library {
  const { counts: c, ...rest } = p;
  return {
    id: "x", name: "X", paths: ["/media/x"], fileType: "video", enabled: true, workflowId: "w", workflowName: "W", priority: 0,
    variables: [], scan: { state: "idle", progress: null, lastScanAt: "2026-11-22T01:30:00Z", nextScanAt: null, scheduleLabel: "" },
    counts: { ...counts, ...c }, bytes: { freed: 0, waitingToFree: 0 },
    output: { policy: "replace", folder: null, overrideFolder: null, problem: null },
    discovery: { subfolders: true, settleSeconds: 15, followLinks: false, include: [], exclude: [] },
    status: "ok", pausedBy: null, writable: true, unwritableMessage: "", ...rest,
  } as Library;
}

test("a healthy started library is Watching and in no other filter but All", () => {
  const l = lib();
  assert.equal(inFilter(l, "all"), true);
  assert.equal(inFilter(l, "watching"), true);
  assert.equal(inFilter(l, "attention"), false);
  assert.equal(inFilter(l, "paused"), false);
});

test("each problem status needs attention", () => {
  assert.equal(needsAttention(lib({ writable: false })), true);
  assert.equal(needsAttention(lib({ status: "no_workflow" })), true);
  assert.equal(needsAttention(lib({ status: "path_missing" })), true);
  assert.equal(needsAttention(lib({ status: "not_readable" })), true);
  assert.equal(needsAttention(lib({ counts: { failed: 2 } })), true);
  assert.equal(needsAttention(lib({ status: "paused_share" })), false); // waiting for its share is not a problem
});

test("a library with failed jobs is both Needs attention and Watching while started", () => {
  const l = lib({ counts: { failed: 1 } });
  assert.equal(inFilter(l, "attention"), true);
  assert.equal(inFilter(l, "watching"), true);
});

test("no workflow is never Watching or Paused", () => {
  const l = lib({ status: "no_workflow", enabled: false });
  assert.equal(inFilter(l, "watching"), false);
  assert.equal(inFilter(l, "paused"), false);
  assert.equal(inFilter(l, "attention"), true);
});

test("paused means not enabled and scanned before; never-scanned is only in All", () => {
  assert.equal(inFilter(lib({ enabled: false }), "paused"), true);
  const fresh = lib({ enabled: false, scan: { state: "idle", progress: null, lastScanAt: null, nextScanAt: null, scheduleLabel: "" } });
  assert.equal(inFilter(fresh, "paused"), false);
  assert.equal(inFilter(fresh, "watching"), false);
  assert.equal(inFilter(fresh, "all"), true);
});

test("filterCounts counts from the loaded list", () => {
  const ls = [lib(), lib({ writable: false }), lib({ enabled: false }), lib({ counts: { failed: 1 } })];
  assert.deepEqual(filterCounts(ls), { all: 4, watching: 2, attention: 2, paused: 1 });
  assert.deepEqual(filterCounts([]), { all: 0, watching: 0, attention: 0, paused: 0 });
});

test("the done share is (files - waiting - running - failed) / files", () => {
  assert.equal(doneShare({ ...counts, files: 1600, waiting: 640, running: 0 }), 0.6);
  assert.equal(doneShare({ ...counts, files: 10, waiting: 2, running: 3 }), 0.5);
  assert.equal(doneShare({ ...counts, files: 210, waiting: 0, running: 0 }), 1);
});

test("a failed file is not done: failed is subtracted, and an all-failed library is an empty bar", () => {
  assert.equal(doneShare({ ...counts, files: 10, waiting: 0, running: 0, failed: 4 }), 0.6);
  assert.equal(doneShare({ ...counts, files: 5, waiting: 0, running: 0, failed: 5 }), 0);
  assert.equal(doneShare({ ...counts, files: 5, waiting: 1, running: 1, failed: 1 }), 0.4);
});

test("no files means no share (null), and the share never leaves 0..1", () => {
  assert.equal(doneShare({ ...counts, files: 0 }), null);
  assert.equal(doneShare({ ...counts, files: 4, waiting: 0, running: 9 }), 0); // more running than files: clamped
  assert.equal(doneShare({ ...counts, files: 4, waiting: -1, running: 0 }), 1);
});

test("Select all is checked only when every shown library is selected (and there is one)", () => {
  const a = lib({ id: "a" }), b = lib({ id: "b" }), c = lib({ id: "c" });
  assert.equal(allShownSelected([], []), false);
  assert.equal(allShownSelected([a, b], ["a"]), false);
  assert.equal(allShownSelected([a, b], ["a", "b"]), true);
  assert.equal(allShownSelected([a, b], ["a", "b", "c"]), true); // a hidden one selected does not matter
  assert.equal(allShownSelected([a], []), false);
  assert.equal(allShownSelected([c], ["c"]), true);
});

test("Select all acts on the shown libraries only: a hidden selection is kept, not added to", () => {
  const a = lib({ id: "a" }), b = lib({ id: "b" }), hidden = lib({ id: "h" });
  assert.deepEqual(selectShown([], [a, b], true), ["a", "b"]);
  assert.deepEqual(selectShown(["h"], [a, b], true), ["h", "a", "b"]); // the hidden one stays as it was
  assert.deepEqual(selectShown(["h", "a", "b"], [a, b], false), ["h"]); // clearing leaves the hidden one selected
  assert.deepEqual(selectShown(["a"], [a, hidden], true), ["a", "h"]); // a shown one already selected is not doubled
});

test("a filter or search change drops the hidden libraries from the selection (the intersection with what is shown)", () => {
  const a = lib({ id: "a" }), b = lib({ id: "b" }), c = lib({ id: "c" });
  assert.deepEqual(keepShown(["a", "b"], [a, b, c]), ["a", "b"]); // all still shown: nothing dropped
  assert.deepEqual(keepShown(["a", "b"], [b, c]), ["b"]); // a is now hidden: dropped
  assert.deepEqual(keepShown(["3", "1", "2"], [{ id: "1" }, { id: "2" }, { id: "9" }]), ["1", "2"]); // jobs: any {id} rows, selection order kept
  assert.deepEqual(keepShown(["a", "b"], []), []); // nothing shown: the selection is empty
  assert.deepEqual(keepShown([], [a, b]), []);
  assert.deepEqual(keepShown(["b", "a"], [a, b]), ["b", "a"]); // order of the selection is kept
});

// U-A: the Jobs library select lists names in order, with numbers counted as numbers
test("libraryOptions sorts by name, numbers as numbers, case-blind", () => {
  const ls = [{ id: "c", name: "Library 10" }, { id: "a", name: "archive" }, { id: "b", name: "Library 2" }, { id: "d", name: "Films" }];
  assert.deepEqual(libraryOptions(ls).map((l) => l.name), ["archive", "Films", "Library 2", "Library 10"]);
  assert.deepEqual(ls.map((l) => l.id), ["c", "a", "b", "d"]); // the input is not reordered
});
