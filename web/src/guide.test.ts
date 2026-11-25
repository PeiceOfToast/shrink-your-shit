import { test } from "node:test";
import assert from "node:assert/strict";
import { applyGoal, basicDoesntAsk, basicSave, canBasic, hasAfter, questionCount, skipOn, subtitleChoices, QUESTION_OF_FIELD } from "./guide.ts";
import { GUIDE_INFO } from "./mock/guide.ts";

const info = GUIDE_INFO;
const noTargets = { ...info, available: { ...info.available, after: { mediaServer: false, downloader: false, notifyTargets: [] } } };

test("a Basic save sends the answers it shows and the Advanced settings it read back, unchanged", () => {
  const readBack = { ...info.defaults.smaller, more: { "video.convert": { rateControl: "crf", crf: 22 } } };
  const edits = { ...info.defaults.smaller, quality: 4 };
  const sent = basicSave(readBack, edits);
  assert.deepEqual(sent.more, readBack.more);
  assert.equal(sent.quality, 4);
  assert.equal(basicSave(null, edits).more, undefined);
});

test("Q10 starts unticked for every goal, and the original is never Replace by default", () => {
  for (const g of ["smaller", "every_device", "both"] as const) {
    const d = info.defaults[g];
    assert.deepEqual(d.after, { mediaServer: false, downloader: false, notify: false, notifyTarget: "" });
    assert.notEqual(d.original, "replace");
    assert.equal(d.ifBigger, "keep");
  }
});

test("choosing a goal sets its defaults and keeps the person's languages, folder, Advanced settings and Q9/Q10", () => {
  const cur = { ...info.defaults.smaller, languages: ["deu"], folder: "/media/out", more: { "audio.tracks": { loudness: true } }, skip: { minSizeGB: 2, minResolution: 0, minMinutes: 0 } };
  const next = applyGoal(cur, "every_device", info);
  assert.equal(next.goal, "every_device");
  assert.equal(next.format, "h264");
  assert.equal(next.fileType, "mp4");
  assert.equal(next.subtitles, "text_only");
  assert.deepEqual(next.languages, ["deu"]);
  assert.equal(next.folder, "/media/out");
  assert.deepEqual(next.more, cur.more);
  assert.deepEqual(next.skip, cur.skip);
});

test("choosing the goal already chosen changes nothing", () => {
  const cur = { ...info.defaults.smaller, quality: 5 };
  assert.equal(applyGoal(cur, "smaller", info), cur);
});

test("Q10 opens only when a connection or a notification target exists", () => {
  assert.equal(hasAfter(info.available.after), true);
  assert.equal(questionCount(info), 10);
  assert.equal(questionCount(noTargets), 9);
});

test("what Basic doesn't ask about is named from the server's step names and the Advanced step types", () => {
  const words = basicDoesntAsk(["Tag the file"], { "video.convert": {}, "audio.tracks": {} });
  assert.deepEqual(words, ["Tag the file", "Convert video's rate control", "Audio's even loudness"]);
  assert.deepEqual(basicDoesntAsk(["Tag the file"], { x: {} }), ["Tag the file", "x"]);
  assert.deepEqual(basicDoesntAsk([], undefined), []);
});

test("a workflow the questions can't read has no Basic view", () => {
  assert.equal(canBasic({ coverage: "none", answers: null, extra: [], why: "A Convert step in selectors kind." }), false);
  assert.equal(canBasic({ coverage: "partial", answers: info.defaults.smaller, extra: ["Tag the file"] }), true);
});

test("a server refusal names the question it belongs to", () => {
  assert.equal(QUESTION_OF_FIELD.format, 3);
  assert.equal(QUESTION_OF_FIELD.folder, 8);
  assert.equal(QUESTION_OF_FIELD.after, 10);
});

test("Q9 conditions are on when their value is above zero", () => {
  assert.deepEqual(skipOn({ minSizeGB: 0, minResolution: 0, minMinutes: 0 }), { size: false, res: false, minutes: false });
  assert.deepEqual(skipOn({ minSizeGB: 2, minResolution: 1080, minMinutes: 0 }), { size: true, res: true, minutes: false });
});

test("Q6 offers the .srt choice only when the server offers srt, in the plan's order", () => {
  assert.deepEqual(subtitleChoices(["srt", "keep_all", "text_only"]), ["keep_all", "text_only", "srt"]);
  assert.equal(subtitleChoices(["keep_all", "my_languages", "text_only"]).includes("srt"), false);
  assert.deepEqual(subtitleChoices(["keep_all", "vtt"]), ["keep_all", "vtt"]);
});
