// The Job log's steps built from the events: the nth step_start..step_end block is steps[n-1].
import assert from "node:assert/strict";
import { test } from "node:test";
import type { JobStep, LogEvent, LogKind, LogLevel } from "../api/types.ts";
import { addLiveLine, levelBadges, logsFromEvents, olderPageEnds, showLines } from "./logParts.ts";
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

test("a fold of a line from the initial load replaces that line on the page, not a second copy", () => {
  const loaded = [liveEv(2, "2026-11-28T06:59:00Z", "scan started"), liveEv(3, "2026-11-28T07:00:00Z", "sign-in refused")];
  const fold = liveEv(4, "2026-11-28T07:00:09Z", "sign-in refused", { count: 2, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:09Z" });
  const shown = showLines(loaded, [fold]);
  assert.deepEqual(shown.map((l) => l.seq), [2, 4]);
  assert.equal(shown[1].repeat?.count, 2);
});

test("a reload that already has the live lines does not add them again (Follow on, refresh)", () => {
  const a = liveEv(1, "2026-11-28T07:00:00Z", "one");
  const b = liveEv(2, "2026-11-28T07:00:01Z", "two");
  const c = liveEv(3, "2026-11-28T07:00:02Z", "three");
  const reloaded = [a, b, c];
  assert.deepEqual(showLines(reloaded, [b, c]), reloaded);
});

test("a later fold over a row that is already folded replaces that row, not a second copy", () => {
  // The server answers a folded line as one row (seq 4, count 2); the page holds only that row.
  const folded = liveEv(4, "2026-11-28T07:00:09Z", "sign-in refused", { count: 2, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:09Z" });
  const again = liveEv(5, "2026-11-28T07:00:12Z", "sign-in refused", { count: 3, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:12Z" });
  const shown = showLines([folded], [again]);
  assert.equal(shown.length, 1);
  assert.equal(shown[0].seq, 5);
  assert.equal(shown[0].repeat?.count, 3);
});

test("the system log page builds its lines with showLines (a source check)", async () => {
  const { readFileSync } = await import("node:fs");
  const src = readFileSync(new URL("./SystemLog.tsx", import.meta.url), "utf8");
  assert.match(src, /showLines\(loaded, live\)/);
});

test("an older live fold does not replace a newer fold the page already holds", () => {
  // A reload holds the fold at seq 6 (count 4); a late live fold of the same run is seq 5 (count 3) and must not win.
  const newer = liveEv(6, "2026-11-28T07:00:15Z", "sign-in refused", { count: 4, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:15Z" });
  const older = liveEv(5, "2026-11-28T07:00:12Z", "sign-in refused", { count: 3, firstAt: "2026-11-28T07:00:00Z", lastAt: "2026-11-28T07:00:12Z" });
  assert.deepEqual(showLines([newer], [older]), [newer]);
});

test("a run longer than the poll window holds one row across polls (base page 20, Follow polls 50)", () => {
  // 60 identical refusals, one per second. The base page is the newest 20 (a fold of seq 60, count 20); each poll reads the
  // newest 50, so its fold's firstAt slides with every poll. The page must still show one row for the run.
  const at = (k: number) => new Date(Date.UTC(2026, 10, 28, 7, 0, 0) + k * 1000).toISOString();
  const run = (seq: number, count: number) => liveEv(seq, at(seq), "sign-in refused", { count, firstAt: at(seq - count + 1), lastAt: at(seq) });
  let shown = [run(60, 20)];
  for (let n = 61; n <= 64; n++) shown = showLines(shown, [run(n, 50)]);
  assert.equal(shown.length, 1);
  assert.equal(shown[0].seq, 64);
  assert.equal(shown[0].repeat?.count, 50);
});

test("the level badges are the server's counts: live lines are not added on top", () => {
  const server = { info: 40, warn: 4, error: 1 };
  const warn = { ...liveEv(9, "2026-11-28T07:00:09Z", "sign-in refused"), level: "warn" } as LogEvent;
  const folded = { ...liveEv(10, "2026-11-28T07:00:19Z", "sign-in refused", { count: 3, firstAt: "2026-11-28T07:00:09Z", lastAt: "2026-11-28T07:00:19Z" }), level: "warn" } as LogEvent;
  assert.deepEqual(levelBadges(server, [warn, folded]), server);
  assert.deepEqual(levelBadges(server, []), server);
});

test("Load older: a short folded page keeps the button; only an empty page ends it", () => {
  const PAGE = 20;
  const fold = { ...liveEv(30, "2026-11-28T07:00:30Z", "sign-in refused", { count: 40, firstAt: "2026-11-28T06:59:00Z", lastAt: "2026-11-28T07:00:30Z" }) } as LogEvent;
  const short = [fold];
  assert.ok(short.length < PAGE, "a folded page can be shorter than the page size");
  assert.equal(olderPageEnds(short), false);
  assert.equal(olderPageEnds([]), true);
});

