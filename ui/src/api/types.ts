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
  lastScan: { at: string; found: number; new: number; changed: number } | null;
  status: LibraryStatus;
  watch: Watch;
  paused: boolean;
  // ISO time of the next scan; null while paused.
  nextScanAt: string | null;
  scanPct?: number;
  shareDownAt?: string;
}

// Watch for new files, per library. There is no "off": pause is `paused`.
export type Watch = "hourly" | "daily" | "weekly";

export interface LibraryDetail extends Library {
  watch: Watch;
  // Read-only, optional to show. Worker paths live on the Workers page.
  workers?: { id: string; name: string }[];
}

export interface Workflow {
  id: string;
  name: string;
  line: string;
  usedBy: { libraryId: string; name: string }[];
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

export interface Overview {
  freedGB: number;
  freedTodayGB: number;
  converted: number;
  convertedPerDay: number;
  waiting: number;
  nextScan: string;
  failed24h: number;
  failedRatePct: number;
  avgShrinkPct: number;
  convertSecPerGB: number;
  running: number;
  workersOn: boolean;
  trashGB: number;
  savedMoreOn16Nov: number;
  savedSeries: Series[];
  filesSeries: Series[];
  filesLast24h: { done: number; skipped: number; failed: number };
  libraries: Library[];
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
  step: string;
  progress: number | null;
  when: string;
  result: string;
  resultTone: "ok" | "bad" | "";
  // Converting-now card (dashboard) only.
  tileLines?: string[];
  hint?: string;
  started?: string;
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
  before: string;
  after: string | null;
  changed: boolean;
}

export interface HistoryLine {
  time: string;
  level: "info" | "warn" | "error";
  text: string;
}

export interface JobDetail {
  job: Job;
  path: string;
  workflow: string;
  worker: string;
  attempt: number;
  startedAt: string | null;
  took: string | null;
  sizeBefore: string;
  sizeAfter: string | null;
  sizeBeforeBytes: number;
  sizeAfterBytes: number | null;
  finishedAt: string | null;
  writtenAt: string | null;
  // Where the new file went; null until one is written.
  outputPath: string | null;
  // Shown on the head while a job runs.
  timeLeft: string | null;
  original: string;
  failure: { step: string; reason: string; nextAction: string; actionLabel: string } | null;
  skipReason: string | null;
  steps: JobStep[];
  // null once the detailed log has expired (7/30/90 days).
  logs: LogStep[] | null;
  media: MediaRow[];
  history: HistoryLine[];
}
