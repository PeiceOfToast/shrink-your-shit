import { test } from "node:test";
import assert from "node:assert/strict";
import { attentionRows } from "./attention.ts";

type F = { id: string; libraryId: string | null; finishedAt: string | null };
const f = (id: string, libraryId: string | null, finishedAt: string | null): F => ({ id, libraryId, finishedAt });
const base = { libraryIds: new Set(["l1", "l2", "l3"]), healthShown: false, fallbacks: 0, libraries: [] as { id: string; name: string; state: string }[] };

test("a failure from a library the page doesn't have is dropped, and the foot is flagged", () => {
  const r = attentionRows({ ...base, failures: [f("a", "l9", "2026-11-25T10:00:00Z"), f("b", "l1", "2026-11-25T09:00:00Z")] });
  assert.deepEqual(r.failures.map((x) => x.id), ["b"]);
  assert.equal(r.dropped, 1);
  assert.equal(r.none, false);
});

test("while the libraries have not loaded (null), every failure is kept and none is counted as removed", () => {
  const r = attentionRows({ ...base, libraryIds: null, failures: [f("a", "l9", "2026-11-25T10:00:00Z"), f("b", null, null)] });
  assert.deepEqual(r.failures.map((x) => x.id), ["a", "b"]);
  assert.equal(r.dropped, 0);
  assert.equal(r.none, false);
});

test("a failure with no library id is dropped too", () => {
  const r = attentionRows({ ...base, failures: [f("a", null, null)] });
  assert.equal(r.failures.length, 0);
  assert.equal(r.dropped, 1);
  assert.equal(r.none, true);
});

test("at most three failures, newest first", () => {
  const r = attentionRows({ ...base, failures: [
    f("old", "l1", "2026-11-24T00:00:00Z"), f("new", "l2", "2026-11-25T12:00:00Z"), f("mid", "l3", "2026-11-25T06:00:00Z"),
    f("older", "l1", "2026-11-23T00:00:00Z"),
  ] });
  assert.deepEqual(r.failures.map((x) => x.id), ["new", "mid", "old"]);
  assert.equal(r.dropped, 0, "a failure cut off by the three-row cap is not a removed library");
});

test("a dismissed health check is hidden; a shown one is a row", () => {
  assert.equal(attentionRows({ ...base, failures: [], healthShown: false }).none, true);
  const shown = attentionRows({ ...base, failures: [], healthShown: true });
  assert.equal(shown.health, true);
  assert.equal(shown.none, false);
});

test("encoder fall-backs are a row", () => {
  const r = attentionRows({ ...base, failures: [], fallbacks: 1 });
  assert.equal(r.fallbacks, 1);
  assert.equal(r.none, false);
});

test("each library that isn't active is a row; active ones are not", () => {
  const libs = [
    { id: "l1", name: "Movies", state: "active" },
    { id: "l2", name: "TV", state: "no_workflow" },
    { id: "l3", name: "Home", state: "paused" },
    { id: "l4", name: "NAS", state: "paused_share" },
  ];
  const r = attentionRows({ ...base, failures: [], libraries: libs });
  assert.deepEqual(r.libraries.map((l) => l.id), ["l2", "l3", "l4"]);
  assert.equal(r.none, false);
});

test("nothing to show: none is true, the card is absent", () => {
  const r = attentionRows({ ...base, failures: [], libraries: [{ id: "l1", name: "Movies", state: "active" }] });
  assert.equal(r.none, true);
  assert.deepEqual(r.failures, []);
  assert.equal(r.dropped, 0);
});
