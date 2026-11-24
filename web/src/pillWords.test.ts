// W9 (#694): the "Not linked" words on a graph step's free ports, chosen from the step's fileOn.
import { test } from "node:test";
import assert from "node:assert/strict";
import { joinSentences, notLinked, pillWords, PILL_WORDS } from "./pillWords.ts";
import type { StepDefinition } from "./types/v2.ts";

const saved = { fileOn: { success: "saved", error: "saved", skipped: "saved", no: "saved" } } as unknown as StepDefinition;
const before = { fileOn: { success: "original", error: "original" } } as unknown as StepDefinition;
const error = { name: "error", label: "Didn't work", outcome: "error" };

test("before the save, an error port keeps the original", () => {
  assert.equal(notLinked(error, before), "Didn't work: not linked. The original file is kept.");
  assert.equal(pillWords(error, before), "Not linked: keeps the original");
});

test("before the save, another port names itself and keeps the original", () => {
  const skipped = { name: "skipped", label: "Left as it is", outcome: "skipped" };
  assert.equal(notLinked(skipped, before), "Left as it is: not linked. The original file is kept.");
});

test("after the save, an error port says a warning shows and the next steps still run", () => {
  assert.equal(notLinked(error, saved), "Didn't work: not linked. The new file is already saved; a warning is shown and the next steps still run.");
  assert.equal(pillWords(error, saved), "Not linked: carries on");
});

test("after the save, a skipped port reads the same way, with its own name", () => {
  const skipped = { name: "skipped", label: "Skipped", outcome: "skipped" };
  assert.equal(notLinked(skipped, saved), "Skipped: not linked. The new file is already saved; a warning is shown and the next steps still run.");
});

test("after the save, a question's No says nothing more happens", () => {
  const no = { name: "no", label: "Question No", outcome: "branch" };
  assert.equal(notLinked(no, saved), "Question No: not linked. The new file is already saved; nothing more happens on this path.");
  assert.equal(pillWords(no, saved), "Not linked: nothing more");
});

test("an unknown port, or no definition, falls back to the original-file words", () => {
  assert.equal(notLinked(error, undefined), "Didn't work: not linked. The original file is kept.");
  assert.equal(pillWords(error, undefined), "Not linked: keeps the original");
  const other = { fileOn: { success: "saved" } } as unknown as StepDefinition;
  assert.equal(notLinked(error, other), "Didn't work: not linked. The original file is kept.");
});

test("the wide pill has three possible words, and all are measured", () => {
  assert.deepEqual(PILL_WORDS, ["Not linked: keeps the original", "Not linked: carries on", "Not linked: nothing more"]);
});

test("an aria-label joins its parts without a double full stop", () => {
  assert.equal(joinSentences(["Name", "Summary", notLinked(error, before)]), "Name. Summary. Didn't work: not linked. The original file is kept.");
  assert.equal(joinSentences(["Name.", "", "Warning: x."]), "Name. Warning: x.");
});

// the revision's per-node fileOn (the server's answer for this save) beats the definition's; the definition is only the fallback
test("the revision's per-port state beats the definition's", () => {
  const nodeSaved = { success: "saved", error: "saved" };
  assert.equal(pillWords(error, before, nodeSaved), "Not linked: carries on");
  assert.equal(notLinked(error, before, nodeSaved), "Didn't work: not linked. The new file is already saved; a warning is shown and the next steps still run.");
  assert.equal(pillWords(error, saved, { error: "original" }), "Not linked: keeps the original");
});

// #704: the editable graph's pill must read the revision's state too; a question's No after the save, on a definition whose port isn't 'saved'
test("the revision's state for a question's No reads 'nothing more' even when the definition says otherwise", () => {
  const no = { name: "no", label: "Question No", outcome: "branch" };
  assert.equal(pillWords(no, before, { no: "saved" }), "Not linked: nothing more");
  assert.equal(pillWords(no, before, undefined), "Not linked: keeps the original");
});

test("a revision entry without this port reads as not saved", () => {
  assert.equal(pillWords(error, saved, {}), "Not linked: keeps the original");
});

test("no revision entry falls back to the definition", () => {
  assert.equal(pillWords(error, saved, undefined), "Not linked: carries on");
  assert.equal(pillWords(error, before, undefined), "Not linked: keeps the original");
});
