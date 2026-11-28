// The status line's connection bar: only an unreachable server clears it. A server-answered error leaves it alone.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { connectionAfterError } from "./connection.ts";
import { ApiError } from "./errors.ts";

test("a server-answered error (404, 403, 400) does not say the connection is lost", () => {
  assert.equal(connectionAfterError(new ApiError("workflow_not_found", "That workflow doesn't exist.")), null);
  assert.equal(connectionAfterError(new ApiError("setup_needed", "Set up first.")), null);
});

test("an unreachable server does clear it", () => {
  assert.equal(connectionAfterError(new ApiError("unreachable", "Can't reach the server.")), false);
});

test("a plain error from the page is not a connection problem", () => {
  assert.equal(connectionAfterError(new Error("boom")), null);
});

test("a failed load asks connectionAfterError, not a blanket false", () => {
  const src = readFileSync(new URL("./useApi.ts", import.meta.url), "utf8");
  assert.match(src, /connectionAfterError\(e\)/);
  assert.doesNotMatch(src, /setConnection\(false\);\n\s*setState\(\{ loading: false, error/);
});
