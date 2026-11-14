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
  test: "passed" | "failed" | "untested" | "unsupported" | "not_present"; // F1 #250 unsupported: the card can't do this format (dim). #254 not_present: no card for it here.
  testedAt: Time | null; detail?: string;
}
// E4 (ONE-COMMAND-SETUP.md section 9): GET /api/v2/workers/join-requests. A worker that ran the install command asks to
// join; decided and expired requests stay about 10 minutes so the card can show the outcome.
export interface JoinRequest {
  id: ID; name: string; address: string; os?: string; hardware: string | null; url: string;
  askedAt: Time; expiresAt: Time; state: "waiting" | "expired"; replacesWorker: boolean;
}
export interface Worker {
  id: ID; name: string; kind: "local" | "remote";
  host: string; os: "linux" | "windows" | "darwin"; arch: string; appVersion: string;
  status: WorkerStatus; statusSince: Time;
  lastHeartbeatAt: Time | null;
  slots: { cpu: { used: number; total: number }; gpu: { used: number; total: number } };
  live: { cpuPercent: number | null; memUsedBytes: number | null; memTotalBytes: number | null;
    gpu: { index: number; utilPercent: number | null; vramUsedBytes: number | null }[] };
  gpus: { index: number; vendor: "nvidia" | "intel" | "amd" | "apple" | "other"; model: string; vramBytes: number | null }[]; // CAN'T YET until V4 (empty)
  ffmpeg: { path: string; version: string } | null;
  capabilities: Capability[]; // CAN'T YET until V4 (empty)
  currentJobIds: ID[];
  tags: string[]; pool: string | null;
  schedule: { from: string; to: string } | null;
  pathMappings: { server: string; worker: string; test: "read" | "read_write" | "failed" | null; testedAt: Time | null }[];
  primaryEncoder?: string; // 'cpu' or an encoder name (hevc_qsv…): the effective choice, CPU is the fallback
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
  bytes: { freed: number; waitingToFree: number; grew?: number };
  output: {
    policy: CommitPolicy | null; // null: its workflow can't be read
    folder: string | null;
    overrideFolder: string | null;
    problem: { code: string; message: string } | null; // no_default_folder, inside_library...: show the message
  };
  discovery: { subfolders: boolean; settleSeconds: number; followLinks: boolean; include: string[]; exclude: string[]; scanEverySeconds?: number }; // V5b: the effective interval
  status: "ok" | "scanning" | "path_missing" | "not_readable" | "disabled";
}

export interface Summary { // GET /summary
  seq: number;
  badges: { failed24h: number; workersOffline: number; pluginUpdates: number; joinRequestsWaiting?: number };
  activity: { converting: number; waiting: number; workersRunning: number };
  jobCounts: Record<JobState, number>;
  last24h: { completed: number; failed: number; skipped: number; successRate: number | null };
  bytes: {
    freed: number;
    waitingToFree: number;
    freedLast7d: number;
    grew?: number;
    perLibrary: { libraryId: ID; freed: number; waitingToFree: number; grew?: number }[];
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

// GET /api/v2/settings: v1's settings (contract: Settings), including first run's firstRunDone/firstRunStep.
// firstRunLibrary (#116): first run's step 1 folder, kept until a library is created (then null; PATCH null clears it).
export interface Settings { firstRunDone: boolean; firstRunStep: number; intakePaused: boolean; catalogueEnabled: boolean; firstRunLibrary: { name: string; path: string } | null;
  scanEvery?: number; scanEverySource?: "flag" | "saved"; scanEveryMin?: number; scanEveryMax?: number;
  languages?: string[]; keepOriginalsDays?: number; trashPath?: string; authRequired?: boolean; logKeepDays?: number } // #136: seconds; "flag" until someone changes it here; #146: the bounds; E: job logs kept, days (0 = forever)

// GET /api/v2/trash (contract update 01:33Z; workshop branch v2-trash-settings): originals replaced and kept in the
// trash until their date. Later embedded as Summary.bytes.trash.
export interface Trash { bytes: number; files: number; nextDeleteAt: Time | null; lastDeleteAt: Time | null }
export interface Workflow { // GET /workflows (contract: Workflows, revisions and the graph)
  id: ID;
  name: string;
  description: string;
  fileType: FileType;
  status: "draft" | "enabled" | "disabled";
  favourite: boolean;
  tags: string[];
  presetId: "shrink_videos" | null;
  summaryTag: string;
  publishedRevisionId: ID | null;
  version: number | null;
  draftDiffers: boolean;
  validation: { errors: number; warnings: number };
  libraries: { id: ID; name: string }[];
  activeJobs: number;
  lastRun: { at: Time | null; completed: number; failed: number; skipped: number };
  changesOriginals: boolean;
  createdAt: Time; changedAt: Time;
}
export interface Template { id: ID; name: string; fileType: FileType; description: string; steps: string[]; changesOriginals: boolean } // GET /templates

// Workflows, revisions and the graph (contract v2): the draft the Stack view edits.
export interface WorkflowRevision {
  id: ID;
  workflowId: ID;
  number: number | null; // null for the draft
  status: "draft" | "published";
  workflow: WorkflowDoc;
  layout: Layout;
  logicHash: string;
  stackable: boolean;
  stepSummaries: Record<ID, string>;
  validation: ValidationIssue[];
  etag: string;
  createdAt: Time; publishedAt: Time | null;
}
export interface WorkflowDoc {
  schemaVersion: 1;
  nodes: Node[];
  edges: Edge[];
  variables: Variable[];
  subflows?: Record<ID, { name: string; nodes: Node[]; edges: Edge[] }>;
}
export interface Node {
  id: ID;
  type: string;
  typeVersion: number;
  name?: string;
  config: Record<string, unknown>;
  enabled: boolean;
  onError?: ErrorPolicy;
}
export type ErrorPolicy =
  | { action: "skip" } | { action: "stop" }
  | { action: "retry"; max: number; backoffSeconds: number; then: "skip" | "stop" }
  | { action: "fallback"; to: ID };
export interface Edge { id: ID; from: ID; fromPort: string; to: ID; toPort: string; label?: string }
export interface Layout {
  nodes: Record<ID, { x: number; y: number; collapsed?: boolean }>;
  groups: { id: ID; label: string; nodeIds: ID[]; collapsed: boolean }[];
  comments: { id: ID; text: string; x: number; y: number }[];
  viewport?: { x: number; y: number; zoom: number };
}
export interface Variable {
  name: string;
  type: "string" | "number" | "bool" | "enum" | "path" | "secret";
  value?: unknown;
  hasValue?: boolean;
  options?: string[];
  description?: string;
  templateParam?: boolean;
}
export interface ValidationIssue {
  code: "no_trigger" | "multiple_triggers" | "unreachable_node" | "cycle_without_loop" | "unhandled_error"
    | "unknown_node_type" | "unknown_type_version" | "missing_config" | "invalid_config" | "unknown_config_key"
    | "missing_variable" | "capability_unavailable" | "capability_unknown" | "unsafe_output_path"
    | "dangling_edge" | "unknown_port" | "port_type_mismatch"
    | "not_runnable_yet" | "unknown_schema_version" | "duplicate_id" // the last three: added in V1 (Built in V1)
    | "unconnected_port" | "port_used_twice" | "empty_check" | "branch_in_conversion" | "path_without_save" | "fallback_loop" // V6a/V6b
    | (string & {}); // a newer server's code: show its message
  severity: "error" | "warning";
  message: string;
  nodeId?: ID; edgeId?: ID; port?: string; configPath?: string; field?: string; // field: #165, the setting's key (invalid_config)
}
// GET /workflows/{id}/publish-preview (V1 also returns validation).
export interface PublishPreview {
  nextNumber: number;
  changes: { nodeId: ID; kind: "added" | "removed" | "changed"; summary: string }[];
  libraries: { id: ID; name: string }[];
  changesOriginals: boolean;
  validation?: ValidationIssue[];
}

// Node registry (GET /step-definitions). configSchema is a JSON Schema subset; V1 writes enum + "x-enumLabels",
// "title", "default" and "x-ui": {widget: "slider" | "segmented" | "folder"}; formats add "x-available".
export type JSONSchema = Record<string, unknown>;
export interface Port {
  name: "in" | "success" | "error" | "skipped" | "match" | "else" | string;
  label: string;
  kind: "file" | "control";
  outcome: "success" | "error" | "skipped" | "branch";
}
export interface StepDefinition {
  type: string;
  typeVersion: number;
  category: "input" | "filter" | "video" | "audio" | "subtitles" | "container" | "verify" | "output" | "flow";
  fileTypes: FileType[];
  name: string;
  description: string;
  icon: string;
  typeLabel: string;
  inputs: Port[];
  outputs: Port[];
  configSchema: JSONSchema;
  alwaysOn: boolean;
  requires: Capability["key"][];
  requiresByConfig?: Record<string, Record<string, string[]>>;
  runsWith?: string[];
  sideEffects: "none" | "writes_staging" | "commits";
  source: { kind: "builtin" } | { kind: "plugin"; pluginId: ID; pluginVersion: string };
  deprecated?: boolean;
  readOnly?: boolean; // migration-only types (video.legacy_settings): kept as they are, never edited (workshop #109)
}

// Enrollment (ONE-COMMAND-SETUP.md section 11; workshop 00:42Z): GET /api/v2/setup, /setup/servers, POST /setup/role
export type JoinState = "waiting" | "allowed" | "denied" | "expired" | "unreachable" | "not_local";
export interface Setup {
  role: "unset" | "server" | "worker";
  join: null | { serverName: string; serverUrl: string; state: JoinState; expiresAt: Time | null };
}
export interface FoundServer { name: string; address: string; url: string; version: string; matchesVersion: boolean }
export type RoleBody = { role: "server" | "unset" } | { role: "worker"; serverUrl: string; pairCode?: string };
