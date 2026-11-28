// The mock's log route answers the server's cursor: a full page carries nextBefore (its oldest seq); a short page doesn't.
import assert from "node:assert/strict";
import { test } from "node:test";
import { getLog } from "./mockLog.ts";

test("a full page carries nextBefore = its oldest seq, and paging by it reaches the start with no short cursor", async () => {
  const limit = 5;
  let before: number | undefined;
  let pages = 0;
  for (;;) {
    const p = await getLog({ limit, ...(before != null ? { before } : {}) });
    pages++;
    if (p.nextBefore == null) {
      assert.ok(p.items.length < limit, "a page without a cursor is short");
      break;
    }
    assert.equal(p.items.length, limit, "a page with a cursor is full");
    assert.equal(p.nextBefore, p.items[0].seq, "the cursor is the oldest line's seq");
    before = p.nextBefore;
    assert.ok(pages < 100, "paging ends");
  }
  assert.ok(pages > 1, "the mock log has more than one page");
});

test("a log shorter than one page carries no nextBefore", async () => {
  const p = await getLog({ limit: 100000 });
  assert.equal(p.nextBefore, undefined);
  assert.ok(p.items.length > 0);
});
