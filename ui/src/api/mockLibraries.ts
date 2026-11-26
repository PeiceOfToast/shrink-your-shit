// Libraries, workflows and folders for the mock. Writes change an in-memory copy, so a session behaves like the app.
import type { FolderCheck, FolderListing, Library, LibraryDetail, LibraryInput, LibraryPatch, Preview, Watch, Workflow, WorkflowSummary } from "./types.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, respond } from "./mockState.ts";
import { FOLDERS, LIBRARIES, PREVIEW, SHARE_FOLDERS, WORKFLOW_SUMMARY, WORKFLOWS } from "./fixtures/libraries.ts";

let rows: Library[] = LIBRARIES.map((l) => ({ ...l }));
let seq = 0;

const norm = (p: string) => (p.length > 1 ? p.replace(/\/+$/, "") : p);
const WATCHING: Watch[] = ["hourly", "daily", "weekly"];

// The folder rules from page 09: inside another library, holding one, overlapping, or differing only in case.
function conflict(path: string, exceptId?: string): ApiError | null {
  const p = norm(path);
  for (const l of rows) {
    if (l.id === exceptId) continue;
    const q = norm(l.path);
    if (p === q) return new ApiError("overlaps", `${l.name} already watches this folder.`, "path");
    if (p.toLowerCase() === q.toLowerCase()) return new ApiError("case_twin", `${l.name} watches this folder under a different letter case.`, "path");
    if (p.startsWith(q + "/")) return new ApiError("inside_library", `This folder is inside ${l.name}, which is already a library.`, "path");
    if (q.startsWith(p + "/")) return new ApiError("holds_library", `This folder holds ${l.name}, which is already a library.`, "path");
  }
  return null;
}

function findRow(id: string): Library {
  const l = rows.find((r) => r.id === id);
  if (!l) throw new ApiError("library_not_found", "That library doesn't exist.");
  return l;
}

function checkWatching(v: unknown): asserts v is Watch {
  if (!WATCHING.includes(v as Watch)) throw new ApiError("invalid_setting", "Pick hourly, daily or weekly.", "watch");
}

// The library table and the overview read the same rows.
export function currentLibraries(): Library[] {
  return rows.map((l) => ({ ...l }));
}

export function getLibraries(): Promise<Library[]> {
  return respond(() => {
    if (getForcedState() === "empty") return [];
    if (getForcedState() === "running") return rows.map((l) => (l.id === "movies" ? { ...l, status: "scanning" as const, scanPct: 62 } : { ...l }));
    return currentLibraries();
  });
}

export function getLibrary(id: string): Promise<LibraryDetail> {
  return respond(() => ({ ...findRow(id), workers: [{ id: "this-server", name: "this server" }] }));
}

export function createLibrary(input: LibraryInput): Promise<Library> {
  return respond(() => {
    if (!input.name.trim()) throw new ApiError("name_required", "Give the library a name.", "name");
    if (!input.path && !input.shareId) throw new ApiError("path_required", "Pick the folder with the videos.", "path");
    if (input.watch !== undefined) checkWatching(input.watch);
    if (input.path && !FOLDERS[norm(input.path)]) throw new ApiError("folder_not_found", "That folder isn't on this server.", "path");
    const hit = conflict(input.path ?? input.shareId ?? "");
    if (hit) throw hit;
    seq += 1;
    const id = `${input.name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}-${seq}`;
    const videos = input.path ? (FOLDERS[norm(input.path)]?.videos ?? 0) : 0;
    const lib: Library = {
      id,
      name: input.name.trim(),
      path: input.path ?? input.shareId ?? "",
      shareId: input.shareId,
      workflow: null,
      files: videos,
      waiting: 0,
      savedBytes: 0,
      savedPct: 0,
      lastScan: { at: "just now", found: videos, new: videos, changed: 0 },
      status: "needs_workflow",
      watch: input.watch ?? "daily",
      paused: false,
      nextScanAt: null,
    };
    rows = [...rows, lib];
    return { ...lib };
  });
}

export function patchLibrary(id: string, patch: LibraryPatch): Promise<LibraryDetail> {
  return respond(() => {
    const cur = findRow(id);
    let next: Library = { ...cur };
    if (patch.path !== undefined && norm(patch.path) !== norm(cur.path)) {
      const hit = conflict(patch.path, id);
      if (hit) throw hit;
      next.path = patch.path;
    }
    if (patch.name !== undefined) next.name = patch.name.trim() || cur.name;
    if (patch.watch !== undefined) {
      checkWatching(patch.watch);
      next.watch = patch.watch;
    }
    if (patch.workflowId !== undefined) {
      if (patch.workflowId === null) {
        next.workflow = null;
        next.status = "needs_workflow";
      } else {
        const wf = WORKFLOWS.find((w) => w.id === patch.workflowId);
        if (!wf) throw new ApiError("workflow_not_found", "That workflow doesn't exist.", "workflowId");
        next.workflow = { id: wf.id, name: wf.name };
        if (cur.status === "needs_workflow") {
          // Setting a workflow on a counted library starts queueing its files at once.
          next.status = "watching";
          next.waiting = cur.files;
        }
      }
    }
    if (patch.paused !== undefined) {
      next.paused = patch.paused;
      if (patch.paused && next.status === "watching") next.status = "paused";
      if (!patch.paused && next.status === "paused") next.status = "watching";
    }
    // nextScanAt is null while paused, and follows the chosen interval otherwise.
    next.nextScanAt = next.paused ? null : nextScanFor(next.watch);
    rows = rows.map((r) => (r.id === id ? next : r));
    return { ...next, workers: [{ id: "this-server", name: "this server" }] };
  });
}

function nextScanFor(w: Watch): string {
  const hours = w === "hourly" ? 1 : w === "daily" ? 24 : 168;
  return new Date(Date.now() + hours * 3600_000).toISOString();
}

export function deleteLibrary(id: string): Promise<{ ok: true }> {
  return respond(() => {
    findRow(id);
    rows = rows.filter((r) => r.id !== id);
    return { ok: true as const };
  });
}

export function scanLibrary(id: string): Promise<Library> {
  return respond(() => {
    const cur = findRow(id);
    const next: Library = cur.status === "needs_workflow" || cur.status === "share_down" ? cur : { ...cur, status: "scanning", scanPct: 0 };
    rows = rows.map((r) => (r.id === id ? next : r));
    return { ...next };
  });
}

export function reconnectShare(id: string): Promise<Library> {
  return respond(() => {
    const cur = findRow(id);
    const next: Library = { ...cur, status: "watching", shareDownAt: undefined };
    rows = rows.map((r) => (r.id === id ? next : r));
    return { ...next };
  });
}

// Check this folder: counts the videos and reports what Add would refuse. Saves nothing.
export function checkFolder(target: { path?: string; shareId?: string }): Promise<FolderCheck> {
  return respond(() => {
    if (!target.path && !target.shareId) throw new ApiError("path_required", "Pick the folder with the videos.", "path");
    if (target.shareId) return { videos: 1108, sizeBytes: 2.4e12, problems: [] };
    const info = FOLDERS[norm(target.path!)];
    if (!info) throw new ApiError("folder_not_found", "That folder isn't on this server.", "path");
    const hit = conflict(target.path!);
    return {
      videos: info.videos ?? 0,
      sizeBytes: info.sizeBytes ?? 0,
      problems: hit ? [{ code: hit.code, message: hit.message }] : [],
    };
  });
}

export function getFolders(path: string): Promise<FolderListing> {
  return respond(() => {
    const p = norm(path || "/media");
    const info = FOLDERS[p];
    if (!info) throw new ApiError("folder_not_found", "That folder isn't on this server.", "path");
    return {
      path: p,
      parent: info.parent,
      folders: info.children.map((c) => ({ name: FOLDERS[c].name, path: c })),
    };
  });
}

export function testShare(input: { address: string; user?: string; password?: string }): Promise<{ ok: true; folders: string[] }> {
  return respond(() => {
    if (!input.address.trim()) throw new ApiError("address_required", "Type the share's address.", "address");
    return { ok: true as const, folders: SHARE_FOLDERS };
  });
}

export function createShare(input: { address: string; user?: string; password?: string }): Promise<{ id: string; address: string }> {
  return respond(() => ({ id: `share-${input.address.replace(/[^a-z0-9]+/gi, "-").toLowerCase()}`, address: input.address }));
}

export function getWorkflows(): Promise<Workflow[]> {
  return respond(() => WORKFLOWS.map((w) => ({ ...w, usedBy: [...w.usedBy] })));
}

export function getWorkflow(id: string): Promise<WorkflowSummary> {
  return respond(() => {
    const wf = WORKFLOWS.find((w) => w.id === id);
    if (!wf) throw new ApiError("workflow_not_found", "That workflow doesn't exist.");
    return { id: wf.id, name: wf.name, summary: WORKFLOW_SUMMARY[id] };
  });
}

export function getPreview(libraryId: string, workflowId: string): Promise<Preview> {
  return respond(() => {
    findRow(libraryId);
    const p = PREVIEW[workflowId];
    if (!p) throw new ApiError("workflow_not_found", "That workflow doesn't exist.", "workflowId");
    return p;
  });
}
