// The Job log's steps built from the events: the nth step_start..step_end block is steps[n-1].
import assert from "node:assert/strict";
import { test } from "node:test";
import type { JobStep, LogEvent, LogKind, LogLevel } from "../api/types.ts";
import { addLiveLine, logsFromEvents } from "./logParts.ts";
import { readFileSync } from "node:fs";

const steps: JobStep[] = [
  { index: 1, name: "Video", outcome: "done", durationMs: 0 },
  { index: 2, name: "Audio", outcome: "done", durationMs: 0 },
  { index: 3, name: "Save", outcome: "done", durationMs: 0 },
];

let seq = 0;
function ev(kind: LogKind, message: string, level: LogLevel = "info"): LogEvent {
  seq += 1;
  return {
    seq,
    ts: `2026-01-01T00:00:${String(seq).padStart(2, "0")}.000Z`,
    level,
    kind,
    source: "",
    message,
    jobId: "1",
    libraryId: "1",
    workerId: "local",
  };
}

// The shape of a real run (made-up names): lines before the first step, three step blocks, lines after the last.
function runEvents(): LogEvent[] {
  seq = 0;
  return [
    ev("queued", "queued"),
    ev("picked", "picked"),
    ev("run_start", "Starting sample.mp4"),
    ev("probe", ""),
    ev("step_start", ""),
    ev("decision", "Decision one"),
    ev("step_end", "Decision one"),
    ev("step_start", ""),
    ev("decision", "Decision two"),
    ev("step_end", "Decision two"),
    ev("step_start", ""),
    ev("decision", "Decision three"),
    ev("step_end", "Decision three"),
    ev("command", "example-encoder -i in -o out"),
    ev("size", ""),
    ev("move", "sample-out.mkv"),
    ev("probe", ""),
    ev("run_end", "Done"),
    ev("note", "smaller"),
  ];
}

test("each block goes to its step; the decision and command attach to it", () => {
  const out = logsFromEvents(steps, runEvents());
  assert.deepEqual(out.map((s) => s.name), ["Video", "Audio", "Save"]);
  assert.deepEqual(out.map((s) => s.decision), ["Decision one", "Decision two", "Decision three"]);
  assert.equal(out[0].command, null);
  assert.equal(out[2].command, "example-encoder -i in -o out");
});

test("lines before the first step go to step 1; lines after the last step go to the last step", () => {
  const out = logsFromEvents(steps, runEvents());
  assert.deepEqual(out[0].lines.map((l) => l.text), ["queued", "picked", "Starting sample.mp4"]);
  assert.deepEqual(out[1].lines.map((l) => l.text), []);
  assert.deepEqual(out[2].lines.map((l) => l.text), ["sample-out.mkv", "Done", "smaller"]);
});

test("step markers and empty messages are not lines; decision and command are not lines", () => {
  const texts = logsFromEvents(steps, runEvents()).flatMap((s) => s.lines.map((l) => l.text));
  for (const gone of ["Decision one", "Decision two", "Decision three", "example-encoder -i in -o out"]) {
    assert.ok(!texts.includes(gone), `${gone} should not be a line`);
  }
  assert.equal(texts.includes(""), false);
});

test("warn and error keep their level; debug shows as info", () => {
  const warn = ev("warning", "Slow disk", "warn");
  const events = [ev("step_start", ""), warn, ev("error", "Failed once", "error"), ev("note", "Quiet", "debug")];
  const lines = logsFromEvents(steps, events)[0].lines;
  assert.deepEqual(lines.map((l) => l.level), ["warn", "error", "info"]);
  assert.equal(lines[0].time, warn.ts);
});

test("no steps gives no log steps", () => {
  assert.deepEqual(logsFromEvents([], runEvents()), []);
});

test("more step blocks than steps: the extra lines stay on the last step", () => {
  const events = [ev("step_start", ""), ev("step_end", ""), ev("step_start", ""), ev("step_end", ""), ev("step_start", ""), ev("step_end", ""), ev("step_start", ""), ev("note", "extra")];
  const out = logsFromEvents(steps, events);
  assert.deepEqual(out[2].lines.map((l) => l.text), ["extra"]);
});

test("an empty decision or command does not replace an earlier one", () => {
  const events = [ev("step_start", ""), ev("decision", "First"), ev("decision", ""), ev("command", "run it"), ev("command", "")];
  const out = logsFromEvents(steps, events);
  assert.equal(out[0].decision, "First");
  assert.equal(out[0].command, "run it");
});

test("only queued and picked: no steps reached, so none come back", () => {
  const events = [ev("queued", "queued"), ev("picked", "picked")];
  assert.deepEqual(logsFromEvents(steps, events), []);
});

test("a run that failed in step 1 of 3 shows step 1 alone, with its lines after the failure", () => {
  const events = [
    ev("queued", "queued"),
    ev("run_start", "Starting sample.mp4"),
    ev("step_start", ""),
    ev("decision", "Decision one"),
    ev("error", "Could not read the video", "error"),
    ev("note", "Check the file and try again"),
  ];
  const out = logsFromEvents(steps, events);
  assert.deepEqual(out.map((s) => s.name), ["Video"]);
  assert.deepEqual(out[0].lines.map((l) => l.text), ["queued", "Starting sample.mp4", "Could not read the video", "Check the file and try again"]);
  assert.equal(out[0].decision, "Decision one");
});

// Follow: a folded repeat replaces the line it folds, so the page shows it once.
const liveEv = (seq: number, ts: string, message: string, repeat?: { count: number; firstAt: string; lastAt: string }) =>
  ({ seq, ts, level: "info", kind: "note", source: "Scanner", message, jobId: null, libraryId: null, workerId: null, ...(repeat ? { repeat } : {}) }) as LogEvent;

test("a folded repeat replaces the line it folds (same message, the first time) instead of adding a second", () => {
  const first = liveEv(3, "2026-11-28T07:00:00Z", "sign-in refused");
  const folded = liveEv(4, "2026-11-28T07:00:09Z", "sign-in refused", { count: 2, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:09Z" });
  const out = addLiveLine([first], folded);
  assert.equal(out.length, 1);
  assert.equal(out[0].seq, 4);
  assert.equal(out[0].repeat?.count, 2);
});

test("a new line, or one with no repeat, is added after the lines shown", () => {
  const a = liveEv(1, "2026-11-28T07:00:00Z", "one");
  const b = liveEv(2, "2026-11-28T07:00:01Z", "two");
  assert.deepEqual(addLiveLine([a], b).map((l) => l.seq), [1, 2]);
  const other = liveEv(5, "2026-11-28T07:00:05Z", "sign-in refused", { count: 2, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:05Z" });
  assert.deepEqual(addLiveLine([a], other).map((l) => l.seq), [1, 5], "a repeat of a different line is added");
});

test("the page adds live lines with the helper, so a fold replaces its line", () => {
  const page = readFileSync(new URL("./SystemLog.tsx", import.meta.url), "utf8");
  assert.match(page, /setLive\(\(l\) => addLiveLine\(l, e\)\)/);
});
