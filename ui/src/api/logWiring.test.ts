// The System log's wiring: the real client overlays the mock only with its flag on, and the mock's seen reply is the server's.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { markLogSeen } from "./mockLog.ts";

const src = (rel: string) => readFileSync(new URL(rel, import.meta.url), "utf8");

test("the real log module is overlaid onto the api, under the log group", () => {
  assert.match(src("./index.ts"), /import \* as realLog from "\.\/real\/log\.ts";/);
  assert.match(src("./index.ts"), /\n  log: realLog,\n/);
});

test("the log group's flag is off until the server's log routes are live", () => {
  assert.match(src("./groups.ts"), /\n  log: false,\n/);
});

test("the mock's seen reply is the server's {seenSeq}", async () => {
  const r = await markLogSeen(20);
  assert.equal(r.seenSeq, 20);
  assert.equal("ok" in (r as object), false);
});
