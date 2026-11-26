import type { IconName } from "../ui/icon-paths.ts";

// Response shapes for the routes in each page's "Data and API" section (Plan/pages).

export type JobState = "waiting" | "running" | "done" | "failed" | "skipped";

export interface Series {
  c: "ok" | "accent" | "text2" | "bad";
  v: (number | null)[];
}

// Library row (page 09 "Data and API"). workflow is null until one is picked: status then is needs_workflow.
export type LibraryStatus = "needs_workflow" | "watching" | "scanning" | "paused" | "share_down";

export interface WorkflowRef {
  id: string;
  name: string;
}

export interface Library {
  id: string;
  name: string;
  path: string;
  shareId?: string;
  workflow: WorkflowRef | null;
  files: number;
  waiting: number;
  savedBytes: number;
  savedPct: number;
  // RFC 3339 UTC time of the last scan.
  lastScan: { at: string; found: number; new: number; changed: number } | null;
  status: LibraryStatus;
  watch: Watch;
  paused: boolean;
  // ISO time of the next scan; null while paused.
  nextScanAt: string | null;
  scanPct?: number;
  // RFC 3339 UTC time the share dropped.
  shareDownAt?: string;
}

// Watch for new files, per library. There is no "off": pause is `paused`.
export type Watch = "hourly" | "daily" | "weekly";

export interface LibraryDetail extends Library {
  watch: Watch;
  // Read-only, optional to show. Worker paths live on the Workers page.
  workers?: { id: string; name: string }[];
}

// Page 04 "Data and API": one row per workflow on the list. Only the video kind is built so far (decisions.md:57).
export type WorkflowKind = "video" | "audio" | "other" | "failure" | "part";
export type WorkflowState = "published" | "draft" | "problems";

// Who uses a workflow: a library today, and a workflow once workflows can nest (page 04 "Data and API").
export interface WorkflowUse {
  type: "library" | "workflow";
  id: string;
  name: string;
}

export interface WorkflowRow {
  id: string;
  name: string;
  description: string;
  kind: WorkflowKind;
  usedBy: WorkflowUse[];
  // RFC 3339 UTC time of the last change.
  updatedAt: string;
  state: WorkflowState;
  problemCount: number;
  madeWith: "guided" | "editor";
  fitsGuided: boolean;
}

export interface WorkflowSummary {
  id: string;
  name: string;
  summary: { converts: string; leavesAlone: string; original: string };
}

export interface Preview {
  wouldConvert: number;
  total: number;
  skipped: { reason: string; count: number }[];
}

export interface FolderListing {
  path: string;
  parent: string | null;
  folders: { name: string; path: string }[];
}

export interface FolderCheck {
  videos: number;
  sizeBytes: number;
  problems: { code: string; message: string }[];
}

export interface LibraryInput {
  name: string;
  path?: string;
  shareId?: string;
  // Defaults to "daily" when omitted.
  watch?: Watch;
}

// Changed fields only.
export interface LibraryPatch {
  workflowId?: string | null;
  name?: string;
  path?: string;
  watch?: Watch;
  paused?: boolean;
}

// GET /api/v2/overview, in pages/08 "Data and API" nesting. Sizes are whole bytes; times are RFC 3339 UTC.
export interface Overview {
  freed: {
    totalBytes: number;
    todayBytes: number;
    // Space that frees up when originals leave the trash, and the day it happens. Both null when the trash is empty.
    nextReleaseBytes: number | null;
    nextReleaseAt: string | null;
    // The saving still waiting in the trash now.
    inTrashBytes: number;
  };
  converted: { total: number; perDay: number };
  waiting: { count: number; nextScanAt: string };
  // pct is a percent: 0.6 means 0.6 %.
  failed24h: { count: number; pct: number };
  avgShrink: { pct: number; secPerGB: number };
  series: { days: DaySeries[] };
  // The jobs converting now, in the same shape as the Jobs list.
  running: Job[];
  workersOn: boolean;
  last24h: { done: number; skipped: number; failed: number };
  libraries: Library[];
}

// One day of the charts. Freed and in-trash are bytes; the rest are job counts. null = no data that day.
export interface DaySeries {
  day: string; // local calendar day, YYYY-MM-DD
  freedBytes: number | null;
  trashBytes: number | null;
  done: number | null;
  skipped: number | null;
  failed: number | null;
}

export interface Attention {
  id: string;
  tile: { icon: IconName; tone: "bad" | "warn" | "" } | { lines: string[] };
  title: string;
  titleHref?: string;
  chips?: { icon?: IconName; label: string }[];
  hint?: { text: string; link?: { label: string; href?: string; action?: "test-encoders" } };
  side: { text?: { label: string; tone: "bad" | "" }; pill: { label: string; tone: "bad" | "warn" | "ok" | ""; icon?: IconName } };
}

export interface Summary {
  converting: { running: number; workers: number };
  queue: number;
  // RFC 3339 UTC times.
  lastDoneAt: string;
  nextScanAt: string;
  diskFreeBytes: number;
  failed24h: number;
  workersOffline: number;
  newErrors: number;
  healthFailing: boolean;
}

export interface Job {
  id: string;
  state: JobState;
  file: string;
  library: string;
  workflow: string;
  worker: string;
  // The latest step the job reached (page 02 "step"); null while waiting or when skipped.
  step: JobStepRef | null;
  // The latest progress event; null unless running.
  progress: JobProgress | null;
  // RFC 3339 UTC times; null until the step happens. Durations are whole milliseconds.
  queuedAt: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  tookMs: number | null;
  result: JobResult;
  // Converting-now card (dashboard) only.
  tileLines?: string[];
}

export interface JobStepRef {
  index: number;
  count: number;
  name: string;
}

export interface JobProgress {
  pct: number;
  // Page 02 says "speed" with no unit; the mock uses bytes per second.
  speed: number | null;
  // Milliseconds left; null while unknown.
  etaMs: number | null;
}

export interface JobResult {
  // Bytes. While a job runs, sizeAfter is the estimate the saving text is built from.
  sizeBefore: number;
  sizeAfter: number | null;
  outcome: "smaller" | "bigger_kept" | "failed" | null;
  failedStep: string | null;
  reason: string | null;
  warnings: number;
}

export interface JobCounts {
  all: number;
  waiting: number;
  running: number;
  failed: number;
  done: number;
  skipped: number;
}

export interface JobsPage {
  counts: JobCounts;
  items: Job[];
  next: string | null;
}

export interface JobsParams {
  state?: JobState | "all";
  limit?: number;
  cursor?: string;
}

// Job details (page 03 "Data and API"). GET /jobs/{id} fills the head, Summary and Steps; the rest are their own calls.
export type StepOutcome = "done" | "nothing" | "failed" | "running" | "skipped" | "waiting";

export interface JobStep {
  index: number;
  name: string;
  outcome: StepOutcome;
  durationMs: number | null;
}

export interface LogLine {
  // RFC 3339 UTC, with milliseconds.
  time: string;
  level: "info" | "warn" | "error" | "detail";
  text: string;
}

// A setting or measurement a step used, shown as a row under the step's decision.
export interface LogValue {
  name: string;
  value: string;
  from: string;
}

export interface LogStep extends JobStep {
  decision: string;
  command: string | null;
  values: LogValue[];
  lines: LogLine[];
}

export interface MediaRow {
  group: "General" | "Video" | "Audio" | "Subtitles";
  field: string;
  // "duration" values are milliseconds and "bytes" values are whole bytes; the screen formats them.
  kind?: "text" | "duration" | "bytes";
  before: string | number;
  after: string | number | null;
  changed: boolean;
}

export interface HistoryLine {
  // RFC 3339 UTC.
  time: string;
  level: "info" | "warn" | "error";
  text: string;
}

// The Summary's "Original" line (pages/03). until is RFC 3339, set only for "trashed", otherwise null.
export interface JobOriginal {
  action: "not_yet" | "not_touched" | "trashed" | "deleted" | "restored";
  until: string | null;
}

export interface JobDetail {
  job: Job;
  path: string;
  workflow: string;
  worker: string;
  attempt: number;
  // RFC 3339 UTC; null for a job that never started.
  startedAt: string | null;
  tookMs: number | null;
  sizeBeforeBytes: number;
  sizeAfterBytes: number | null;
  finishedAt: string | null;
  writtenAt: string | null;
  // Where the new file went; null until one is written.
  outputPath: string | null;
  // Shown on the head while a job runs; milliseconds left.
  timeLeftMs: number | null;
  original: JobOriginal;
  failure: { step: string; reason: string; nextAction: string; actionLabel: string } | null;
  skipReason: string | null;
  steps: JobStep[];
  // null once the detailed log has expired (7/30/90 days).
  logs: LogStep[] | null;
  media: MediaRow[];
  history: HistoryLine[];
}
