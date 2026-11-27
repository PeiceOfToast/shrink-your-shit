// The real client's request paths and bodies, with a stubbed fetch (no server). Every call carries X-SYP: 1 and same-origin.
import assert from "node:assert/strict";
import { test } from "node:test";
import { request } from "../http.ts";
import { ApiError } from "../errors.ts";
import { getJobNeighbours, getJobs } from "./jobs.ts";
import { getLibraries, createLibrary } from "./libraries.ts";
import { getOverview } from "./overview.ts";

type Call = { url: string; init: RequestInit };
const realFetch = globalThis.fetch;

function stub(status: number, body: string, contentType = "application/json"): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    return new Response(body, { status, headers: { "Content-Type": contentType } });
  }) as typeof fetch;
  return calls;
}

test.afterEach(() => {
  globalThis.fetch = realFetch;
});

test("a read goes to /api/v2 with X-SYP and same-origin credentials, and parses the body", async () => {
  const calls = stub(200, "[]");
  assert.deepEqual(await getLibraries(), []);
  assert.equal(calls[0].url, "/api/v2/libraries");
  assert.equal(calls[0].init.method, "GET");
  assert.equal(calls[0].init.credentials, "same-origin");
  assert.equal((calls[0].init.headers as Record<string, string>)["X-SYP"], "1");
  assert.equal(calls[0].init.body, undefined);
});

test("a write sends its JSON body with the content type", async () => {
  const calls = stub(200, JSON.stringify({ id: "lib-1" }));
  await createLibrary({ name: "Movies", path: "/media/movies" } as never);
  assert.equal(calls[0].init.method, "POST");
  assert.equal((calls[0].init.headers as Record<string, string>)["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { name: "Movies", path: "/media/movies" });
});

test("the job list sends only the values that are set", async () => {
  const calls = stub(200, JSON.stringify({ items: [], next: null, counts: {} }));
  await getJobs({ state: "running", limit: 5, worker: undefined } as never);
  assert.equal(calls[0].url, "/api/v2/jobs?state=running&limit=5");
});

test("neighbours take the state as a query value, and the overview is its own route", async () => {
  const calls = stub(200, JSON.stringify({ prev: null, next: null }));
  await getJobNeighbours("j-1", "done");
  assert.equal(calls[0].url, "/api/v2/jobs/j-1/neighbours?state=done");
  stub(200, "{}");
  await getOverview();
});

test("an error envelope becomes an ApiError with its code, message and field", async () => {
  stub(400, JSON.stringify({ error: { code: "invalid_path", message: "Pick a folder.", field: "path" } }));
  await assert.rejects(getLibraries(), (e: unknown) => e instanceof ApiError && e.code === "invalid_path" && e.message === "Pick a folder." && e.field === "path");
});

test("a non-JSON error becomes ApiError unreachable", async () => {
  stub(502, "<html>bad gateway</html>", "text/html");
  await assert.rejects(getLibraries(), (e: unknown) => e instanceof ApiError && e.code === "unreachable");
});

test("a network failure becomes ApiError unreachable", async () => {
  globalThis.fetch = (async () => {
    throw new TypeError("fetch failed");
  }) as typeof fetch;
  await assert.rejects(request("GET", "/summary"), (e: unknown) => e instanceof ApiError && e.code === "unreachable");
});
