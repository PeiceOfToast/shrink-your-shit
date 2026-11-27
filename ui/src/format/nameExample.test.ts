import { test } from "node:test";
import assert from "node:assert/strict";
import { nameExample } from "./nameExample.ts";

test("empty suffix shows only the keep-the-name line", () => {
  assert.equal(nameExample("mkv", ""), "Empty keeps the name.");
  assert.equal(nameExample("keep", ""), "Empty keeps the name.");
});

test("typed suffix shows the file it becomes", () => {
  assert.equal(nameExample("mkv", "small"), "media.mkv becomes media-small.mkv.");
  assert.equal(nameExample("mp4", "small"), "media.mp4 becomes media-small.mp4.");
});

test("keep the same shows the source's own extension", () => {
  assert.equal(nameExample("keep", "small"), "Keeps the file's own extension: media.mov becomes media-small.mov.");
});
