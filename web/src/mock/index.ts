// The one mock module (NEXT-ui-v2.md "Contracts"): in-memory state in the contract's types, and the actions the
// screens offer, really applied to that state. Every screen using it shows the Prototype banner. ?mock=fresh shows a
// new install (no libraries yet) for review. When a contract endpoint lands, its function here becomes the fetch.
import { useSyncExternalStore } from "react";
import type { ActivityEvent, ID, Job, JobState, Library, Settings, Summary, Template, Trash, Worker, Workflow } from "../types/v2";

const now = Date.now(), ago = (min: number) => new Date(now - min * 60e3).toISOString();
const GB = 1e9;

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
    settings: { firstRunDone: true, firstRunStep: 4, intakePaused: false, catalogueEnabled: false },
    libraries: [library("l1", "Movies", "/media/movies", 8.1 * GB, 780 * GB, 1600), library("l2", "TV Shows", "/media/tv", 0, 410 * GB, 900), library("l3", "Home videos", "/media/home", 28.3 * GB, 10 * GB, 300)],
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
    ],
    trash: { bytes: 120 * GB, files: 14, nextDeleteAt: ago(-60 * 24), lastDeleteAt: ago(-60 * 24 * 7) },
  };
}
function fresh(): State {
  return {
    settings: { firstRunDone: false, firstRunStep: 1, intakePaused: false, catalogueEnabled: false },
    libraries: [library("l1", "Movies", "/media/movies", 0, 0, 1284)],
    workers: [worker("this", "This computer", "paused", [0, 2], [0, 0], 5, [])], // workers start stopped (rebuild decision)
    jobs: [], waiting: 0, last24h: { completed: 0, failed: 0, skipped: 0, successRate: null }, activity: [], wf: [], trash: { bytes: 0, files: 0, nextDeleteAt: null, lastDeleteAt: null },
  };
}

let state: State = new URLSearchParams(location.search).get("mock") === "fresh" ? fresh() : running();
let version = 0;
// When the data last changed (the real app: when the summary or a live event last arrived). The Dashboard's "Updated".
export let updatedAt = new Date();
const listeners = new Set<() => void>();
const change = (f: (s: State) => void) => { f(state); state = { ...state }; version++; updatedAt = new Date(); listeners.forEach((l) => l()); };
const log = (type: ActivityEvent["type"], text: string, refs: ActivityEvent["refs"] = {}) =>
  state.activity.unshift({ id: "a" + Date.now() + Math.random(), at: new Date().toISOString(), type, text, refs });

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
export const setWorkflow = (id: ID, patch: Partial<Pick<Workflow, "status" | "favourite">>) =>
  change((s) => { s.wf = s.wf.map((w) => (w.id === id ? { ...w, ...patch } : w)); });
// DELETE answers 409 in_use while libraries use it; the page doesn't offer it then.
export function deleteWorkflow(id: ID): string | null {
  const w = state.wf.find((x) => x.id === id);
  if (!w) return "That workflow is gone.";
  if (w.libraries.length) return "Used by " + w.libraries.map((l) => l.name).join(", ") + ". Nothing was changed.";
  change((s) => { s.wf = s.wf.filter((x) => x.id !== id); });
  return null;
}
