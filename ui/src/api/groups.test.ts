import { test } from "node:test";
import assert from "node:assert/strict";
import { REAL, sampleOnly } from "./groups.ts";

// No ?api override here (no location in node): the build's flags decide.
test("sampleOnly: a page without a group, or with its group off, shows sample data; a connected group doesn't", () => {
  assert.equal(sampleOnly(), true);
  assert.equal(REAL.overview, false);
  assert.equal(sampleOnly("overview"), true);
  assert.equal(REAL.settings, true);
  assert.equal(sampleOnly("settings"), false);
});
