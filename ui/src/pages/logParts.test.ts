// The Job log's steps built from the events: the nth step_start..step_end block is steps[n-1].
import assert from "node:assert/strict";
import { test } from "node:test";
import type { JobStep, LogEvent, LogKind, LogLevel } from "../api/types.ts";
import { addLiveLine, followsBottom, keptScrollTop, levelBadges, logsFromEvents, olderCursorAfter, pagingCursor, shownCounts, showLines, showLoadOlder, startCountReads } from "./logParts.ts";
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

test("paging older lines follows the server's cursor and reaches every distinct line once", () => {
  // A fake server with the server's rules: a page is the newest `limit` raw lines before `before`, folded (a run of the same
  // line is one row whose seq is its newest raw line), with nextBefore set only when the raw page was full.
  type Raw = { seq: number; message: string };
  const raw: Raw[] = [];
  for (let s = 1; s <= 10; s++) raw.push({ seq: s, message: `line ${s}` });
  for (let s = 11; s <= 40; s++) raw.push({ seq: s, message: "sign-in refused" });
  const LIMIT = 20;
  const serve = (before?: number): LogPage => {
    const hit = raw.filter((r) => before == null || r.seq < before);
    const page = hit.slice(-LIMIT);
    const rows: LogEvent[] = [];
    for (const r of page) {
      const last = rows[rows.length - 1];
      if (last && last.message === r.message && r.message === "sign-in refused") {
        rows[rows.length - 1] = { ...last, seq: r.seq, repeat: { count: (last.repeat?.count ?? 1) + 1, firstAt: last.repeat?.firstAt ?? "", lastAt: "" } } as LogEvent;
      } else {
        rows.push({ ...liveEv(r.seq, "2026-11-28T07:00:00Z", r.message) });
      }
    }
    const full = page.length === LIMIT;
    return { items: rows, counts: { info: 0, warn: 0, error: 0 }, ...(full ? { nextBefore: page[0].seq } : {}) };
  };
  const first = serve();
  let shown = first.items;
  let cursor = pagingCursor(undefined, first.nextBefore);
  for (let guard = 0; cursor != null && guard < 10; guard++) {
    const p = serve(cursor);
    shown = [...p.items, ...shown];
    cursor = pagingCursor(olderCursorAfter(p), first.nextBefore);
  }
  const seqs = shown.map((e) => e.seq);
  assert.equal(new Set(seqs).size, seqs.length, "no row appears twice");
  for (let s = 1; s <= 10; s++) assert.ok(shown.some((e) => e.message === `line ${s}`), `line ${s} is reached`);
});

test("the older-page cursor is the server's nextBefore, not the first row's seq", () => {
  assert.equal(olderCursorAfter({ items: [], counts: { info: 0, warn: 0, error: 0 }, nextBefore: 7 }), 7);
  assert.equal(olderCursorAfter({ items: [], counts: { info: 0, warn: 0, error: 0 } }), null, "a short page without a cursor ends it");
  assert.equal(pagingCursor(undefined, 21), 21, "the first page's cursor");
  assert.equal(pagingCursor(4, 21), 4, "once an older page is loaded, its cursor");
  assert.equal(pagingCursor(null, 21), null, "an older page that ended ends the paging");
});

test("the Load older button: a short folded first page shows it; hasMore=false hides it; no lines hides it", () => {
  assert.equal(showLoadOlder(true, 3), true, "a short folded page keeps the button");
  assert.equal(showLoadOlder(false, 3), false, "once an older load came back empty, it is hidden");
  assert.equal(showLoadOlder(true, 0), false, "no lines to hang it on");
});

test("the page shows the Load older button through showLoadOlder (a revert to the folded-length guard is caught)", () => {
  const src = readFileSync(new URL("./SystemLog.tsx", import.meta.url), "utf8");
  assert.match(src, /\{showLoadOlder\(pagingCursor\(olderCursor, log\.data\?\.nextBefore\) != null, items\.length\) && \(/);
  assert.doesNotMatch(src, /base\.length >= api\.LOG_PAGE_SIZE/);
});

test("Load older pages by the paging cursor, not the first row's seq (a fold's seq is its newest line)", () => {
  const src = readFileSync(new URL("./SystemLog.tsx", import.meta.url), "utf8");
  assert.match(src, /const before = pagingCursor\(olderCursor, log\.data\?\.nextBefore\);/);
  assert.match(src, /api\.getLog\(\{ \.\.\.paramsFor\(\), before, limit: api\.LOG_PAGE_SIZE \}\)/);
  assert.doesNotMatch(src, /before: oldest/);
  assert.doesNotMatch(src, /items\[0\]\?\.seq;\s*if \(oldest/);
});

test("Follow scrolls to the bottom for a new line at the end, or when following is turned on; not for prepends, pauses or off", () => {
  assert.equal(followsBottom(10, 11, true, false, true), true, "a new newest line while following");
  assert.equal(followsBottom(10, 10, true, false, true), false, "older lines prepended: the newest is unchanged");
  assert.equal(followsBottom(10, 11, true, true, true), false, "paused: the person scrolled up");
  assert.equal(followsBottom(10, 11, false, false, false), false, "not following");
  assert.equal(followsBottom(10, 10, true, false, false), true, "Follow turned on with the same newest line: go to the newest");
});

test("Load older keeps the distance from the bottom", () => {
  // The view was 300 px above the bottom; 40 rows (800 px) are added above it.
  assert.equal(keptScrollTop(2000 + 800, 300), 2500);
});

test("the page scrolls through followsBottom and keeps the distance from the bottom through keptScrollTop", () => {
  const src = readFileSync(new URL("./SystemLog.tsx", import.meta.url), "utf8");
  assert.match(src, /followsBottom\(seen\.current\.newest, newestSeq, follow, paused, seen\.current\.on\)/);
  assert.match(src, /keptScrollTop\(el\.scrollHeight, keepFromBottom\.current\)/);
  assert.doesNotMatch(src, /\[items\.length, follow, paused\]/);
});

test("the badges show the live read while there is one, else the last load's, else zeros", () => {
  const live = { info: 5, warn: 2, error: 0 };
  const load = { info: 3, warn: 1, error: 0 };
  assert.deepEqual(shownCounts(live, load), live);
  assert.deepEqual(shownCounts(null, load), load);
  assert.deepEqual(shownCounts(null, null), { info: 0, warn: 0, error: 0 });
});

test("a count read in flight when Follow stops is dropped, not written over the new filter", async () => {
  let tick: (() => void) | null = null;
  const pending: ((c: { info: number; warn: number; error: number }) => void)[] = [];
  const written: unknown[] = [];
  const stop = startCountReads(
    () => new Promise((res) => pending.push(res)),
    (c) => written.push(c),
    4_000,
    (fn) => { tick = fn; return 1; },
    () => {},
  );
  tick!();
  stop();
  pending[0]({ info: 9, warn: 9, error: 9 });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(written, []);
});

test("a count read that lands while Follow is on is written", async () => {
  let tick: (() => void) | null = null;
  const pending: ((c: { info: number; warn: number; error: number }) => void)[] = [];
  const written: unknown[] = [];
  const stop = startCountReads(
    () => new Promise((res) => pending.push(res)),
    (c) => written.push(c),
    4_000,
    (fn) => { tick = fn; return 1; },
    () => {},
  );
  tick!();
  pending[0]({ info: 4, warn: 1, error: 0 });
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(written, [{ info: 4, warn: 1, error: 0 }]);
  stop();
});

