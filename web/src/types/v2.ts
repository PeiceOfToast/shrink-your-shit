// The server's types from workshop's contract (missions/.../UI-API-CONTRACT-v2.md, draft 2), field names exactly.
// Only the shapes the built screens use so far; the rest join with their screens. Mocks (../mock) use these types,
// so swapping a mock for the real /api/v2 call changes no screen code.
export type ID = string;
export type Time = string; // RFC 3339 UTC
export type FileType = "video";
export interface List<T> { items: T[]; total: number; counts?: Record<string, number> }

export type JobState = "queued" | "preparing" | "running" | "validating" | "committing"
  | "completed" | "skipped" | "failed" | "cancelled" | "retry_wait";
export type JobSubState =
  | "waiting" | "processing_paused" | "library_disabled" | "no_capable_worker"
  | "retrying"
  | "probing" | "converting" | "checking" | "saving"
  | null;
export type CommitPolicy = "separate_folder" | "beside" | "replace";

export interface Job {
  id: ID;
  kind: "normal" | "test";
  state: JobState;
  subState: JobSubState;
  file: { path: string; name: string; folder: string; bytes: number; codec: string | null; resolution: string | null; durationSec: number | null };
  library: { id: ID; name: string } | null;
  workflow: { id: ID; name: string; revisionId: ID; version: number };
  priority: number;
  worker: { id: ID; name: string } | null;
  currentStep: { nodeId: ID; name: string; index: number; count: number } | null;
  progress: {
    stage: string;
    percent: number | null; // of the current step, only when known; never the whole job's
    workflowPercent: number;
    elapsedS: number;
    speed: number | null;
    etaS: number | null;
    fps: number | null; bitrateKbps: number | null;
  } | null;
  attempts: number; maxAttempts: number; retryAt: Time | null;
  reason: { code: string; message: string } | null;
  originalUntouched: boolean | null;
  sizes: { before: number; after: number | null; saved: number | null };
  output: { path: string | null; policy: CommitPolicy; committedAt: Time | null };
  originalState: "untouched" | "kept" | "replaced" | "restored" | "removed" | null;
  createdAt: Time; startedAt: Time | null; endedAt: Time | null; durationSec: number | null;
}

export type WorkerStatus = "online" | "busy" | "draining" | "paused" | "offline" | "stalled";
export interface Capability {
  key: string;
  kind: "encoder" | "decoder" | "hwaccel" | "filter" | "tool";
  label: string;
  slot: "cpu" | "gpu";
  device?: string;
  test: "passed" | "failed" | "untested";
  testedAt: Time | null; detail?: string;
}
export interface Worker {
  id: ID; name: string; kind: "local" | "remote";
  host: string; os: "linux" | "windows" | "darwin"; arch: string; appVersion: string;
  status: WorkerStatus; statusSince: Time;
  lastHeartbeatAt: Time | null;
  slots: { cpu: { used: number; total: number }; gpu: { used: number; total: number } };
  live: { cpuPercent: number | null; memUsedBytes: number | null; memTotalBytes: number | null;
    gpu: { index: number; utilPercent: number | null; vramUsedBytes: number | null }[] };
  gpus: { index: number; vendor: "nvidia" | "intel" | "amd" | "apple" | "other"; model: string; vramBytes: number | null }[];
  ffmpeg: { path: string; version: string } | null;
  capabilities: Capability[];
  currentJobIds: ID[];
  tags: string[]; pool: string | null;
  schedule: { from: string; to: string } | null;
  pathMappings: { server: string; worker: string; test: "read" | "read_write" | "failed" | null; testedAt: Time | null }[];
}

export interface Library {
  id: ID;
  name: string;
  paths: string[];
  fileType: FileType;
  enabled: boolean;
  workflowId: ID;
  workflowName: string;
  priority: number;
  scan: { state: "idle" | "scanning" | "settling"; progress: number | null; lastScanAt: Time | null; nextScanAt: Time | null; scheduleLabel: string };
  counts: { files: number; waiting: number; running: number; completed: number; skipped: number; failed: number };
  bytes: { freed: number; waitingToFree: number };
  output: {
    policy: CommitPolicy;
    folder: string | null;
    overrideFolder: string | null;
    problem: { code: "no_default_folder" | "system_folder" | "inside_library" | "not_writable" | "low_space"; message: string } | null;
  };
  discovery: { subfolders: boolean; settleSeconds: number; followLinks: boolean; include: string[]; exclude: string[] };
  status: "ok" | "scanning" | "path_missing" | "not_readable" | "disabled";
}

export interface Summary { // GET /summary
  seq: number;
  badges: { failed24h: number; workersOffline: number; pluginUpdates: number };
  activity: { converting: number; waiting: number; workersRunning: number };
  jobCounts: Record<JobState, number>;
  last24h: { completed: number; failed: number; skipped: number; successRate: number | null };
  bytes: {
    freed: number;
    waitingToFree: number;
    freedLast7d: number;
    perLibrary: { libraryId: ID; freed: number; waitingToFree: number }[];
  };
  workers: { id: ID; name: string; status: WorkerStatus; slots: Worker["slots"]; currentJobIds: ID[] }[];
  activeJobs: Job[];
  recentFailures: Job[];
  throughput: { hourStart: Time; completed: number; savedBytes: number }[];
  processingPaused: boolean;
  intakePaused: boolean;
}

export interface ActivityEvent { // GET /activity
  id: ID; at: Time;
  type: "job.completed" | "job.skipped" | "job.failed" | "worker.joined" | "worker.left" | "setting.changed" | "workflow.published" | "library.scanned";
  text: string;
  refs: { jobId?: ID; workerId?: ID; workflowId?: ID; libraryId?: ID };
}

// v1's settings, re-exposed at /api/v2/settings (contract: Settings). First run's progress is in them.
export interface Settings { firstRunDone: boolean; firstRunStep: number; intakePaused: boolean; catalogueEnabled: boolean }

// GET /api/v2/trash (contract update 01:33Z; workshop branch v2-trash-settings): originals replaced and kept in the
// trash until their date. Later embedded as Summary.bytes.trash.
export interface Trash { bytes: number; files: number; nextDeleteAt: Time | null; lastDeleteAt: Time | null }
