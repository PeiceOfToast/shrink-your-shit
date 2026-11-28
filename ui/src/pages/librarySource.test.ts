// The network share form (page 09): a share that does not answer is a problem and saves nothing; a working one says Connected.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const src = readFileSync(new URL("./LibrarySource.tsx", import.meta.url), "utf8");
const test_ = src.slice(src.indexOf("const test = async"), src.indexOf("return (\n"));

test("the share test sends username, not user", () => {
  assert.match(test_, /username: value\.user \|\| undefined/);
  assert.doesNotMatch(test_, /\buser: value\.user/);
});

test("a share that does not answer shows its message and does not save the share", () => {
  const refused = test_.indexOf("if (!r.ok)");
  assert.ok(refused > 0, "the form checks r.ok");
  assert.ok(test_.indexOf("api.createShare") > refused, "createShare runs only after the ok check");
  assert.match(test_.slice(refused, test_.indexOf("api.createShare")), /onProblem\(\{ message: r\.message \}\);\s*return;/);
});

test("a working share says Connected with no folder count", () => {
  assert.match(test_, /testRes: "Connected"/);
  assert.doesNotMatch(test_, /folders/);
});

test("the banner tells the truth: the test is real, and this build can't add shares yet", () => {
  assert.doesNotMatch(src, /made up/);
  assert.match(src, /can't be added on this build yet\. The test is real/);
});

test("the share note is not the sample banner: no 'Sample data' label on the real build", () => {
  assert.doesNotMatch(src, /SampleBanner/);
  assert.doesNotMatch(src, /Sample data/);
  assert.match(src, /Network shares can't be added on this build yet\. The test is real/);
});

