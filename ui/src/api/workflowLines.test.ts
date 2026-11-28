// The library panel's three lines describe the published revision, with the server's sentences (steps/sentences.go).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { answersToFlow, getFlow, linesFor, publishFlow } from "./mockFlow.ts";
import type { Flow } from "./flowTypes.ts";

const meta = { name: "Shrink", description: "" };
// The contract golden internal/api/testdata/contract/workflow.json (in #928): the Go flow "Shrink videos" with audio copied.
const golden = {
  converts: "Converts video to HEVC at the Balanced setting, on the graphics card, or the processor if the card can't do the file. Keeps the audio as it is.",
  leavesAlone: "Leaves the video alone when it is already in HEVC.",
  original: "The original stays next to the new file.",
};

test("the golden's flow (audio copied) gives the golden's three lines, word for word", () => {
  assert.deepEqual(linesFor(answersToFlow({ audio: "as_is" } as never, meta), 7), golden);
});

test("the sample workflow's published flow gives its three lines from the same rules", async () => {
  const { summary } = await getFlow("shrink-videos");
  assert.match(summary!.converts, /^Converts video to HEVC at the Balanced setting, on the graphics card, or the processor if the card can't do the file\. Converts audio to AAC\.$/);
  assert.equal(summary!.original, "The original stays next to the new file.");
});

test("a flow with no published revision gives three empty lines (a draft the library does not run)", () => {
  assert.deepEqual(linesFor(undefined, 7), { converts: "", leavesAlone: "", original: "" });
});

test("replacing the original names the trash days the run reads; one day is singular; an unreadable setting gives no number", () => {
  const replace = answersToFlow({ original: "replace" } as never, meta);
  assert.equal(linesFor(replace, 7).original, "The original goes to the trash for 7 days.");
  assert.equal(linesFor(replace, 1).original, "The original goes to the trash for 1 day.");
  assert.equal(linesFor(replace, -1).original, "");
});

test("the card phrase follows the GPU setting and the fail rule", () => {
  const flow = answersToFlow({} as never, meta);
  const video = flow.steps[0];
  const onCpu: Flow = { ...flow, steps: [{ ...video, settings: { ...video.settings, useGpu: false } }, ...flow.steps.slice(1)] };
  const stops: Flow = { ...flow, steps: [{ ...video, settings: { ...video.settings, ifGpuFails: "stop" } }, ...flow.steps.slice(1)] };
  assert.match(linesFor(onCpu, 7).converts, / at the Balanced setting, on the processor\. /);
  assert.match(linesFor(stops, 7).converts, /on the graphics card, and it stops if the card can't do the file\. /);
});

test("audio: a forced conversion (a layout) drops the 'except' clause; a plain one keeps it; copy keeps it as it is", () => {
  const forced = linesFor(answersToFlow({ audio: "stereo_aac" } as never, meta), 7).converts;
  assert.match(forced, / Converts audio to AAC\.$/);
  const plain = linesFor(answersToFlow({ audio: "eac3_surround" } as never, meta), 7).converts;
  assert.match(plain, / Converts audio to E-AC-3, except tracks already in E-AC-3 and lossy tracks already at or below the target bitrate\.$/);
  assert.match(linesFor(answersToFlow({ audio: "as_is" } as never, meta), 7).converts, / Keeps the audio as it is\.$/);
});

test("leave alone names the video's format, and HDR when it is set to skip", () => {
  const flow = answersToFlow({} as never, meta);
  assert.equal(linesFor(flow, 7).leavesAlone, "Leaves the video alone when it is already in HEVC.");
  const video = flow.steps[0];
  const skip: Flow = { ...flow, steps: [{ ...video, settings: { ...video.settings, hdr: "skip" } }, ...flow.steps.slice(1)] };
  assert.equal(linesFor(skip, 7).leavesAlone, "Leaves the video alone when it is already in HEVC. Leaves HDR files alone.");
});

test("the library panel asks the flow for its lines", () => {
  const src = readFileSync(new URL("../pages/LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(src, /api\.getFlow\(wfId\)/);
  assert.doesNotMatch(src, /getWorkflow|isReal\("workflows"\)/);
});

test("the panel shows the summary block only when its lines are not empty", () => {
  const src = readFileSync(new URL("../pages/LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(src, /\{!!sum\.data\?\.summary\?\.converts && \(/);
  assert.doesNotMatch(src, /\{sum\.data\?\.summary && \(/);
});

test("a workflow never published gives empty lines, even though it has a draft", async () => {
  const { summary } = await getFlow("shrink-old-tv");
  assert.deepEqual(summary, { converts: "", leavesAlone: "", original: "" });
});

test("a draft's lines appear when it is published: the library runs that revision", async () => {
  const flow = answersToFlow({ audio: "as_is" } as never, { name: "Shrink anime", description: "" });
  assert.deepEqual((await getFlow("shrink-anime")).summary, { converts: "", leavesAlone: "", original: "" });
  await publishFlow("shrink-anime", flow);
  assert.deepEqual((await getFlow("shrink-anime")).summary, linesFor(flow, 7));
});

