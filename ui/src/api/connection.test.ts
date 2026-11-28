// The status line's connection bar: only an unreachable server clears it. A server-answered error leaves it alone.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { connectionAfterError, isConnected, setConnection } from "./connection.ts";
import { ApiError } from "./errors.ts";
import { request } from "./http.ts";

const realFetch = globalThis.fetch;
const answer = (r: Response) => { globalThis.fetch = (async () => r) as typeof fetch; };

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

test("the library page asks for a workflow summary only off the real build", () => {
  const src = readFileSync(new URL("../pages/LibraryPanel.tsx", import.meta.url), "utf8");
  assert.match(src, /wfId && !isReal\("workflows"\) \? api\.getWorkflow\(wfId\)/);
});

test("a failed load asks connectionAfterError, not a blanket false", () => {
  const src = readFileSync(new URL("./useApi.ts", import.meta.url), "utf8");
  assert.match(src, /connectionAfterError\(e\)/);
  assert.doesNotMatch(src, /setConnection\(false\);\n\s*setState\(\{ loading: false, error/);
});

test("a proxy's 502 page is not the server: the connection is lost", async () => {
  setConnection(true);
  answer(new Response("<html>Bad Gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } }));
  await assert.rejects(request("GET", "/summary"), (e: unknown) => e instanceof ApiError && e.code === "unreachable");
  assert.equal(isConnected(), false);
  globalThis.fetch = realFetch;
});

test("a server-answered error (JSON envelope) keeps the connection up", async () => {
  setConnection(false);
  answer(new Response(JSON.stringify({ error: { code: "workflow_not_found", message: "That workflow doesn't exist." } }), { status: 404, headers: { "Content-Type": "application/json" } }));
  await assert.rejects(request("GET", "/workflows/1"), (e: unknown) => e instanceof ApiError && e.code === "workflow_not_found");
  assert.equal(isConnected(), true);
  globalThis.fetch = realFetch;
});

test("a successful load does not set the connection (sample data resolves without a server)", () => {
  const src = readFileSync(new URL("./useApi.ts", import.meta.url), "utf8");
  assert.doesNotMatch(src, /setConnection\(true\)/);
});

