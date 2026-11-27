// The real client's request paths and bodies, with a stubbed fetch (no server). Every call carries X-SYP: 1 and same-origin.
import assert from "node:assert/strict";
import { test } from "node:test";
import { request } from "../http.ts";
import { ApiError } from "../errors.ts";
import { getJobNeighbours, getJobs } from "./jobs.ts";
import { getLibraries, createLibrary } from "./libraries.ts";
import { getAttention, getOverview } from "./overview.ts";
import { createDraft, deleteWorkflow, duplicateWorkflow, exportWorkflow, getFlow, listWorkflows, publishFlow, putDraft, validateFlow } from "./workflows.ts";

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

test("needs attention is its own route, so the Dashboard never shows the mock's rows beside real data", async () => {
  const calls = stub(200, "[]");
  assert.deepEqual(await getAttention(), []);
  assert.equal(calls[0].url, "/api/v2/attention");
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

// The workflows group: the editor's routes and bodies, and the calls that have no route yet.
test("the workflow calls use the editor's routes and bodies", async () => {
  const flow = { name: "Holidays", description: "", steps: [] };
  let calls = stub(200, JSON.stringify({ flow, rev: 1, fitsGuided: false }));
  await getFlow("7");
  assert.equal(calls[0].url, "/api/v2/workflows/7");
  assert.equal(calls[0].init.method, "GET");

  calls = stub(200, JSON.stringify({ rev: 2 }));
  await putDraft("7", flow as never, 1);
  assert.equal(calls[0].url, "/api/v2/workflows/7/draft");
  assert.equal(calls[0].init.method, "PUT");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { flow, baseRev: 1 });

  calls = stub(200, JSON.stringify({ rev: 2, published: true }));
  assert.deepEqual(await publishFlow("7", flow as never), { rev: 2, published: true });
  assert.equal(calls[0].url, "/api/v2/workflows/7/publish");

  // The check is pure, so the route takes the flow and any id; an unsaved draft checks too.
  calls = stub(200, JSON.stringify({ problems: [], disabled: [], summaries: {} }));
  assert.deepEqual(await validateFlow(flow as never), { problems: [], disabled: [], summaries: {} });
  assert.equal(calls[0].url, "/api/v2/workflows/0/validate");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { flow });

  calls = stub(201, JSON.stringify({ id: "8", rev: 1 }));
  await createDraft({ name: "Holidays", description: "Smaller files", flow: flow as never });
  assert.equal(calls[0].url, "/api/v2/workflows");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { name: "Holidays", description: "Smaller files", flow });

  // The guided page's save carries answers; the server answers a plain not_yet for them.
  calls = stub(400, JSON.stringify({ error: { code: "not_yet", message: "The guided builder is not built yet; use the editor." } }));
  await assert.rejects(createDraft({ name: "Holidays", answers: { smaller: true } }), (e: unknown) => e instanceof ApiError && e.code === "not_yet");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { name: "Holidays", answers: { smaller: true } });
});

test("the list, duplicate and delete use the routes the server has", async () => {
  let calls = stub(200, "[]");
  assert.deepEqual(await listWorkflows(), []);
  assert.equal(calls[0].url, "/api/v2/workflows");

  calls = stub(200, JSON.stringify({ id: "9", name: "Holidays copy" }));
  await duplicateWorkflow("7");
  assert.equal(calls[0].url, "/api/v2/workflows/7/duplicate");
  assert.equal(calls[0].init.method, "POST");

  calls = stub(200, JSON.stringify({ ok: true }));
  assert.deepEqual(await deleteWorkflow("7"), { ok: true });
  assert.equal(calls[0].url, "/api/v2/workflows/7");
  assert.equal(calls[0].init.method, "DELETE");
});

test("the calls with no route answer a plain not-yet the page can show", async () => {
  await assert.rejects(exportWorkflow("7"), (e: unknown) => e instanceof ApiError && e.code === "not_yet");
});
