import { test } from "node:test";
import assert from "node:assert/strict";
import { cancelOnTab, selectOnTab } from "./jobTabActions.ts";
import type { Tab } from "./types/jobs.ts";

test("Cancel is offered only on the waiting and running tabs", () => {
  const cancel: Record<Tab, boolean> = { waiting: true, running: true, completed: false, skipped: false, failed: false };
  for (const t of Object.keys(cancel) as Tab[]) assert.equal(cancelOnTab(t), cancel[t], t);
});

test("Select is offered on every tab except done", () => {
  assert.equal(selectOnTab("completed"), false);
  for (const t of ["waiting", "running", "skipped", "failed"] as Tab[]) assert.equal(selectOnTab(t), true, t);
});
