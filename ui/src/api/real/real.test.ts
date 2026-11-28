// The real client's request paths and bodies, with a stubbed fetch (no server). Every call carries X-SYP: 1 and same-origin.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { request } from "../http.ts";
import { ApiError } from "../errors.ts";
import { convertAgain, getJobNeighbours, getJobs, runJobsAction } from "./jobs.ts";
import { getLibraries, createLibrary, patchLibrary, deleteLibrary } from "./libraries.ts";
import { getSettings, patchSettings, getDoctor, runHealthFix } from "./settings.ts";
import { getTrash, restoreTrash } from "./trash.ts";
import { getAttention, getOverview, runAction } from "./overview.ts";
import { createDraft, deleteWorkflow, duplicateWorkflow, exportWorkflow, getFlow, getGuideAnswers, guidePreview, listWorkflows, publishFlow, putAnswers, putDraft, tryFlow, validateFlow } from "./workflows.ts";

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

test("job actions post {action, ids}, and run again posts to the job's own route with no body", async () => {
  const calls = stub(200, JSON.stringify({ done: ["j-1"], refused: [] }));
  await runJobsAction("retry", ["j-1"]);
  assert.equal(calls[0].url, "/api/v2/jobs/actions");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { action: "retry", ids: ["j-1"] });
  const again = stub(200, "{}");
  await convertAgain("j 1");
  assert.equal(again[0].url, "/api/v2/jobs/j%201/convert-again");
  assert.equal(again[0].init.method, "POST");
  assert.equal(again[0].init.body, undefined);
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

// The workflows group: the editor's routes and bodies, the guided builder's, and the export that has no route yet.
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

  // The guided page's create carries answers and no flow.
  calls = stub(201, JSON.stringify({ id: "9", rev: 1 }));
  assert.deepEqual(await createDraft({ name: "Holidays", answers: { smaller: true } }), { id: "9", rev: 1 });
  assert.equal(calls[0].url, "/api/v2/workflows");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { name: "Holidays", answers: { smaller: true } });
});

test("the guided builder's answers and preview use the routes the server has", async () => {
  const flow = { name: "Holidays", description: "", steps: [] };
  let calls = stub(200, JSON.stringify({ answers: { quality: 3 }, fitsGuided: true }));
  assert.deepEqual(await getGuideAnswers("new"), { answers: { quality: 3 }, fitsGuided: true });
  assert.equal(calls[0].url, "/api/v2/workflows/new/answers");
  assert.equal(calls[0].init.method, "GET");

  calls = stub(200, JSON.stringify({ answers: { quality: 4 }, fitsGuided: false }));
  await getGuideAnswers("7");
  assert.equal(calls[0].url, "/api/v2/workflows/7/answers");

  calls = stub(200, JSON.stringify({ flow, rev: 2 }));
  assert.deepEqual(await putAnswers("7", { quality: 4 }, 1), { flow, rev: 2 });
  assert.equal(calls[0].url, "/api/v2/workflows/7/answers");
  assert.equal(calls[0].init.method, "PUT");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { answers: { quality: 4 }, baseRev: 1 });

  // The preview is pure and saves nothing, so any workflow may ask it.
  calls = stub(200, JSON.stringify({ sentences: ["Converts video."], flow, problems: [], disabled: [] }));
  assert.deepEqual(await guidePreview({ quality: 4 }), { sentences: ["Converts video."], flow, problems: [], disabled: [] });
  assert.equal(calls[0].url, "/api/v2/workflows/preview");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { answers: { quality: 4 } });

  // Try on one file sends the page's own flow and the file's full path, and answers the lines as they came.
  const lines = [{ stepId: "video", text: "Video: about 3 s." }, { stepId: "save", text: "Size: 1.9 GB (now 4.6 GB)." }];
  calls = stub(200, JSON.stringify({ lines }));
  const stop = new AbortController();
  assert.deepEqual(await tryFlow(flow as never, "/videos/holiday.mkv", stop.signal), { lines });
  assert.equal(calls[0].url, "/api/v2/workflows/try");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { flow, file: "/videos/holiday.mkv" });
  // The page's Stop is the request's signal: the fetch is the one a Stop can abort.
  assert.equal(calls[0].init.signal, stop.signal);
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

test("settings: GET the whole document, PATCH only the changed fields nested by group, a refusal keeps its field", async () => {
  let calls = stub(200, JSON.stringify({ general: { languages: [] } }));
  await getSettings();
  assert.equal(calls[0].url, "/api/v2/settings");
  assert.equal(calls[0].init.method, "GET");
  calls = stub(200, JSON.stringify({ general: { languages: ["eng"] } }));
  await patchSettings({ general: { languages: ["eng"] } });
  assert.equal(calls[0].url, "/api/v2/settings");
  assert.equal(calls[0].init.method, "PATCH");
  assert.deepEqual(JSON.parse(calls[0].init.body as string), { general: { languages: ["eng"] } });
  stub(400, JSON.stringify({ error: { code: "invalid_setting", field: "general.languages", message: "Use three-letter codes like eng, ger, fra." } }));
  await assert.rejects(patchSettings({ general: { languages: ["english"] } }), (e: unknown) =>
    e instanceof ApiError && e.code === "invalid_setting" && e.field === "general.languages");
});

test("trash: GET the list, POST a restore by id, a refusal keeps its code", async () => {
  let calls = stub(200, JSON.stringify([{ id: "7", file: "a.mkv", library: "Films", trashedAt: "2026-11-27T10:00:00Z", daysLeft: 6 }]));
  const items = await getTrash();
  assert.equal(calls[0].url, "/api/v2/trash");
  assert.equal(calls[0].init.method, "GET");
  assert.equal(items[0].daysLeft, 6);
  calls = stub(200, JSON.stringify({ ok: true }));
  await restoreTrash("7");
  assert.equal(calls[0].url, "/api/v2/trash/7/restore");
  assert.equal(calls[0].init.method, "POST");
  stub(409, JSON.stringify({ error: { code: "original_gone", message: "The original isn't in the trash folder any more." } }));
  await assert.rejects(restoreTrash("7"), (e: unknown) => e instanceof ApiError && e.code === "original_gone");
});

test("saving a library sends PATCH with the changes and returns the library", async () => {
  const calls = stub(200, JSON.stringify({ id: "2", name: "Films", watch: "daily" }));
  const out = await patchLibrary("2", { name: "Films", watch: "daily" });
  assert.equal(calls[0].url, "/api/v2/libraries/2");
  assert.equal(calls[0].init.method, "PATCH");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { name: "Films", watch: "daily" });
  assert.equal(out.name, "Films");
});

test("a folder the server refuses shows its sentence on the path field", async () => {
  stub(400, JSON.stringify({ error: { code: "invalid_request", message: "Another library already watches this folder. Choose a different folder.", field: "path" } }));
  await assert.rejects(patchLibrary("2", { path: "/media/movies" }), (e: unknown) =>
    e instanceof ApiError && e.code === "invalid_request" && e.field === "path" && e.message === "Another library already watches this folder. Choose a different folder.");
});

test("removing a library sends DELETE and returns ok", async () => {
  const calls = stub(200, '{"ok":true}');
  assert.deepEqual(await deleteLibrary("2"), { ok: true });
  assert.equal(calls[0].url, "/api/v2/libraries/2");
  assert.equal(calls[0].init.method, "DELETE");
});

test("a Needs attention action sends its own method, path and body once, under /api/v2", async () => {
  const calls = stub(200, '{"line":"Retry started."}');
  const out = await runAction("POST", "/api/v2/jobs/actions", { action: "retry", ids: ["j1", "j2"] });
  assert.equal(calls[0].url, "/api/v2/jobs/actions");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), { action: "retry", ids: ["j1", "j2"] });
  assert.deepEqual(out, { line: "Retry started." });
});

test("an action with no line in its reply shows nothing", async () => {
  stub(200, "null");
  assert.deepEqual(await runAction("POST", "/api/v2/jobs/actions", { action: "retry", ids: [] }), {});
});

test("an action path outside /api/v2 is refused, and nothing is sent", async () => {
  const calls = stub(200, "{}");
  await assert.rejects(runAction("POST", "/jobs/actions", {}), (e: unknown) => e instanceof ApiError && e.code === "invalid_request");
  assert.equal(calls.length, 0);
});

test("a Needs attention action where every item is refused shows the refusal, not done", async () => {
  stub(200, JSON.stringify({ done: [], refused: [{ id: "j1", code: "job_busy", message: "Still running." }] }));
  await assert.rejects(runAction("POST", "/api/v2/jobs/actions", { action: "retry", ids: ["j1"] }), (e: unknown) =>
    e instanceof ApiError && e.code === "job_busy" && e.message === "Still running.");
});

// Settings > Health against the server: the checks, and a check's fix. The encoder test's fix waits for its run on the
// worker, then reads the checks again (retest), so the page shows the result, not the run's answer.
const doctorBody = () => JSON.parse(readFileSync(new URL("../../../../internal/api/testdata/contract/doctor.json", import.meta.url), "utf8")).body;

// A fetch that answers each request from its own queue, and records the calls in order.
function route(map: Record<string, { status: number; body: string }[]>): { url: string; method: string }[] {
  const calls: { url: string; method: string }[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit) => {
    const key = `${init.method ?? "GET"} ${url}`;
    calls.push({ url, method: init.method ?? "GET" });
    const next = map[key]?.shift();
    if (!next) throw new Error(`no answer queued for ${key}`);
    return new Response(next.body, { status: next.status, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;
  return calls;
}

test("the checks are read from /doctor, and run again with retest=1", async () => {
  const calls = route({ "GET /api/v2/doctor": [{ status: 200, body: JSON.stringify(doctorBody()) }], "GET /api/v2/doctor?retest=1": [{ status: 200, body: JSON.stringify(doctorBody()) }] });
  assert.deepEqual(await getDoctor(), doctorBody());
  assert.equal(calls[0].url, "/api/v2/doctor");
  assert.deepEqual(await getDoctor(true), doctorBody());
  assert.equal(calls[1].url, "/api/v2/doctor?retest=1");
});

test("the encoder fix waits for its run on the worker, then answers the checks again", async () => {
  const calls = route({
    "POST /api/v2/workers/local/capabilities/test": [{ status: 200, body: '{"runId":"r1"}' }],
    "GET /api/v2/workers/local": [{ status: 200, body: '{"id":"local","test":{"runId":"r1","running":false,"finishedAt":"2026-11-28T07:30:00Z"}}' }],
    "GET /api/v2/doctor?retest=1": [{ status: 200, body: JSON.stringify(doctorBody()) }],
  });
  assert.deepEqual(await runHealthFix("/api/v2/workers/local/capabilities/test"), doctorBody());
  assert.deepEqual(calls.map((c) => `${c.method} ${c.url}`), [
    "POST /api/v2/workers/local/capabilities/test",
    "GET /api/v2/workers/local",
    "GET /api/v2/doctor?retest=1",
  ]);
});

test("a fix the server refuses shows the server's message, and nothing else is read", async () => {
  const calls = route({
    "POST /api/v2/workers/local/capabilities/test": [{ status: 409, body: JSON.stringify({ error: { code: "worker_busy", message: "A worker is starting. Try again in a moment." } }) }],
  });
  await assert.rejects(runHealthFix("/api/v2/workers/local/capabilities/test"), (e: unknown) =>
    e instanceof ApiError && e.code === "worker_busy" && e.message === "A worker is starting. Try again in a moment.");
  assert.equal(calls.length, 1);
});
