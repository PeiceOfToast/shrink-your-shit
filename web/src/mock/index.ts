// The one mock module (NEXT-ui-v2.md "Contracts"): in-memory state in the contract's types, and the actions the
// screens offer, really applied to that state. Every screen using it shows the Prototype banner. ?mock=fresh shows a
// new install (no libraries yet) for review. When a contract endpoint lands, its function here becomes the fetch.
import { useSyncExternalStore } from "react";
import type { ActivityEvent, ID, Job, JobState, Library, PublishPreview, Settings, Summary, Template, Trash, Worker, Workflow, WorkflowDoc, WorkflowRevision } from "../types/v2";
import type { FirstFiles, FolderReport, Issue, LibraryItem } from "../types/v1";
import { branchedDoc, legacyDoc, presetDoc, preview, revision, summary as stepSummary } from "./steps";

const now = Date.now(), ago = (min: number) => new Date(now - min * 60e3).toISOString();
const GB = 1e9;
const mockMode = new URLSearchParams(location.search).get("mock");

function library(id: ID, name: string, path: string, freed: number, waitingToFree: number, files: number): Library {
  return {
    id, name, paths: [path], fileType: "video", enabled: true, workflowId: "w1", workflowName: "Shrink videos", priority: 0,
    scan: { state: "idle", progress: null, lastScanAt: ago(90), nextScanAt: ago(-270), scheduleLabel: "Every 6 hours" },
    counts: { files, waiting: Math.round(files * 0.4), running: 0, completed: Math.round(files * 0.5), skipped: 12, failed: 1 },
    bytes: { freed, waitingToFree },
    output: { policy: "separate_folder", folder: path + " (shrunk)", overrideFolder: null, problem: null },
    discovery: { subfolders: true, settleSeconds: 15, followLinks: false, include: [], exclude: [] },
    status: "ok",
  };
}
function worker(id: ID, name: string, status: Worker["status"], cpu: [number, number], gpu: [number, number], since: number, jobs: ID[]): Worker {
  return {
    id, name, kind: id === "this" ? "local" : "remote", host: name.toLowerCase().replace(/\s+/g, "-"), os: "linux", arch: "amd64", appVersion: "2.0.0",
    status, statusSince: ago(since), lastHeartbeatAt: status === "offline" ? ago(since) : ago(0),
    slots: { cpu: { used: cpu[0], total: cpu[1] }, gpu: { used: gpu[0], total: gpu[1] } },
    live: { cpuPercent: null, memUsedBytes: null, memTotalBytes: null, gpu: [] }, gpus: [], ffmpeg: null, capabilities: [],
    currentJobIds: jobs, tags: [], pool: null, schedule: null, pathMappings: [],
  };
}
function job(id: ID, name: string, lib: [ID, string], state: JobState, extra: Partial<Job>): Job {
  return {
    id, kind: "normal", state, subState: null,
    file: { path: "/media/" + lib[1].toLowerCase() + "/" + name, name, folder: ".", bytes: 8 * GB, codec: "H.264", resolution: "1080p", durationSec: 7200 },
    library: { id: lib[0], name: lib[1] }, workflow: { id: "w1", name: "Shrink videos", revisionId: "w1r3", version: 3 }, priority: 0,
    worker: null, currentStep: null, progress: null, attempts: 1, maxAttempts: 3, retryAt: null, reason: null, originalUntouched: true,
    sizes: { before: 8 * GB, after: null, saved: null }, output: { path: null, policy: "separate_folder", committedAt: null }, originalState: "untouched",
    createdAt: ago(300), startedAt: null, endedAt: null, durationSec: null, ...extra,
  };
}
function workflow(id: ID, name: string, description: string, extra: Partial<Workflow>): Workflow {
  return {
    id, name, description, fileType: "video", status: "enabled", favourite: false, tags: [], presetId: null,
    summaryTag: "HEVC · Balanced · MKV", publishedRevisionId: id + "r1", version: 1, draftDiffers: false,
    validation: { errors: 0, warnings: 0 }, libraries: [], activeJobs: 0, lastRun: { at: null, completed: 0, failed: 0, skipped: 0 },
    changesOriginals: false, createdAt: ago(60 * 24 * 30), changedAt: ago(60 * 24 * 2), ...extra,
  };
}
// GET /templates: the V1 server has only the Start Simple preset; more come with the engine (review P3).
export const TEMPLATES: Template[] = [
  { id: "shrink_videos", name: "Shrink videos", fileType: "video", description: "Converts to HEVC and keeps your languages", changesOriginals: false,
    steps: ["Leave some files alone", "Convert video", "Audio", "Subtitles", "File type", "Check the result", "Save the result"] },
];
const MOVIES: [ID, string] = ["l1", "Movies"], TV: [ID, string] = ["l2", "TV Shows"], HOME: [ID, string] = ["l3", "Home videos"];

interface State {
  settings: Settings;
  libraries: Library[];
  workers: Worker[];
  jobs: Job[];
  waiting: number;
  last24h: Summary["last24h"];
  activity: ActivityEvent[];
  wf: Workflow[];
  trash: Trash; // GET /trash (not on dev yet: mocked like the rest of the page)
}

function running(): State {
  const step = (index: number, name: string) => ({ nodeId: "n" + index, name, index, count: 5 });
  return {
    settings: { firstRunDone: true, firstRunStep: 4, intakePaused: false, catalogueEnabled: false, firstRunLibrary: null },
    libraries: [library("l1", "Movies", "/media/movies", 8.1 * GB, 780 * GB, 1600), library("l2", "TV Shows", "/media/tv", 0, 410 * GB, 900), { ...library("l3", "Home videos", "/media/home", 28.3 * GB, 10 * GB, 300), workflowId: "w2", workflowName: "Home videos - my rules" }],
    workers: [worker("this", "This computer", "busy", [1, 2], [0, 1], 120, ["j-48213"]), worker("nas", "NAS box", "busy", [1, 1], [0, 0], 300, ["j-48214"]), worker("laptop", "Laptop", "offline", [0, 1], [0, 0], 22, [])],
    jobs: [
      job("j-48213", "Dune.Part.Two.2024.2160p.REMUX.mkv", MOVIES, "running", { subState: "converting", worker: { id: "this", name: "This computer" }, currentStep: step(3, "Convert video"), startedAt: ago(12),
        progress: { stage: "Converting video", percent: 62, workflowPercent: 55, elapsedS: 728, speed: 1.7, etaS: 450, fps: 41, bitrateKbps: 9800 } }),
      job("j-48214", "The.Bear.S03E04.1080p.WEB-DL.mkv", TV, "running", { subState: "converting", worker: { id: "nas", name: "NAS box" }, currentStep: step(3, "Convert video"), startedAt: ago(4),
        progress: { stage: "Converting video", percent: 18, workflowPercent: 30, elapsedS: 222, speed: 0.4, etaS: null, fps: 9, bitrateKbps: 4100 } }),
      job("j-48190", "Wedding.Raw.2018.mov", HOME, "failed", { endedAt: ago(11), currentStep: step(3, "Convert video"), reason: { code: "encoder_failed", message: "The encoder ran out of graphics memory." } }),
      job("j-48161", "Arrival.2016.1080p.BluRay.mkv", MOVIES, "failed", { endedAt: ago(102), currentStep: step(4, "Check the result"), reason: { code: "check_failed", message: "The new file is 4 s shorter than the original. The original is kept." } }),
      job("j-48102", "Severance.S02E07.1080p.WEB-DL.mkv", TV, "failed", { endedAt: ago(255), currentStep: step(5, "Save the result"), reason: { code: "not_writable", message: "Can't write to /media/shrunk: no space left." } }),
    ],
    waiting: 1284,
    last24h: { completed: 142, failed: 3, skipped: 9, successRate: 0.979 },
    activity: [
      { id: "a3", at: ago(1), type: "job.completed", text: "Oppenheimer.2023.1080p.BluRay.mkv done, 41% smaller", refs: { jobId: "j-48212" } },
      { id: "a2", at: ago(6), type: "job.skipped", text: "Blade.Runner.2049.2017.2160p.DV.mkv skipped: Dolby Vision (profile 5)", refs: { jobId: "j-48211" } },
      { id: "a1", at: ago(22), type: "worker.left", text: "Laptop stopped answering", refs: { workerId: "laptop" } },
    ],
    wf: [
      workflow("w1", "Shrink videos", "Converts videos to HEVC, keeps your languages", { favourite: true, tags: ["default"], presetId: "shrink_videos", version: 4, draftDiffers: true,
        summaryTag: "HEVC · Balanced · same resolution · Stereo AAC · MKV", libraries: [{ id: "l1", name: "Movies" }, { id: "l2", name: "TV Shows" }], activeJobs: 2, lastRun: { at: ago(1), completed: 131, failed: 2, skipped: 9 } }),
      workflow("w2", "Home videos - my rules", "Replaces originals, keeps all audio, extra checks", { tags: ["home"], version: 2, validation: { errors: 0, warnings: 2 },
        libraries: [{ id: "l3", name: "Home videos" }], changesOriginals: true, summaryTag: "HEVC · Better quality · All audio · MKV · Replaces originals" }),
      workflow("w3", "Audio and subtitles only", "Leaves the picture alone", { status: "disabled", summaryTag: "Audio and subtitles · MKV" }),
      workflow("w4", "Old settings", "Carried over from an earlier install", { summaryTag: "Settings from an earlier install" }),
    ],
    trash: { bytes: 120 * GB, files: 14, nextDeleteAt: ago(-60 * 24), lastDeleteAt: ago(-60 * 24 * 7) },
  };
}
function fresh(): State {
  return {
    settings: { firstRunDone: false, firstRunStep: 1, intakePaused: false, catalogueEnabled: false, firstRunLibrary: null },
    libraries: [],
    workers: [worker("this", "This computer", "paused", [0, 2], [0, 0], 5, [])], // workers start stopped (rebuild decision)
    jobs: [], waiting: 0, last24h: { completed: 0, failed: 0, skipped: 0, successRate: null }, activity: [], wf: [], trash: { bytes: 0, files: 0, nextDeleteAt: null, lastDeleteAt: null },
  };
}

let state: State = mockMode === "fresh" ? fresh() : running();
let version = 0;
// When the data last changed (the real app: when the summary or a live event last arrived). The Dashboard's "Updated".
export let updatedAt = new Date();
const listeners = new Set<() => void>();
const change = (f: (s: State) => void) => { f(state); state = { ...state }; version++; updatedAt = new Date(); listeners.forEach((l) => l()); };
const log = (type: ActivityEvent["type"], text: string, refs: ActivityEvent["refs"] = {}) =>
  state.activity.unshift({ id: "a" + Date.now() + Math.random(), at: new Date().toISOString(), type, text, refs });

export const mockState = () => state; // the same, outside a component (libraryApi.ts)
export function useMock(): State {
  useSyncExternalStore((f) => (listeners.add(f), () => listeners.delete(f)), () => version);
  return state;
}

// GET /summary, computed from the state the way the server would.
export function summary(s: State): Summary {
  const by = (st: JobState) => s.jobs.filter((j) => j.state === st).length;
  const counts = Object.fromEntries((["queued", "preparing", "running", "validating", "committing", "completed", "skipped", "failed", "cancelled", "retry_wait"] as JobState[]).map((st) => [st, by(st)])) as Record<JobState, number>;
  counts.queued += s.waiting;
  const active = s.jobs.filter((j) => ["preparing", "running", "validating", "committing"].includes(j.state));
  const workersRunning = s.workers.filter((w) => w.status === "online" || w.status === "busy").length;
  return {
    seq: version,
    badges: { failed24h: s.last24h.failed, workersOffline: s.workers.filter((w) => w.status === "offline" || w.status === "stalled").length, pluginUpdates: 0 },
    activity: { converting: active.length, waiting: counts.queued + counts.retry_wait, workersRunning },
    jobCounts: counts,
    // The rate follows the 24 h counts, so a retried failure changes it (completed of completed + failed; skips aren't failures).
    last24h: { ...s.last24h, successRate: s.last24h.completed + s.last24h.failed ? s.last24h.completed / (s.last24h.completed + s.last24h.failed) : null },
    bytes: {
      freed: s.libraries.reduce((a, l) => a + l.bytes.freed, 0), waitingToFree: s.libraries.reduce((a, l) => a + l.bytes.waitingToFree, 0), freedLast7d: 0,
      perLibrary: s.libraries.map((l) => ({ libraryId: l.id, freed: l.bytes.freed, waitingToFree: l.bytes.waitingToFree })),
    },
    workers: s.workers.map((w) => ({ id: w.id, name: w.name, status: w.status, slots: w.slots, currentJobIds: w.currentJobIds })),
    activeJobs: active,
    recentFailures: s.jobs.filter((j) => j.state === "failed").slice(0, 5),
    throughput: [], // CAN'T YET in the engine (job_events): the Dashboard hides Throughput
    processingPaused: workersRunning === 0,
    intakePaused: s.settings.intakePaused,
  };
}

// Actions. Each is the same action as its one place (Workers, Libraries, Jobs); here it changes the mock state.
export function startAll() {
  change((s) => s.workers.forEach((w) => {
    if (w.status === "paused") (w.status = "online", (w.statusSince = new Date().toISOString()));
  }));
  change(() => log("setting.changed", "All workers started"));
}
export function stopAll() {
  change((s) => {
    for (const j of s.jobs) if (j.state === "running") (j.state = "queued", (j.subState = "processing_paused"), (j.worker = null), (j.progress = null), (j.currentStep = null));
    s.jobs = s.jobs.filter((j) => j.state !== "queued"); // back in the queue
    s.waiting += s.workers.reduce((a, w) => a + w.currentJobIds.length, 0);
    for (const w of s.workers) if (w.status === "online" || w.status === "busy") (w.status = "paused", (w.currentJobIds = []), (w.slots.cpu.used = 0), (w.slots.gpu.used = 0), (w.statusSince = new Date().toISOString()));
    log("setting.changed", "All workers stopped; the files they were converting went back to the queue");
  });
}
export function scanAll() {
  change((s) => s.libraries.forEach((l) => (l.scan.state = "scanning")));
  setTimeout(() => change((s) => {
    s.libraries.forEach((l) => ((l.scan.state = "idle"), (l.scan.lastScanAt = new Date().toISOString())));
    log("library.scanned", s.libraries.length === 1 ? "Scanned " + s.libraries[0].name + ": no new files" : "Scanned " + s.libraries.length + " libraries: no new files");
  }), 1200);
}
export function retry(id: ID) {
  change((s) => {
    const j = s.jobs.find((x) => x.id === id);
    if (!j || j.state !== "failed") return;
    s.jobs = s.jobs.filter((x) => x !== j);
    s.waiting += 1;
    s.last24h = { ...s.last24h, failed: Math.max(0, s.last24h.failed - 1) };
    log("setting.changed", j.file.name + " is back in the queue", { jobId: id });
  });
}

// Workflows (V3). POST /workflows creates the workflow and its draft (status "draft", never published).
let made = 0;
// The V1 server always starts from the Shrink videos preset (templateId other than shrink_videos: 400).
export function createWorkflow(name: string, description: string): Workflow {
  const w = workflow("wn" + ++made, name, description, {
    status: "draft", version: null, publishedRevisionId: null, presetId: "shrink_videos",
    summaryTag: "HEVC · Balanced · MKV", createdAt: new Date().toISOString(), changedAt: new Date().toISOString(),
  });
  change((s) => s.wf.push(w));
  return w;
}
export function duplicateWorkflow(id: ID) {
  change((s) => {
    const w = s.wf.find((x) => x.id === id);
    if (!w) return;
    // As the V1 server does: "X (copy)", enabled, version 1 (review P3).
    s.wf.push({ ...w, id: "wd" + ++made, name: w.name + " (copy)", favourite: false, libraries: [], activeJobs: 0, status: "enabled", version: 1, publishedRevisionId: "wd" + made + "r1", draftDiffers: false, lastRun: { at: null, completed: 0, failed: 0, skipped: 0 } });
  });
}
export const setWorkflow = (id: ID, patch: Partial<Pick<Workflow, "status" | "favourite" | "name">>) =>
  change((s) => { s.wf = s.wf.map((w) => (w.id === id ? { ...w, ...patch } : w)); });
// DELETE answers 409 in_use while libraries use it; the page doesn't offer it then.
export function deleteWorkflow(id: ID): string | null {
  const w = state.wf.find((x) => x.id === id);
  if (!w) return "That workflow is gone.";
  if (w.libraries.length) return "Used by " + w.libraries.map((l) => l.name).join(", ") + ". Nothing was changed.";
  change((s) => { s.wf = s.wf.filter((x) => x.id !== id); });
  return null;
}

// Drafts (GET/PUT /workflows/{id}/draft, publish-preview, publish). A draft is made from the published revision on first
// read; one per workflow. The mock's published documents: the preset, or a branched one for "Home videos - my rules".
const drafts: Record<ID, { draft: WorkflowRevision; published: WorkflowDoc | null }> = {};
function slot(id: ID) {
  const w = state.wf.find((x) => x.id === id);
  if (!w) return null;
  if (!drafts[id]) {
    const published = w.version == null ? null : id === "w2" ? branchedDoc() : id === "w4" ? legacyDoc() : presetDoc();
    drafts[id] = { published, draft: revision(id, structuredClone(published ?? presetDoc()), null) };
  }
  return { w, d: drafts[id] };
}
export function getDraft(id: ID): WorkflowRevision | null {
  return slot(id)?.d.draft ?? null;
}
// PUT draft with If-Match: a stale etag is refused (409 stale_draft).
export function saveDraft(id: ID, doc: WorkflowDoc, etag: string): { draft?: WorkflowRevision; error?: string } {
  const x = slot(id);
  if (!x) return { error: "That workflow is gone." };
  if (x.d.draft.etag !== etag) return { error: "This draft was changed somewhere else. Reload to see it. Nothing was saved." };
  const draft = revision(id, structuredClone(doc), null);
  x.d.draft = draft;
  const errors = draft.validation.filter((v) => v.severity === "error").length;
  const commit = doc.nodes.find((n) => n.type === "output.commit");
  change((s) => {
    s.wf = s.wf.map((w) => w.id !== id ? w : { ...w, changedAt: new Date().toISOString(), draftDiffers: JSON.stringify(doc) !== JSON.stringify(x.d.published),
      validation: { errors, warnings: draft.validation.length - errors }, changesOriginals: commit?.config.policy === "replace",
      summaryTag: doc.nodes.filter((n) => n.type === "video.convert" || n.type === "container.output").map(stepSummary).join(" · ") });
  });
  return { draft };
}
export function publishPreview(id: ID): PublishPreview | null {
  const x = slot(id);
  return x && preview(x.d.draft.workflow, x.d.published, (x.w.version ?? 0) + 1, x.w.libraries);
}
// POST publish {etag}: 400 has_errors while the draft has errors. Running jobs keep the revision they started with.
export function publish(id: ID, etag: string): { version?: number; error?: string } {
  const x = slot(id);
  if (!x) return { error: "That workflow is gone." };
  if (x.d.draft.etag !== etag) return { error: "Save the draft first." };
  if (x.d.draft.validation.some((v) => v.severity === "error")) return { error: "Fix the problems first. Nothing was published." };
  const version = (x.w.version ?? 0) + 1;
  x.d.published = structuredClone(x.d.draft.workflow);
  x.d.draft = revision(id, structuredClone(x.d.published), null);
  change((s) => {
    s.wf = s.wf.map((w) => w.id !== id ? w : { ...w, version, publishedRevisionId: id + "r" + version, draftDiffers: false, status: w.status === "draft" ? "enabled" : w.status });
    log("workflow.published", x.w.name + " v" + version + " published", { workflowId: id });
  });
  return { version };
}

// Libraries (V5), on v1's routes until /api/v2/libraries comes (contract v2, "Libraries: what exists now"). The state
// keeps the v2 Library (the Dashboard reads it); these functions answer in v1's shapes and with v1's error codes.
// There is no pause or resume for a library in v1: a new library isn't started until POST /api/libraries/{id}/scan.
type Answer<T> = T | { error: Issue };
const trim = (p: string) => p.trim().replace(/(.)\/+$/, "$1");
const systemPath = (p: string) => p === "/" || /^\/(etc|usr|bin|boot|dev|proc|sys|var)(\/|$)/.test(p);
const SYSTEM = "That is a system folder. Choose the folder that holds your videos.";
const UNREADABLE = "We can't open this folder. Check the path, and that shrink-your-shit is allowed to read it.";
const free: Record<ID, number | null> = { l1: 1.9e12, l2: 1.9e12, l3: 410 * GB }; // freeBytes (workshop 5053a73)
export function libraryItem(l: Library): LibraryItem {
  const c = l.counts, scanning = l.scan.state === "scanning", started = l.enabled;
  return {
    id: l.id, name: l.name, path: l.paths[0], state: !started ? "not_started" : c.running ? "working" : c.failed ? "needs_attention" : "idle", started,
    workingFiles: c.running, waitingFiles: c.waiting, nextScan: started ? l.scan.nextScanAt : null, freedBytes: l.bytes.freed, waitingBytes: l.bytes.waitingToFree,
    flowId: l.workflowId, settingLabel: l.workflowName, settingKind: "workflow", madeWith: "workflow", tag: "", failedFiles: c.failed,
    status: { kind: c.running ? "converting" : scanning ? "scanning" : l.scan.lastScanAt ? "scanned" : "not_scanned", videos: c.files, foundSoFar: scanning ? l.scan.progress ?? 0 : null,
      lastScan: l.scan.lastScanAt, nextScan: started ? l.scan.nextScanAt : null, converting: c.running, toDo: c.waiting },
    freeBytes: l.id in free ? free[l.id] : 1.2e12,
  };
}
// POST /api/libraries/check {path}. /readonly, /empty, /full and /locked show v1's other answers.
export function checkFolder(path: string): Answer<FolderReport> {
  const p = trim(path), r: FolderReport = { ok: false, blockers: [], notes: [], videoCount: 0, totalBytes: 0 };
  if (!p.startsWith("/")) return { error: { code: "invalid_path", message: "Choose a folder by its full path." } };
  if (systemPath(p)) r.blockers.push({ code: "system_folder", message: SYSTEM });
  else if (/missing|locked/.test(p)) r.blockers.push({ code: "unreadable", message: UNREADABLE });
  else if (/readonly/.test(p)) r.blockers.push({ code: "read_only", message: "This folder can be read but not written, so converted files can't be saved here." });
  else if (/empty/.test(p)) r.blockers.push({ code: "no_videos", message: "We found no videos here. Pick the folder that holds them." });
  else if (/full/.test(p)) r.blockers.push({ code: "low_space", message: "Only 2 GB is free on this disk. Free some space, or choose another disk." });
  else (r.videoCount = 1284), (r.totalBytes = 9.4e12), /tv/.test(p) && r.notes.push({ code: "hardlinks", message: "3 files are shared with another folder (for example by Radarr). We will leave them alone, because changing one would break the link." });
  r.ok = !r.blockers.length;
  return r;
}
const SAMPLE: [string, string, number, string, string][] = [
  ["Dune Part Two (2024)", "Dune.Part.Two.2024.2160p.REMUX.mkv", 58.2, "H.264", "2160p"], ["Arrival (2016)", "Arrival.2016.1080p.BluRay.mkv", 11.4, "H.264", "1080p"],
  ["Oppenheimer (2023)", "Oppenheimer.2023.1080p.BluRay.mkv", 14.9, "H.264", "1080p"], ["Blade Runner 2049 (2017)", "Blade.Runner.2049.2017.2160p.DV.mkv", 41, "HEVC", "2160p"],
  ["Alien (1979)", "Alien.1979.1080p.mkv", 9.8, "H.264", "1080p"]];
// POST /api/libraries/first-files {path} (and GET /api/libraries/{id}/first-files): the first 5 videos; nothing is converted.
export function firstFiles(path: string): Answer<FirstFiles> {
  const p = trim(path);
  if (!p.startsWith("/")) return { error: { code: "invalid_path", message: "Choose a folder by its full path." } };
  if (systemPath(p)) return { error: { code: "system_folder", message: SYSTEM } };
  if (/missing|locked/.test(p)) return { error: { code: "unreadable", message: UNREADABLE } };
  if (/empty/.test(p)) return { items: [], total: 0, stillLooking: false };
  return { items: SAMPLE.map(([folder, name, gb, codec, resolution]) => ({ path: p + "/" + folder + "/" + name, folder, name, bytes: gb * GB, state: "ready", codec, resolution, durationSec: 7000 })), total: 1284, stillLooking: false };
}
// What POST and PATCH check about a folder (libraryPath), and the workflow (storeErr, folderRefused).
function pathError(path: string, except?: ID): Issue | null {
  const p = trim(path);
  if (!p.startsWith("/")) return { code: "invalid_path", message: "Choose a folder by its full path." };
  if (systemPath(p)) return { code: "system_folder", message: SYSTEM };
  if (/missing|locked/.test(p)) return { code: "not_a_folder", message: "That folder does not exist, or the program cannot see it." };
  for (const l of state.libraries.filter((x) => x.id !== except)) {
    const x = l.paths[0];
    if (x === p) return { code: "path_taken", message: "Another library already uses this folder: " + l.name + "." };
    if (p.startsWith(x + "/") || x.startsWith(p + "/")) return { code: "path_overlaps", message: "This folder is inside, or contains, the folder of library " + l.name + ". Choose a folder that doesn't overlap." };
  }
  return null;
}
function workflowError(id: ID): Issue | null {
  const w = state.wf.find((x) => x.id === id);
  if (!w) return { code: "invalid_flow", message: "There is no such flow." };
  if (w.publishedRevisionId == null) return { code: "workflow_not_published", message: "This workflow hasn't been published yet, so nothing can use it. Publish it first. Nothing was changed." };
  return null;
}
// folderRefused: a workflow that saves new files inside the library's folder (or one holding it) would convert them again.
function outputInside(flowId: ID, path: string): Issue | null {
  const out = drafts[flowId]?.published?.nodes.find((n) => n.type === "output.commit")?.config.folder, p = trim(path);
  if (typeof out !== "string" || !out) return null;
  const o = trim(out);
  return o === p || o.startsWith(p + "/") || p.startsWith(o + "/")
    ? { code: "inside_library", message: "This flow saves new files to " + o + ", which is inside this library (or holds it), so new files would be converted again. Choose another flow, or change the flow's folder. Nothing was changed." } : null;
}
const linkWorkflows = (s: State) => { s.wf = s.wf.map((w) => ({ ...w, libraries: s.libraries.filter((l) => l.workflowId === w.id).map((l) => ({ id: l.id, name: l.name })) })); };
// POST /api/libraries {name, path, flowId}: 201 with the library, not started (nothing scanned or queued).
export function addLibrary(name: string, path: string, flowId: ID): Answer<LibraryItem> {
  if (!name.trim()) return { error: { code: "invalid_name", message: "Give the library a name." } };
  const e = pathError(path) ?? workflowError(flowId) ?? outputInside(flowId, path);
  if (e) return { error: e };
  const l = library("l" + Date.now().toString(36), name.trim(), trim(path), 0, 0, 0);
  l.workflowId = flowId; l.workflowName = state.wf.find((w) => w.id === flowId)!.name; l.enabled = false;
  l.counts = { files: 0, waiting: 0, running: 0, completed: 0, skipped: 0, failed: 0 };
  l.scan = { ...l.scan, lastScanAt: null, nextScanAt: null };
  change((s) => { s.libraries = [...s.libraries, l]; linkWorkflows(s); s.settings = { ...s.settings, firstRunLibrary: null }; log("library.scanned", "Added the library " + l.name, { libraryId: l.id }); });
  return libraryItem(l);
}
// PATCH /api/libraries/{id} {name?, path?}: a new path gets the same checks as adding one.
export function patchLibrary(id: ID, patch: { name?: string; path?: string }): Answer<LibraryItem> {
  const l = state.libraries.find((x) => x.id === id);
  if (!l) return { error: { code: "not_found", message: "There is no such library." } };
  if (patch.name !== undefined && !patch.name.trim()) return { error: { code: "invalid_name", message: "Give the library a name." } };
  const e = patch.path !== undefined ? pathError(patch.path, id) : null;
  if (e) return { error: e };
  change((s) => {
    const x = s.libraries.find((y) => y.id === id)!;
    if (patch.name !== undefined) x.name = patch.name.trim();
    if (patch.path !== undefined) x.paths = [trim(patch.path)];
    linkWorkflows(s);
  });
  return libraryItem(state.libraries.find((x) => x.id === id)!);
}
// PUT /api/libraries/{id}/flow {flowId}
export function setLibraryWorkflow(id: ID, flowId: ID): Answer<LibraryItem> {
  const e = workflowError(flowId);
  if (e) return { error: e };
  change((s) => { const x = s.libraries.find((y) => y.id === id); if (x) (x.workflowId = flowId), (x.workflowName = s.wf.find((w) => w.id === flowId)!.name); linkWorkflows(s); });
  return libraryItem(state.libraries.find((x) => x.id === id)!);
}
// POST /api/libraries/{id}/scan: starts the library if it wasn't, and looks for files now.
export function scanLibrary(id: ID) {
  change((s) => { const l = s.libraries.find((x) => x.id === id); if (l) (l.enabled = true), (l.status = "scanning"), (l.scan = { ...l.scan, state: "scanning", progress: 0 }); });
  setTimeout(() => change((s) => {
    const l = s.libraries.find((x) => x.id === id);
    if (!l || l.scan.state !== "scanning") return;
    const found = l.counts.files ? 0 : 1284;
    l.scan = { ...l.scan, state: "idle", progress: null, lastScanAt: new Date().toISOString(), nextScanAt: new Date(Date.now() + 6 * 36e5).toISOString() };
    l.status = "ok";
    if (found) l.counts = { ...l.counts, files: found, waiting: found };
    log("library.scanned", "Scanned " + l.name + ": " + (found ? found.toLocaleString("en-US") + " videos found" : "no new files"), { libraryId: id });
  }), 1500);
}
// PATCH /settings
export const setSettings = (patch: Partial<Settings>) => change((s) => { s.settings = { ...s.settings, ...patch }; });
