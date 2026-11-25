import { test } from "node:test";
import assert from "node:assert/strict";
import { canForget, fileName, nextForgetId, reasonLine, unreachableLine } from "./unreachable.ts";
import type { Unreachable } from "./types/v2.ts";

const row = (id: number, code: Unreachable["code"]): Unreachable => ({ id, path: `/share/${id}.mkv`, code, reason: "x" });

test("only a file that can't be found can be forgotten", () => {
  assert.equal(canForget(row(1, "not_found")), true);
  assert.equal(canForget(row(2, "in_use")), false);
  assert.equal(canForget(row(3, "ambiguous")), false);
  assert.equal(canForget(row(4, "changed")), false);
});

test("the file name is the last part of a posix or windows path", () => {
  assert.equal(fileName("/mnt/old-share/Archive/IMG_4432.mov"), "IMG_4432.mov");
  assert.equal(fileName("D:\\Archive\\Set 2.mkv"), "Set 2.mkv");
  assert.equal(fileName("plain.mkv"), "plain.mkv");
});

test("the warning line counts originals in plain words", () => {
  assert.equal(unreachableLine(1), "1 kept original couldn't be deleted.");
  assert.equal(unreachableLine(3), "3 kept originals couldn't be deleted.");
});

test("the next Forget button is the first forgettable row after the removed one", () => {
  const list = [row(1, "not_found"), row(2, "in_use"), row(3, "not_found")];
  assert.equal(nextForgetId(list, 1), 3);
  assert.equal(nextForgetId(list, 0), 1);
});

// #708 fix 2: forgetting the last forgettable row must not send focus to the warning line while an earlier Forget is on screen
test("with none after the removed row, focus goes to the first forgettable row before it", () => {
  const list = [row(7, "not_found"), row(9, "in_use")]; // list after forgetting 8, which sat at index 2
  assert.equal(nextForgetId(list, 2), 7);
  assert.equal(nextForgetId([row(1, "not_found"), row(2, "in_use")], 1), 1);
});

test("null only when no forgettable row is left anywhere, so focus goes to the warning line", () => {
  const list = [row(1, "in_use"), row(2, "changed")];
  assert.equal(nextForgetId(list, 1), null);
  assert.equal(nextForgetId(list, 5), null);
  assert.equal(nextForgetId([], 0), null);
});

test("a server reason starts with a capital on screen; empty and already-capital reasons are unchanged", () => {
  assert.equal(reasonLine("its file was not found under the library folders."), "Its file was not found under the library folders.");
  assert.equal(reasonLine("a queued or running job uses this file, so its kept original was left."), "A queued or running job uses this file, so its kept original was left.");
  assert.equal(reasonLine("Already capital."), "Already capital.");
  assert.equal(reasonLine(""), "");
});
