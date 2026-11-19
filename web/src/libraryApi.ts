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
// outputFolder: #128. next (#144): is there a usable folder beside it for "Next to each library", before the library
// exists? If not, a no_default_folder blocker with folder (the parent) and reason (not_writable | different_disk |
// container_root).
export const checkFolder = (path: string, outputFolder?: string, next?: { name: string }): Promise<Answer<FolderReport>> =>
  MOCK ? M(mock.checkFolder(path, outputFolder, !!next)) : call("POST", "/api/libraries/check", { path, ...(outputFolder && { outputFolder }), ...(next && { nextToLibrary: true, name: next.name }) });
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
  if (MOCK) { const s = mock.mockState().settings; return { firstRunDone: s.firstRunDone, firstRunStep: s.firstRunStep, firstRunLibrary: s.firstRunLibrary ?? null, scanEvery: s.scanEvery, scanEverySource: s.scanEverySource, scanEveryMin: 15, scanEveryMax: 86400, languages: s.languages ?? ["eng"], keepOriginalsDays: s.keepOriginalsDays ?? 7, logKeepDays: s.logKeepDays ?? 30, trashPath: "Kept next to each file, in a folder called .syp-trash", authRequired: true, checkForUpdates: !!s.checkForUpdates }; }
  return call("GET", "/api/v2/settings"); // #111: v1's settings at the v2 path
}
// #324 update notice: GET is the last check, kept (asks GitHub for nothing); POST check asks now (409 updates_off when off).
export interface UpdateNotice { enabled: boolean; current: string; latest: string | null; available: boolean; notes: string; url: string | null;
  publishedAt: string | null; checkedAt: string | null; error: string | null; installType: "script" | "windows" | "docker" | "source";
  // #331 (contract ## Update): how this install updates, its install state, and the last finished update or rollback.
  method: "helper" | "commands" | "none"; commands: string[] | null;
  // #336: the server's OS, the helper's install line (script on Linux without the helper), and what Roll back can do.
  os: "linux" | "darwin" | "windows"; helperCommand: string | null;
  rollback: { available: boolean; version: string | null; dataRestored: boolean };
  install: { state: "idle" | "waiting" | "downloading" | "installing" | "failed"; error: string | null };
  lastResult: { ok: boolean; from: string; to: string; message: string; dataRestored: boolean } | null }
let mockChecked = Date.now() - 36e5; // the sample's last check, kept like the server's
let mockInstall: UpdateNotice["install"]["state"] | null = null; // set by a mock Update click
let mockRolled = false; // set by a mock Roll back click: the sample then runs the previous version
// ?upd=can|jobs|waiting|downloading|installing|failed|done|rolledback|docker|commands|macos|windows|source picks the update block.
const upd = () => new URLSearchParams(location.search).get("upd") ?? "";
const mockUpdate = (): UpdateNotice => { const on = !!mock.mockState().settings.checkForUpdates, q = new URLSearchParams(location.search), u = upd();
  const up = q.has("update") || (!!u && u !== "done"), it = u === "docker" ? "docker" : u === "windows" ? "windows" : u === "source" ? "source" : "script";
  const st = mockInstall ?? (["waiting", "downloading", "installing", "failed"].includes(u) ? u as UpdateNotice["install"]["state"] : "idle");
  const cmd = "curl -fsSL https://raw.githubusercontent.com/PeiceOfToast/shrink-your-shit/main/install.sh | sh", helper = it === "script" && !["docker", "commands", "macos"].includes(u);
  const d: UpdateNotice = { enabled: on, current: "2.0.0", latest: on ? (up ? "2.1.0" : "2.0.0") : null, available: on && up, notes: "", url: on ? "https://github.com/" : null,
    publishedAt: null, checkedAt: on ? new Date(mockChecked).toISOString() : null, error: on && q.has("updateerr") ? "Get \"https://api.github.com/...\": dial tcp: i/o timeout" : null, installType: it,
    os: u === "macos" ? "darwin" : it === "windows" ? "windows" : "linux",
    method: helper ? "helper" : it === "windows" || it === "source" ? "none" : "commands",
    commands: u === "docker" ? ["docker compose pull", "docker compose up -d"] : u === "commands" || u === "macos" ? [cmd] : null,
    helperCommand: u === "commands" ? cmd + " -s -- --update-helper" : null,
    // after an update the helper keeps the old version and a rollback puts the database back; after a failed update it still
    // keeps one (#336) and the database stays. ?rb adds a kept version to any helper state.
    rollback: helper && (u === "done" || u === "rolledback" || q.has("rb")) ? { available: true, version: "1.9.0", dataRestored: u === "done" } : { available: false, version: null, dataRestored: false },
    install: { state: st, error: st === "failed" ? "checksum mismatch for shrink-your-shit_2.1.0_linux_amd64.tar.gz" : null },
    lastResult: u === "done" ? { ok: true, from: "1.9.0", to: "2.0.0", message: "Updated from v1.9.0 to v2.0.0", dataRestored: false }
      : u === "rolledback" ? { ok: false, from: "2.0.0", to: "2.1.0", message: "Update to v2.1.0 failed; rolled back to v2.0.0", dataRestored: false } : null };
  // after a mock Roll back the sample runs 1.9.0, with nothing older kept
  return !mockRolled ? d : { ...d, current: "1.9.0", latest: on ? "2.0.0" : null, available: on, install: { state: "idle", error: null },
    rollback: { available: false, version: null, dataRestored: false },
    lastResult: { ok: true, from: "2.0.0", to: "1.9.0", message: "Rolled back from 2.0.0 to 1.9.0" + (u === "done" ? "; changes made since the update were undone (kept in syp.db.rolled-back)" : ""), dataRestored: u === "done" } }; };
export const getUpdate = (): Promise<Answer<UpdateNotice>> => (!MOCK ? call("GET", "/api/v2/update")
  : new URLSearchParams(location.search).has("updatefail") ? M({ error: { code: "internal", message: "Couldn't load the update status." } }) : M(mockUpdate()));
export const checkUpdate = (): Promise<Answer<UpdateNotice>> => (MOCK ? M((mockChecked = Date.now(), mockUpdate())) : call("POST", "/api/v2/update/check"));
// POST install: 202 with the status body; 409 job_running / not_available / update_in_progress / updates_off / install_unsupported.
export const installUpdate = (when: "now" | "after-job"): Promise<Answer<UpdateNotice>> => {
  if (!MOCK) return call("POST", "/api/v2/update/install", { when });
  const refuse = new URLSearchParams(location.search).get("updrefuse");
  if (refuse) return M({ error: { code: refuse, message: refuse } });
  if (when === "now" && upd() === "jobs") return M({ error: { code: "job_running", message: "a job is running; use after-job" } });
  return M((mockInstall = when === "now" ? "downloading" : "waiting", mockUpdate()));
};
// POST rollback: 202 with the status body; 409 install_unsupported / update_in_progress / no_previous, 500 server_error.
export const rollbackUpdate = (): Promise<Answer<UpdateNotice>> => {
  if (!MOCK) return call("POST", "/api/v2/update/rollback", {});
  const refuse = new URLSearchParams(location.search).get("rbrefuse");
  if (refuse) return M({ error: { code: refuse, message: refuse === "server_error" ? "could not reach the update helper" : refuse === "no_previous" ? "there is no previous version to roll back to" : refuse } });
  if (new URLSearchParams(location.search).has("rbstall")) return M(mockUpdate());
  return M((mockRolled = true, mockUpdate()));
};
// How many files are converting now (the summary's activity), so the button can wait for them.
export const getConverting = async (): Promise<Answer<number>> => {
  if (MOCK) return upd() === "jobs" ? 2 : 0;
  const r = await call<{ activity: { converting: number } }>("GET", "/api/v2/summary");
  return failed(r) ? r : r.activity.converting;
};
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

// The server's folder beside a library with "Next to each library" (V5a, jobs.NextToLibrary): "<library folder> (shrunk)".
export const shrunk = (path: string) => path.replace(/\/+$/, "") + " (shrunk)";

// First run's step 2: a Start Simple workflow that saves new files in a separate folder (output-first), published, so a
// library can use it: POST /api/v2/workflows from the template, its draft's Save step set, PUT with If-Match, publish.
// `out` null is "Next to each library" (V5a #133: each library's new files go to "<its folder> (shrunk)"); a path is
// "One folder for all".
// `again` is the one an earlier try on this page made: a retry renames and re-sets it, never makes a second one
// (review 228de8e P2); after a reload, a never-used one of the same name is used again.
export async function startSimpleWorkflow(name: string, out: string | null, again?: string, libraryPath?: string): Promise<Answer<{ id: string }>> {
  // #128: check the library's folder with this output folder first, so a refused folder never makes or publishes a version
  if (libraryPath) {
    const c = await checkFolder(libraryPath, out ?? undefined); // a folder beside the library is never inside it
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
  if (commit) commit.config = out === null ? { ...commit.config, policy: "separate_folder", folderMode: "next_to_library", folder: undefined }
    : { ...commit.config, policy: "separate_folder", folderMode: "one_folder", folder: out };
  const s = await wf.saveDraft(id, doc, d.layout, d.etag);
  if (failed(s)) return s;
  const problem = s.validation.find((v) => v.severity === "error");
  if (problem) return { error: { code: problem.code, message: problem.message } };
  const p = await wf.publish(id, s.etag);
  return failed(p) ? p : { id };
}

// GET /api/v2/trash (#111): originals kept after being replaced, until their date. The Dashboard's "+120 GB by 18 Nov".
// Settings > General: the access token, shown only when asked for (never stored by the page), a new one, and signing out.
export const getToken = (): Promise<Answer<{ token: string }>> => (MOCK ? M({ token: "sample-token-not-real" }) : call("GET", "/api/settings/token"));
export const rotateToken = (): Promise<Answer<{ token: string }>> => (MOCK ? M({ token: "sample-token-" + Date.now().toString(36) }) : call("POST", "/api/settings/token/rotate"));
export const signOut = (): Promise<Answer<unknown>> => (MOCK ? M({}) : call("POST", "/api/logout"));

// Settings > About: GET /api/system/health (the old Settings > System): checks, version, the last log lines.
export interface Health { checks: { id: string; label: string; status: "ok" | "warn" | "bad"; detail: string }[]; version: string; logsTail: string[] }
export const getHealth = (): Promise<Answer<Health>> => (MOCK ? M({ version: "2.0.0 (sample)", logsTail: ["12:00:01 scanned 3 libraries: no new files"],
  checks: [{ id: "ffmpeg", label: "FFmpeg is installed", status: "ok", detail: "" }, { id: "disk", label: "Space on /media", status: "warn", detail: "120 GB free" }] }) : call("GET", "/api/system/health"));

export async function getTrash(): Promise<Answer<Trash>> {
  return MOCK ? mock.mockState().trash : call("GET", "/api/v2/trash");
}

// G1 (one UI): remove a library: DELETE /api/libraries/{id}. The server stops watching the folder, takes its waiting
// files off the queue and stops the one converting; nothing on disk changes. 409 busy comes in its own words.
export async function removeLibrary(id: string): Promise<Answer<unknown>> {
  if (MOCK) return { error: { code: "not_available", message: "The sample install can't remove a library." } };
  return call("DELETE", "/api/libraries/" + enc(id));
}
