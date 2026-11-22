// L1 (#646): the job page names the check step "preflight" before the encode. Node runs this (npm test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { stepLabel } from "./stepLabel.ts";

test("the preflight step reads as the check before converting", () => {
  assert.equal(stepLabel("preflight"), "Check before converting");
});

test("the encode step keeps its name", () => {
  assert.equal(stepLabel("encode"), "Converting video");
});

test("an unknown step name is shown as the server wrote it", () => {
  assert.equal(stepLabel("something_new"), "something_new");
});

test("the check step is named by what it checked", () => {
  assert.equal(stepLabel("check", ["Check: picture at 3 places, 2 s each: 0.97"]), "Checking the picture");
  assert.equal(stepLabel("check"), "Checking the new file");
});
