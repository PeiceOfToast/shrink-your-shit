// The live stream against a fake EventSource: parsing, the view map, the 45 s rule, the job filter, reconnect, close.
import assert from "node:assert/strict";
import { test } from "node:test";
import { isStale, openEvents, parseFrame, viewsFor } from "./events.ts";

class FakeSource {
  static made: FakeSource[] = [];
  listeners = new Map<string, ((e: { lastEventId: string; data: string }) => void)[]>();
  closed = false;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeSource.made.push(this);
  }
  addEventListener(name: string, fn: (e: { lastEventId: string; data: string }) => void): void {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), fn]);
  }
  emit(name: string, id: string, data: unknown): void {
    for (const fn of this.listeners.get(name) ?? []) fn({ lastEventId: id, data: typeof data === "string" ? data : JSON.stringify(data) });
  }
  close(): void {
    this.closed = true;
  }
}

function harness(start = 1_000) {
  FakeSource.made = [];
  let now = start;
  let tick: (() => void) | null = null;
  let stopped = 0;
  const seen = { frames: [] as string[], resyncs: 0, summaries: 0 };
  const close = openEvents({
    EventSourceCtor: FakeSource as never,
    clock: () => now,
    every: (fn) => {
      tick = fn;
      return () => {
        stopped++;
      };
    },
    onFrame: (f) => seen.frames.push(f.kind),
    onResync: () => seen.resyncs++,
    onSummary: () => seen.summaries++,
  });
  return { close, seen, advance: (ms: number) => (now += ms), tick: () => tick?.(), stopped: () => stopped };
}

test("a frame parses to its seq, kind and JSON data; bad JSON is dropped", () => {
  assert.deepEqual(parseFrame("42", "job.done", '{"id":"j-1"}'), { seq: 42, kind: "job.done", data: { id: "j-1" } });
  assert.equal(parseFrame("1", "job.done", "not json"), null);
  assert.equal(parseFrame("", "summary", "{}")?.seq, 0);
});

test("each kind refreshes the right views; progress refreshes none", () => {
  assert.deepEqual(viewsFor("job.done"), ["jobs", "overview"]);
  assert.deepEqual(viewsFor("scan.progress"), ["libraries", "overview"]);
  assert.deepEqual(viewsFor("library.changed"), ["libraries", "overview"]);
  assert.deepEqual(viewsFor("summary"), ["summary"]);
  assert.deepEqual(viewsFor("resync"), ["all"]);
  assert.deepEqual(viewsFor("progress"), []);
});

test("no frame for 45 s is stale; 44.9 s is not", () => {
  assert.equal(isStale(0, 44_999), false);
  assert.equal(isStale(0, 45_000), true);
});

test("the stream opens on /api/v2/events, or on ?job=<id> for one job's log", () => {
  harness();
  assert.equal(FakeSource.made[0].url, "/api/v2/events");
  FakeSource.made = [];
  openEvents({ job: "j 1", EventSourceCtor: FakeSource as never, every: () => () => {}, onFrame: () => {}, onResync: () => {} });
  assert.equal(FakeSource.made[0].url, "/api/v2/events?job=j%201");
});

test("frames reach the page in order; a summary frame also goes to the summary; resync is its own callback", () => {
  const h = harness();
  const src = FakeSource.made[0];
  src.emit("job.queued", "1", { id: "j-1" });
  src.emit("summary", "2", { converting: { running: 1 } });
  src.emit("resync", "3", {});
  assert.deepEqual(h.seen.frames, ["job.queued", "summary"]);
  assert.equal(h.seen.summaries, 1);
  assert.equal(h.seen.resyncs, 1);
});

test("a stale stream is closed, reopened and resynced", () => {
  const h = harness();
  const first = FakeSource.made[0];
  h.advance(44_000);
  h.tick();
  assert.equal(first.closed, false, "44 s is not stale yet");
  h.advance(1_000);
  h.tick();
  assert.equal(first.closed, true);
  assert.equal(FakeSource.made.length, 2);
  assert.equal(h.seen.resyncs, 1);
});

test("close shuts the stream and stops the stale check", () => {
  const h = harness();
  const src = FakeSource.made[0];
  h.close();
  assert.equal(src.closed, true);
  assert.equal(h.stopped(), 1);
});

test("a ping keeps a quiet stream alive without reaching the page", () => {
  const h = harness();
  const src = FakeSource.made[0];
  h.advance(40_000);
  src.emit("ping", "", "");
  h.advance(40_000);
  h.tick();
  assert.equal(src.closed, false, "a ping 40 s ago is not stale");
  assert.equal(h.seen.frames.length, 0, "a ping is not a frame");
  assert.equal(h.seen.resyncs, 0);
});

