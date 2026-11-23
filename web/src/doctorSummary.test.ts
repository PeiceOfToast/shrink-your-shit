import { test } from "node:test";
import assert from "node:assert/strict";
import { encoderRows, healthSummary, libraryRows } from "./doctorSummary.ts";

test("all checks ok, or only absent, is green", () => {
  assert.deepEqual(healthSummary([{ status: "ok" }, { status: "absent" }]), { tone: "ok", text: "Everything checks out." });
  assert.deepEqual(healthSummary([]), { tone: "ok", text: "Everything checks out." });
});

test("a warning alone is warn, counted singular and plural", () => {
  assert.deepEqual(healthSummary([{ status: "ok" }, { status: "warn" }]), { tone: "warn", text: "1 warning." });
  assert.deepEqual(healthSummary([{ status: "warn" }, { status: "warn" }]), { tone: "warn", text: "2 warnings." });
});

test("any failure is bad, and says both counts", () => {
  assert.deepEqual(healthSummary([{ status: "bad" }, { status: "warn" }, { status: "warn" }]), { tone: "bad", text: "1 check failed, 2 warnings." });
  assert.deepEqual(healthSummary([{ status: "bad" }, { status: "bad" }]), { tone: "bad", text: "2 checks failed." });
});

const lib = (id: string, status: string, detail: string) => ({ id, label: "Library", status, detail, hint: null }) as never;

test("a library whose checks are all ok becomes one row, joined from the server's words", () => {
  const rows = libraryRows([
    lib("library:1:access", "ok", "Movies: readable and writable."),
    lib("library:1:space", "ok", "Movies: enough free space."),
    lib("library:1:hardlinks", "ok", "Movies: no hard-linked files."),
  ]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].status, "ok");
  assert.equal(rows[0].detail, "Movies: readable and writable, enough free space, no hard-linked files.");
});

test("a failing check keeps its own row, and the other libraries still join", () => {
  const rows = libraryRows([
    lib("library:1:access", "ok", "Movies: readable and writable."),
    lib("library:1:space", "bad", "Movies: 1 GB free; the largest video here needs 4 GB."),
    lib("library:2:access", "ok", "Kids: readable and writable."),
    lib("library:2:space", "ok", "Kids: enough free space."),
  ]);
  assert.deepEqual(rows.map((r) => r.id), ["library:1:access", "library:1:space", "library:2:ok"]);
  assert.equal(rows[2].detail, "Kids: readable and writable, enough free space.");
});

test("a library whose names don't line up keeps one row per check", () => {
  const cs = [lib("library:1:access", "ok", "Movies: readable and writable."), lib("library:1:space", "ok", "Films: enough free space.")];
  assert.deepEqual(libraryRows(cs).map((r) => r.id), ["library:1:access", "library:1:space"]);
});

test("encoders: the encoder in use first, the not-found rows last, the rest in order", () => {
  const cs = [
    { id: "encoder:intel", status: "absent" }, { id: "encoder:cpu", status: "ok" }, { id: "encoder:in-use", status: "ok" }, { id: "encoder:nvidia", status: "absent" },
  ].map((c) => ({ label: "", detail: "", hint: null, ...c })) as never;
  assert.deepEqual(encoderRows(cs).map((c: { id: string }) => c.id), ["encoder:in-use", "encoder:cpu", "encoder:intel", "encoder:nvidia"]);
});
