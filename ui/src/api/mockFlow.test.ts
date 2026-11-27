// The workflow mock no longer carries "if a step fails" or flow variables: not in the flow, not in the guide, not in the summary.
import assert from "node:assert/strict";
import { test } from "node:test";
import { answersToFlow, checkFlow, createDraft, DEFAULT_ANSWERS, getFlow, getGuide, guidePreview, tryFlow } from "./mockFlow.ts";
import type { Answers } from "./flowTypes.ts";
import { exportWorkflow, importWorkflow } from "./mockWorkflows.ts";
import { ApiError } from "./errors.ts";

test("a flow from the guided answers has no failure rule or variables", () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, { name: "Shrink videos", description: "" });
  assert.equal("ifStepFails" in flow, false);
  assert.equal("variables" in flow, false);
});

test("the guide has no if-it-fails part", async () => {
  const guide = await getGuide();
  assert.equal(guide.parts.some((p) => p.id === "if_fails"), false);
});

test("the summary never says what happens when a step fails", async () => {
  const preview = await guidePreview(DEFAULT_ANSWERS);
  const text = JSON.stringify(preview);
  assert.equal(/If a step fails/.test(text), false);
});

const META = { name: "Shrink videos", description: "" };
const withAnswers = (a: Answers) => answersToFlow({ ...DEFAULT_ANSWERS, ...a }, META);
const disabledFor = (a: Answers, value: string) =>
  checkFlow(withAnswers(a)).disabled.find((d) => d.stepId === "save" && d.field === "container" && d.value === value);

test("subtitles and the file type land in the audio and save steps", () => {
  const flow = withAnswers({ subtitles: "my_languages", subtitleLanguages: ["eng"], fileType: "keep" });
  assert.equal(flow.steps[1].settings.subtitles, "my_languages");
  assert.deepEqual(flow.steps[1].settings.subtitleLanguages, ["eng"]);
  assert.equal(flow.steps[2].settings.container, "keep");
});

test("the default answers keep MKV and MP4 open, grey WebM (HEVC video), and raise no problem", () => {
  const check = checkFlow(withAnswers({}));
  assert.equal(disabledFor({}, "mkv"), undefined);
  assert.equal(disabledFor({}, "mp4"), undefined);
  assert.ok(disabledFor({}, "webm"));
  assert.equal(check.problems.length, 0);
});

test("MP4 is greyed for Opus audio, with its reason", () => {
  assert.equal(disabledFor({ audio: "stereo_opus" }, "mp4")?.why, "MP4 can't hold Opus audio.");
  assert.equal(disabledFor({ audio: "stereo_aac" }, "mp4"), undefined);
});

test("WebM needs AV1 video and Opus audio; with either missing it is greyed", () => {
  const why = "WebM needs AV1 video and Opus audio.";
  assert.equal(disabledFor({ format: "hevc", audio: "stereo_opus" }, "webm")?.why, why);
  assert.equal(disabledFor({ format: "av1", audio: "stereo_aac" }, "webm")?.why, why);
  assert.equal(disabledFor({ format: "av1", audio: "stereo_opus" }, "webm"), undefined);
});

test("a saved file type the rules rule out is a problem", () => {
  const check = checkFlow(withAnswers({ fileType: "webm", format: "hevc" }));
  const p = check.problems.find((x) => x.code === "container_unsupported");
  assert.equal(p?.stepId, "save");
  assert.equal(p?.field, "container");
});

test("the guided preview greys the file types the answers rule out, with the editor's reason", async () => {
  const p = await guidePreview({ ...DEFAULT_ANSWERS, audio: "stereo_opus" } as Answers);
  const mp4 = p.disabled.find((d) => d.field === "container" && d.value === "mp4");
  assert.equal(mp4?.why, "MP4 can't hold Opus audio.");
  assert.equal(p.disabled.find((d) => d.field === "container" && d.value === "mkv"), undefined);
});

test("the closed Audio card names the subtitles choice", () => {
  assert.match(checkFlow(withAnswers({ subtitles: "keep_all" })).summaries.audio ?? "", / · subtitles: keep all$/);
  assert.match(checkFlow(withAnswers({ subtitles: "my_languages", subtitleLanguages: ["eng", "ger"] })).summaries.audio ?? "", / · subtitles: English and German$/);
  assert.match(checkFlow(withAnswers({ subtitles: "text_only" })).summaries.audio ?? "", / · text only$/);
  assert.match(checkFlow(withAnswers({ subtitles: "srt" })).summaries.audio ?? "", / · SRT files$/);
});

test("the closed Save card names the file type and where the result goes", () => {
  assert.equal(checkFlow(withAnswers({ fileType: "mkv", original: "replace" })).summaries.save, "MKV · replace the original · old file in the trash 7 days");
  assert.equal(checkFlow(withAnswers({ fileType: "mkv", original: "next_to" })).summaries.save, "MKV · next to the original");
  const folder = checkFlow(withAnswers({ fileType: "mp4", original: "one_folder", folder: "/media/out" })).summaries.save ?? "";
  assert.match(folder, /^MP4 · a different folder/);
  assert.equal(folder.includes("trash"), false);
});

test("with AAC audio, MP4 is not greyed; with Opus it is", () => {
  assert.equal(disabledFor({ audio: "stereo_aac" }, "mp4"), undefined);
  assert.ok(disabledFor({ audio: "stereo_opus" }, "mp4"));
});

test("create from answers returns an id and rev 1, and the flow opens", async () => {
  const made = await createDraft({ name: "Shrink videos", answers: DEFAULT_ANSWERS });
  assert.equal(made.rev, 1);
  assert.ok(made.id);
  assert.equal((await getFlow(made.id)).flow.steps.length, 3);
});

test("create from a flow keeps that flow, and a bad flow is refused with invalid_flow", async () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, { name: "From the editor", description: "" });
  const made = await createDraft({ name: "From the editor", flow });
  assert.equal(made.rev, 1);
  assert.deepEqual((await getFlow(made.id)).flow.steps, flow.steps);
  const bad = { ...flow, steps: flow.steps.slice(0, 2) };
  await assert.rejects(createDraft({ name: "Bad", flow: bad }), (e: unknown) => e instanceof ApiError && e.code === "invalid_flow");
});

test("an export then an import opens the same flow", async () => {
  const made = await createDraft({ name: "Round trip", answers: DEFAULT_ANSWERS });
  const file = JSON.stringify(await exportWorkflow(made.id));
  const data = JSON.parse(file);
  assert.equal(data.format, "syp-workflow");
  assert.equal(data.formatVersion, 1);
  assert.equal(typeof data.appVersion, "string");
  const back = await importWorkflow(file);
  assert.notEqual(back.id, made.id);
  assert.deepEqual((await getFlow(back.id)).flow, (await getFlow(made.id)).flow);
});

test("the old app's format is refused as not_a_workflow", async () => {
  const old = JSON.stringify({ format: "shrink-your-shit.workflow", version: 1, name: "Old", description: "", kind: "video", madeWith: "editor" });
  await assert.rejects(importWorkflow(old), (e: unknown) => e instanceof ApiError && e.code === "not_a_workflow");
});

test("a try answers when it is not stopped", async () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, META);
  const r = await tryFlow(flow, "clip.mp4");
  assert.equal(r.lines.length, 3);
});

test("a try rejects with AbortError when its signal aborts", async () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, META);
  const ctl = new AbortController();
  const p = tryFlow(flow, "clip.mp4", ctl.signal);
  setTimeout(() => ctl.abort(), 50);
  await assert.rejects(p, { name: "AbortError" });
});

test("a try started with an aborted signal rejects at once", async () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, META);
  const ctl = new AbortController();
  ctl.abort();
  await assert.rejects(tryFlow(flow, "clip.mp4", ctl.signal), { name: "AbortError" });
});

// Each try line's text starts with its step's name; the editor and Guided show the text alone.
test("each try line starts with its step name", async () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, META);
  const { lines } = await tryFlow(flow, "clip.mp4");
  const name: Record<string, string> = { video: "Video:", audio: "Audio:", save: "Save:" };
  for (const l of lines) assert.ok(l.text.startsWith(name[l.stepId]), `${l.stepId}: ${l.text}`);
});
