// Libraries and first run: v1's routes on the real server, and /api/v2/libraries (V5b #135) for the contract's library
// (Pause/Resume, priority, the folder for new files), or the mock module with ?mock=1.
import { call, MOCK, failed, type Answer } from "./api";
import * as mock from "./mock";
import type { FirstFiles, FirstRunSettings, FolderReport, LibraryItem, WorkerV1 } from "./types/v1";
import type { Library, Trash, Workflow } from "./types/v2";
import * as wf from "./workflowApi";

// A workflow a library can use: a never-published one can't be chosen (87faa4b), on the server and the mock.
export interface WorkflowChoice { id: string; name: string; usable: boolean; usedBy: string[] }

const M = <T,>(v: T) => Promise.resolve(v);
const enc = encodeURIComponent;

export async function listLibraries(): Promise<Answer<LibraryItem[]>> {
  if (MOCK) return mock.mockState().libraries.map(mock.libraryItem);
  const r = await call<{ items: LibraryItem[] }>("GET", "/api/libraries");
  return failed(r) ? r : r.items;
}
// V5b (#135): the contract's Library. enabled is Pause/Resume (the same flag as "started"); priority 10/0/-10 is
// High/Normal/Low; overrideFolder null goes back to the workflow's folder.
export type LibraryPatch = { enabled?: boolean; priority?: number; overrideFolder?: string | null };
export async function listLibrariesV2(): Promise<Answer<Library[]>> {
  if (MOCK) return structuredClone(mock.mockState().libraries);
  const r = await call<{ items: Library[] }>("GET", "/api/v2/libraries");
  return failed(r) ? r : r.items;
}
export const getLibraryV2 = (id: string): Promise<Answer<Library>> =>
  MOCK ? M(mock.getLibrary(id)) : call("GET", "/api/v2/libraries/" + enc(id));
export const patchLibraryV2 = (id: string, patch: LibraryPatch): Promise<Answer<Library>> =>
  MOCK ? M(mock.patchLibraryV2(id, patch)) : call("PATCH", "/api/v2/libraries/" + enc(id), patch);

export async function listWorkflows(): Promise<Answer<WorkflowChoice[]>> {
  if (MOCK) { const s = mock.mockState(); return s.wf.map((w) => ({ id: w.id, name: w.name, usable: w.publishedRevisionId != null, usedBy: s.libraries.filter((l) => l.workflowId === w.id).map((l) => l.name) })); }
  // /api/v2/workflows (#109): a workflow's id is the library's flowId; a never-published one can't be used (409).
  const r = await call<{ items: Workflow[] }>("GET", "/api/v2/workflows");
  return failed(r) ? r : r.items.map((w) => ({ id: w.id, name: w.name, usable: w.publishedRevisionId != null, usedBy: w.libraries.map((l) => l.name) }));
}
export const checkFolder = (path: string, outputFolder?: string): Promise<Answer<FolderReport>> =>
  MOCK ? M(mock.checkFolder(path, outputFolder)) : call("POST", "/api/libraries/check", { path, ...(outputFolder && { outputFolder }) }); // outputFolder: #128
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
  if (MOCK) { const s = mock.mockState().settings; return { firstRunDone: s.firstRunDone, firstRunStep: s.firstRunStep, firstRunLibrary: s.firstRunLibrary ?? null, scanEvery: s.scanEvery, scanEverySource: s.scanEverySource }; }
  return call("GET", "/api/v2/settings"); // #111: v1's settings at the v2 path
}
export async function patchSettings(p: Partial<FirstRunSettings>): Promise<Answer<unknown>> {
  if (MOCK) return mock.setSettings(p);
  return call("PATCH", "/api/v2/settings", p);
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

// First run's step 2: a Start Simple workflow that saves new files to `out` (output-first), published, so a library can
// use it: POST /api/v2/workflows from the template, its draft's Save step set to one folder, PUT with If-Match, publish.
// `again` is the one an earlier try on this page made: a retry renames and re-sets it, never makes a second one
// (review 228de8e P2); after a reload, a never-used one of the same name is used again.
export async function startSimpleWorkflow(name: string, out: string, again?: string, libraryPath?: string): Promise<Answer<{ id: string }>> {
  // #128: check the library's folder with this output folder first, so a refused folder never makes or publishes a version
  if (libraryPath) {
    const c = await checkFolder(libraryPath, out);
    if (failed(c)) return c;
    const b = c.blockers.find((x) => x.code === "inside_library") ?? c.blockers[0];
    if (b) return { error: b };
  }
  const list = await wf.listWorkflows();
  if (failed(list)) return list;
  const before = list.find((w) => w.id === again) ?? list.find((w) => w.name === name && w.libraries.length === 0 && w.presetId === "shrink_videos");
  let id = before?.id;
  if (before && before.name !== name) { const r = await wf.patchWorkflow(before.id, { name }); if (failed(r)) return r; }
  if (!id) { const c = await wf.createWorkflow(name); if (failed(c)) return c; id = c.id; }
  const d = await wf.getDraft(id);
  if (failed(d)) return d;
  const doc = structuredClone(d.workflow), commit = doc.nodes.find((n) => n.type === "output.commit");
  if (commit) commit.config = { ...commit.config, policy: "separate_folder", folderMode: "one_folder", folder: out };
  const s = await wf.saveDraft(id, doc, d.layout, d.etag);
  if (failed(s)) return s;
  const problem = s.validation.find((v) => v.severity === "error");
  if (problem) return { error: { code: problem.code, message: problem.message } };
  const p = await wf.publish(id, s.etag);
  return failed(p) ? p : { id };
}

// GET /api/v2/trash (#111): originals kept after being replaced, until their date. The Dashboard's "+120 GB by 18 Nov".
export async function getTrash(): Promise<Answer<Trash>> {
  return MOCK ? mock.mockState().trash : call("GET", "/api/v2/trash");
}
