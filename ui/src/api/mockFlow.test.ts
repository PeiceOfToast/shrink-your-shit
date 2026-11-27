// The workflow mock no longer carries "if a step fails" or flow variables: not in the flow, not in the guide, not in the summary.
import assert from "node:assert/strict";
import { test } from "node:test";
import { answersToFlow, checkFlow, DEFAULT_ANSWERS, getGuide, guidePreview } from "./mockFlow.ts";
import type { Answers } from "./flowTypes.ts";

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
