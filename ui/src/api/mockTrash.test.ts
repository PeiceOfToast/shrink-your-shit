// Settings > Trash: the list is newest first, restore removes the row, and each refusal answers with its code.
import assert from "node:assert/strict";
import { test } from "node:test";
import { getTrash, restoreTrash } from "./mockTrash.ts";
import { ApiError } from "./errors.ts";

test("the trash lists the newest first", async () => {
  const items = await getTrash();
  const times = items.map((i) => i.trashedAt);
  assert.deepEqual([...times].sort().reverse(), times);
});

test("restore removes the item from the trash", async () => {
  const before = await getTrash();
  const media = before.find((i) => i.id === "t-media");
  assert.ok(media, "the sample trash has media.mkv");
  assert.deepEqual(await restoreTrash("t-media"), { ok: true });
  assert.equal((await getTrash()).some((i) => i.id === "t-media"), false);
});

test("the three refusals answer with their codes", async () => {
  await assert.rejects(restoreTrash("t-gone"), (e: unknown) => e instanceof ApiError && e.code === "original_gone");
  await assert.rejects(restoreTrash("t-changed"), (e: unknown) => e instanceof ApiError && e.code === "converted_changed");
  await assert.rejects(restoreTrash("t-taken"), (e: unknown) => e instanceof ApiError && e.code === "name_taken");
  assert.equal((await getTrash()).some((i) => i.id === "t-gone"), true, "a refused restore leaves the item in the trash");
});

test("an unknown id is not in the trash", async () => {
  await assert.rejects(restoreTrash("nope"), (e: unknown) => e instanceof ApiError && e.code === "trash_not_found");
});
