// The worker pages' source, pinned where the node tests can't render JSX: a refusal of one of the worker's own settings
// shows under its field, Add worker shows the not-yet sentence without a retry, and a disabled field looks disabled.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = (rel: string) => readFileSync(new URL(rel, import.meta.url), "utf8");

test("a refused worker setting is routed to its field: the four setting fields, and only those", () => {
  const page = src("./WorkerPage.tsx");
  assert.match(page, /const SETTING_FIELDS = \["slots", "gpuSlots", "hours", "primaryEncoder"\];/);
  assert.match(page, /e instanceof ApiError && e\.field && SETTING_FIELDS\.includes\(e\.field\)\) setFieldErr\(/);
  for (const field of ["slots", "gpuSlots", "hours", "primaryEncoder"]) {
    assert.match(page, new RegExp(`fieldErr\\?\\.field === "${field}"`));
  }
});

test("Add worker shows a not-yet sentence without a Try now button", () => {
  const page = src("./AddWorker.tsx");
  assert.match(page, /pair\.code === "not_yet" \? \(/);
  assert.match(page, /<p className="bad">\{pair\.error\}<\/p>/);
});

test("a disabled field shows as disabled, like a disabled button", () => {
  assert.match(src("../styles/app.css"), /\.inp:disabled,\.sel:disabled\{cursor:not-allowed;/);
});
