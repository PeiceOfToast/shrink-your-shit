import { test } from "node:test";
import assert from "node:assert/strict";
import { detailLibraryGone, libraryGone } from "./jobLibrary.ts";

const libs = [{ id: "l1" }, { id: "l2" }];

test("a job's library is removed when its id is not in the loaded list", () => {
  assert.equal(libraryGone("l9", libs), true);
  assert.equal(libraryGone("l2", libs), false);
});

test("nothing reads as removed before the library list loads, or without a library id", () => {
  assert.equal(libraryGone("l9", undefined), false);
  assert.equal(libraryGone("", libs), false);
  assert.equal(libraryGone(undefined, libs), false);
  assert.equal(libraryGone("0", libs), false);
});

test("the drawer's detail without a library object means the library is gone", () => {
  assert.equal(detailLibraryGone({}, "l9"), true);
  assert.equal(detailLibraryGone({ library: { id: "l1", name: "Movies" } }, "l1"), false);
  assert.equal(detailLibraryGone(null, "l9"), false);
  assert.equal(detailLibraryGone({}, "0"), false);
});
