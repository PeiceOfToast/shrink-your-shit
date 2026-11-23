// The Libraries form's rules (libraryForm.ts): the folder must pass before a new library or a moved folder saves.
import { test } from "node:test";
import assert from "node:assert/strict";
import { filesHead, folderNeedsPass, saveEnabled } from "./libraryForm.ts";

const ok = { busy: false, changed: true, name: "Movies", pick: "wf-1", needsPass: true, folderOk: true };

test("a new library and a moved folder must pass the check; a saved folder in place need not", () => {
  assert.equal(folderNeedsPass(true, false), true);
  assert.equal(folderNeedsPass(false, true), true);
  assert.equal(folderNeedsPass(false, false), false);
});

test("Save / Add library is enabled only when idle, changed, named, with a workflow, and the folder passed when it must", () => {
  assert.equal(saveEnabled(ok), true);
  assert.equal(saveEnabled({ ...ok, busy: true }), false);
  assert.equal(saveEnabled({ ...ok, changed: false }), false);
  assert.equal(saveEnabled({ ...ok, name: "   " }), false);
  assert.equal(saveEnabled({ ...ok, pick: "" }), false);
  assert.equal(saveEnabled({ ...ok, folderOk: false }), false);
  assert.equal(saveEnabled({ ...ok, needsPass: false, folderOk: false }), true);
});

test("the Files header: 'the first 5 of 1,600' only when the folder holds more than the five shown", () => {
  assert.equal(filesHead(5, 1600), "the first 5 of 1,600");
  assert.equal(filesHead(3, 3), null);
  assert.equal(filesHead(0, 0), null);
  assert.equal(filesHead(0, 12), null);
});
