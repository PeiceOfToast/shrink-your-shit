// Health while the server starts: a 503 "starting" asks again after a wait; any other state is left alone.
import { test } from "node:test";
import assert from "node:assert/strict";
import { retryWhileStarting } from "./startingRetry.ts";

const later = (ms: number) => new Promise((r) => setTimeout(r, ms));

test("starting: the page asks again after its wait, and stops when unmounted", async () => {
  let asked = 0;
  const cleanup = retryWhileStarting("starting", () => asked++, 10);
  assert.equal(typeof cleanup, "function");
  await later(30);
  assert.equal(asked, 1);
  const cancel = retryWhileStarting("starting", () => asked++, 10);
  cancel?.();
  await later(30);
  assert.equal(asked, 1, "a cancelled wait does not ask again");
});

test("any other state is not waited for: no ask, no cleanup", async () => {
  let asked = 0;
  assert.equal(retryWhileStarting(undefined, () => asked++, 5), undefined);
  assert.equal(retryWhileStarting("unreachable", () => asked++, 5), undefined);
  assert.equal(retryWhileStarting("signed_out", () => asked++, 5), undefined);
  await later(20);
  assert.equal(asked, 0);
});
