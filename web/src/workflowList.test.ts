// M3b: the Workflows list's filter rule, counts and combined rows. Node runs these (npm test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { hasProblems, inWfFilter, shownWorkflows, wfFilterCounts } from "./workflowList.ts";
import type { Workflow } from "./types/v2.ts";

function wf(p: Partial<Workflow> & { validation?: Partial<Workflow["validation"]> } = {}): Workflow {
  const { validation, ...rest } = p;
  return {
    id: "w1", name: "Shrink", description: "", fileType: "video", status: "enabled", favourite: false, tags: [],
    presetId: "shrink_videos", summaryTag: "", publishedRevisionId: "r1", version: 1, draftDiffers: false,
    validation: { errors: 0, warnings: 0, ...validation }, libraries: [], activeJobs: 0,
    lastRun: { at: null, completed: 0, failed: 0, skipped: 0 }, changesOriginals: false,
    createdAt: "2026-11-01T00:00:00Z", changedAt: "2026-11-01T00:00:00Z", ...rest,
  } as Workflow;
}

test("With problems counts errors and warnings", () => {
  assert.equal(hasProblems(wf()), false);
  assert.equal(hasProblems(wf({ validation: { errors: 1 } })), true);
  assert.equal(hasProblems(wf({ validation: { warnings: 2 } })), true);
});

test("Enabled is status enabled only; draft and disabled are not in it", () => {
  assert.equal(inWfFilter(wf({ status: "enabled" }), "enabled"), true);
  assert.equal(inWfFilter(wf({ status: "disabled" }), "enabled"), false);
  assert.equal(inWfFilter(wf({ status: "draft" }), "enabled"), false);
});

test("All is every workflow", () => {
  assert.equal(inWfFilter(wf({ status: "draft" }), "all"), true);
});

test("counts come from the whole list, one bucket per filter", () => {
  const ws = [
    wf({ id: "a", status: "enabled" }),
    wf({ id: "b", status: "enabled", validation: { errors: 1 } }),
    wf({ id: "c", status: "disabled", validation: { warnings: 1 } }),
    wf({ id: "d", status: "draft" }),
  ];
  assert.deepEqual(wfFilterCounts(ws), { all: 4, enabled: 2, problems: 2 });
});

test("counts of an empty list are zero", () => {
  assert.deepEqual(wfFilterCounts([]), { all: 0, enabled: 0, problems: 0 });
});

test("the filter, search, tag and favourites combine", () => {
  const ws = [
    wf({ id: "a", name: "Movies shrink", status: "enabled", tags: ["hevc"], favourite: true }),
    wf({ id: "b", name: "TV shrink", description: "for episodes", status: "enabled", tags: ["hevc"] }),
    wf({ id: "c", name: "Old one", status: "disabled", tags: ["x265"], favourite: true }),
    wf({ id: "d", name: "Broken", status: "enabled", validation: { errors: 1 }, tags: ["hevc"] }),
  ];
  const ids = (o: Parameters<typeof shownWorkflows>[1]) => shownWorkflows(ws, o).map((w) => w.id);
  assert.deepEqual(ids({ filter: "all", q: "", tag: "", fav: false }), ["a", "b", "c", "d"]);
  assert.deepEqual(ids({ filter: "enabled", q: "", tag: "", fav: false }), ["a", "b", "d"]);
  assert.deepEqual(ids({ filter: "problems", q: "", tag: "", fav: false }), ["d"]);
  assert.deepEqual(ids({ filter: "all", q: "EPISODES", tag: "", fav: false }), ["b"]);
  assert.deepEqual(ids({ filter: "all", q: "", tag: "x265", fav: false }), ["c"]);
  assert.deepEqual(ids({ filter: "enabled", q: "", tag: "", fav: true }), ["a"]);
  assert.deepEqual(ids({ filter: "enabled", q: "shrink", tag: "hevc", fav: false }), ["a", "b"]);
});

test("a search that matches nothing gives no rows, and counts still come from the whole list", () => {
  const ws = [wf({ id: "a", name: "Movies" }), wf({ id: "b", name: "TV", validation: { errors: 1 } })];
  assert.deepEqual(shownWorkflows(ws, { filter: "problems", q: "zzz", tag: "", fav: false }), []);
  assert.deepEqual(wfFilterCounts(ws), { all: 2, enabled: 2, problems: 1 });
});
