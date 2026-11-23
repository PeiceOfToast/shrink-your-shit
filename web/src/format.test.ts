import { test } from "node:test";
import assert from "node:assert/strict";
import { bytes } from "./format.ts";

test("zero is 0 B, not 0 KB", () => {
  assert.equal(bytes(0), "0 B");
  assert.equal(bytes(2000), "2 KB");
});
