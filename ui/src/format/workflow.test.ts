import { test } from "node:test";
import assert from "node:assert/strict";
import { refusedText } from "./workflow.ts";

test("refusedText names one, two or three libraries", () => {
  assert.equal(refusedText(["Movies"]), "Movies uses this. Pick another workflow for it first.");
  assert.equal(refusedText(["Movies", "TV Shows"]), "Movies and TV Shows use this. Pick another workflow for them first.");
  assert.equal(refusedText(["Movies", "TV Shows", "Home videos"]), "Movies, TV Shows and Home videos use this. Pick another workflow for them first.");
});
