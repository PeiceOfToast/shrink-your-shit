// Libraries and first run: v1's routes on the real server (there is no /api/v2/libraries yet), or the mock module with
// ?mock=1. Each function answers in v1's shapes either way.
import { call, MOCK, failed, type Answer } from "./api";
import * as mock from "./mock";
import type { FirstFiles, FirstRunSettings, FlowItem, FolderReport, LibraryItem, WorkerV1 } from "./types/v1";

// A workflow a library can use. On the real server today these are v1 flows (all usable); on the mock, V1 workflows,
// where a never-published one can't be chosen (87faa4b).
export interface WorkflowChoice { id: string; name: string; usable: boolean; usedBy: string[] }

const M = <T,>(v: T) => Promise.resolve(v);
const enc = encodeURIComponent;

export async function listLibraries(): Promise<Answer<LibraryItem[]>> {
  if (MOCK) return mock.mockState().libraries.map(mock.libraryItem);
  const r = await call<{ items: LibraryItem[] }>("GET", "/api/libraries");
  return failed(r) ? r : r.items;
}
export async function listWorkflows(): Promise<Answer<WorkflowChoice[]>> {
  if (MOCK) { const s = mock.mockState(); return s.wf.map((w) => ({ id: w.id, name: w.name, usable: w.publishedRevisionId != null, usedBy: s.libraries.filter((l) => l.workflowId === w.id).map((l) => l.name) })); }
  const r = await call<{ items: FlowItem[] }>("GET", "/api/flows");
  return failed(r) ? r : r.items.map((f) => ({ id: f.id, name: f.name, usable: true, usedBy: f.usedBy }));
}
export const checkFolder = (path: string): Promise<Answer<FolderReport>> => (MOCK ? M(mock.checkFolder(path)) : call("POST", "/api/libraries/check", { path }));
export const firstFiles = (path: string): Promise<Answer<FirstFiles>> => (MOCK ? M(mock.firstFiles(path)) : call("POST", "/api/libraries/first-files", { path }));
export const addLibrary = (name: string, path: string, flowId: string): Promise<Answer<LibraryItem>> =>
  MOCK ? M(mock.addLibrary(name, path, flowId)) : call("POST", "/api/libraries", { name, path, flowId });
export const patchLibrary = (id: string, patch: { name?: string; path?: string }): Promise<Answer<LibraryItem>> =>
  MOCK ? M(mock.patchLibrary(id, patch)) : call("PATCH", "/api/libraries/" + enc(id), patch);
export const setLibraryWorkflow = (id: string, flowId: string): Promise<Answer<LibraryItem>> =>
  MOCK ? M(mock.setLibraryWorkflow(id, flowId)) : call("PUT", "/api/libraries/" + enc(id) + "/flow", { flowId });
export async function scanLibrary(id: string): Promise<Answer<unknown>> {
  if (MOCK) return (mock.scanLibrary(id), {});
  return call("POST", "/api/libraries/" + enc(id) + "/scan");
}

export async function getSettings(): Promise<Answer<FirstRunSettings>> {
  if (MOCK) { const s = mock.mockState().settings; return { firstRunDone: s.firstRunDone, firstRunStep: s.firstRunStep, firstRunLibrary: s.firstRunLibrary ?? null }; }
  return call("GET", "/api/settings");
}
export async function patchSettings(p: Partial<FirstRunSettings>): Promise<Answer<unknown>> {
  if (MOCK) return (mock.setSettings(p), {});
  return call("PATCH", "/api/settings", p);
}

export async function listWorkers(): Promise<Answer<WorkerV1[]>> {
  if (MOCK) return mock.mockState().workers.map((w) => ({ id: w.id, name: w.name, host: w.host, state: w.status, filesAtOnce: w.slots.cpu.total,
    gpu: { found: w.gpus.length > 0, vendor: w.gpus[0]?.vendor ?? null }, converting: w.currentJobIds, quietUntil: null }));
  const r = await call<{ workers: WorkerV1[] }>("GET", "/api/workers");
  return failed(r) ? r : r.workers;
}
export async function startWorker(id: string): Promise<Answer<unknown>> {
  if (MOCK) return (mock.startAll(), {});
  return call("POST", "/api/workers/" + enc(id) + "/start");
}

// First run's step 2: a Start Simple workflow that saves new files to `out` (output-first). On the real server today
// that is a v1 Standard flow (only Standard can save to another folder) with afterConverting "folder"; a flow of that
// name no library uses yet (from before a reload) is used again. `again` is the one an earlier try on this page made: a
// retry after a refused library renames and re-sets it, never makes a second one (review 228de8e P2). On the mock: a
// V1 workflow from the preset, published.
const SELECTORS = ["quality", "speed", "format", "resolution", "maxBitrate", "audio", "onlyMyLanguages", "subtitles", "fileType", "ignore", "includeDolbyVision"];
export async function startSimpleWorkflow(name: string, out: string, again?: string): Promise<Answer<{ id: string }>> {
  if (MOCK) {
    const s = mock.mockState();
    const before = s.wf.find((x) => x.id === again);
    if (before && before.name !== name) mock.setWorkflow(before.id, { name });
    const w = before ?? s.wf.find((x) => x.presetId === "shrink_videos" && x.publishedRevisionId == null && x.name === name) ?? mock.createWorkflow(name, "");
    const d = mock.getDraft(w.id)!, doc = structuredClone(d.workflow);
    const commit = doc.nodes.find((n) => n.type === "output.commit")!;
    commit.config = { ...commit.config, policy: "separate_folder", folderMode: "one_folder", folder: out };
    const r = mock.saveDraft(w.id, doc, d.etag);
    if (r.error) return { error: { code: "draft", message: r.error } };
    const problem = r.draft!.validation.find((v) => v.severity === "error");
    if (problem) return { error: { code: problem.code, message: problem.message } };
    if (mock.publish(w.id, r.draft!.etag).error) return { error: { code: "changed", message: "Something changed while saving. Try again." } };
    return { id: w.id };
  }
  const flows = await call<{ items: FlowItem[] }>("GET", "/api/flows");
  if (failed(flows)) return flows;
  let id = flows.items.some((f) => f.id === again) ? again : flows.items.find((f) => f.name === name && f.usedBy.length === 0)?.id;
  if (id && flows.items.find((f) => f.id === id)!.name !== name) {
    const r = await call("PATCH", "/api/flows/" + enc(id), { name });
    if (failed(r)) return r;
  }
  if (!id) {
    const f = await call<FlowItem>("POST", "/api/flows", { name, kind: "standard" });
    if (failed(f)) return f;
    id = f.id;
  }
  const cur = await call<Record<string, unknown>>("GET", "/api/flows/" + enc(id) + "/setting");
  if (failed(cur)) return cur;
  const body: Record<string, unknown> = Object.fromEntries(SELECTORS.filter((k) => k in cur).map((k) => [k, cur[k]]));
  const put = await call("PUT", "/api/flows/" + enc(id) + "/setting", { ...body, afterConverting: "folder", outputDir: out });
  return failed(put) ? put : { id };
}
