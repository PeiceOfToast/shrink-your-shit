import { test } from "node:test";
import assert from "node:assert/strict";
import { FILE_TYPE_HINT, fileTypeHint } from "./fileType.ts";

test("with no greyed type the hint is the MKV and MP4 line alone", () => {
  assert.equal(fileTypeHint([]), FILE_TYPE_HINT);
  assert.ok(FILE_TYPE_HINT.startsWith("MKV holds every track"));
});

test("a greyed type's reason is added as its own sentence, with no type name in front", () => {
  const text = fileTypeHint(["WebM needs AV1 video and Opus audio."]);
  assert.equal(text, `${FILE_TYPE_HINT} WebM needs AV1 video and Opus audio.`);
  assert.equal(/(^|\s)(MKV|MP4|WebM):/.test(text.slice(FILE_TYPE_HINT.length)), false);
});
