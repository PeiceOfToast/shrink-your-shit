// The library panel's three workflow lines come from the flow's own settings, and only what the flow does.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { answersToFlow, getFlow, linesFor } from "./mockFlow.ts";
import type { Flow } from "./flowTypes.ts";

const meta = { name: "Shrink", description: "" };

test("the mock's getFlow sends the three lines with the flow", async () => {
  const draft = await getFlow("shrink-videos");
  assert.ok(draft.summary, "summary is present");
  assert.match(draft.summary!.converts, /^Converts video to HEVC, at (Smallest file|Smaller|Balanced|Better quality|Best quality) quality\.$/);
});

test("the lines say only what the flow does: no size, sample or extras rule", async () => {
  const { summary } = await getFlow("shrink-anime");
  const text = JSON.stringify(summary);
  assert.doesNotMatch(text, /GB|MB|samples|extras|openings|endings|over \d/);
});

test("leaves alone names the format the flow skips, not a rule it does not have", () => {
  const flow: Flow = answersToFlow({ format: "hevc" } as never, meta);
  assert.equal(linesFor(flow).leavesAlone, "Leaves alone files already in HEVC.");
});

test("replacing the original says the old file goes to the trash; saving next to it keeps it in place", () => {
  assert.equal(linesFor(answersToFlow({ original: "replace" } as never, meta)).original, "The old file goes to the trash.");
  assert.equal(linesFor(answersToFlow({ original: "next_to_original" } as never, meta)).original, "The original stays where it is.");
});

test("a picked size shows as the most the flow converts", () => {
  assert.match(linesFor(answersToFlow({ resolution: "1080" } as never, meta)).converts, /, at most 1080p\.$/);
});

test("the library panel asks the flow for its lines", () => {
  const src = readFileSync(new URL("../pages/LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(src, /api\.getFlow\(wfId\)/);
  assert.doesNotMatch(src, /getWorkflow|isReal\("workflows"\)/);
});

test("the panel hides the summary block when the lines are empty (a draft whose settings do not read yet)", () => {
  const src = readFileSync(new URL("../pages/LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(src, /\{!!sum\.data\?\.summary\?\.converts && \(/);
  assert.doesNotMatch(src, /\{sum\.data\?\.summary && \(/);
});

