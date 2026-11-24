import { test } from "node:test";
import assert from "node:assert/strict";
import { gcLabel } from "./gcLabel.ts";

test("each saved choice names itself", () => {
  assert.equal(gcLabel("auto"), "Automatic");
  assert.equal(gcLabel("only"), "Graphics card only");
  assert.equal(gcLabel("cpu"), "Processor only");
});

test("a missing value is the default, Automatic", () => {
  assert.equal(gcLabel(undefined), "Automatic");
  assert.equal(gcLabel(null), "Automatic");
});

test("an unknown value is not set, never Automatic", () => {
  assert.equal(gcLabel("bogus"), "not set");
  assert.equal(gcLabel(""), "not set");
  assert.equal(gcLabel(99), "not set");
});
