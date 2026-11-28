// Reconnecting a share is by the share's id, not the library's (POST /shares/{id}/reconnect), in the mock and in the panel.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { HOME_VIDEOS_SHARE } from "./fixtures/libraries.ts";
import { reconnectShare } from "./mockLibraries.ts";
import { runAction } from "./mock.ts";
import { ApiError } from "./errors.ts";

test("the mock reconnects a share by its share id and answers the Share", async () => {
  const share = await reconnectShare(HOME_VIDEOS_SHARE);
  assert.equal(share.id, HOME_VIDEOS_SHARE);
  assert.equal(share.connected, true);
});

test("the mock refuses a library id where a share id is meant", async () => {
  await assert.rejects(reconnectShare("home-videos"), (e: unknown) => e instanceof ApiError && e.code === "share_not_found");
});

test("the library panel reconnects with the library's share id, then reloads", () => {
  const src = readFileSync(new URL("../pages/LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(src, /await api\.reconnectShare\(L\.shareId\);/);
  assert.doesNotMatch(src, /api\.reconnectShare\(id\)/);
});

test("the mock's share route (runAction) reconnects by the share id, and refuses a library id", async () => {
  await assert.doesNotReject(runAction("POST", `/api/v2/shares/${HOME_VIDEOS_SHARE}/reconnect`));
  await assert.rejects(runAction("POST", "/api/v2/shares/home-videos/reconnect"), (e: unknown) => e instanceof ApiError && e.code === "share_not_found");
});

