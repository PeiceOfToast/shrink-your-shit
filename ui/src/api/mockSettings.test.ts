// The version and the update check are read-only; the update line follows the dev state "update".
import assert from "node:assert/strict";
import { test } from "node:test";
import { getSettings, patchSettings } from "./mockSettings.ts";
import { setForcedState } from "./mockState.ts";
import { ApiError } from "./errors.ts";

test("version and updateAvailable are read-only: a PATCH naming either is refused with read_only", async () => {
  await assert.rejects(patchSettings({ general: { version: "9.9.9" } as never }), (e: unknown) => e instanceof ApiError && e.code === "read_only");
  await assert.rejects(patchSettings({ general: { updateAvailable: null } as never }), (e: unknown) => e instanceof ApiError && e.code === "read_only");
});

test("the running version is shown, and no update by default", async () => {
  const s = await getSettings();
  assert.equal(s.general.version, "0.9.4");
  assert.equal(s.general.updateAvailable, null);
});

test("the dev state 'update' shows a newer release with its notes link", async () => {
  setForcedState("update");
  try {
    const s = await getSettings();
    assert.equal(s.general.updateAvailable?.version, "0.9.5");
    assert.match(s.general.updateAvailable?.notesUrl ?? "", /^https:\/\//);
  } finally {
    setForcedState("none");
  }
});
