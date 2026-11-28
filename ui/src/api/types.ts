
// Response shapes for the routes in each page's "Data and API" section (Plan/pages).

// "cancelled" is a job someone stopped (POST /jobs/actions cancel); the server returns it as-is.
export type JobState = "waiting" | "running" | "done" | "failed" | "skipped" | "cancelled";

export interface Series {
  c: "ok" | "accent" | "text2" | "bad" | "trash";
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
export type WorkflowKind = "video" | "audio" | "other" | "part";
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

// DELETE /workflows/{id}: deleted, or refused while a library uses it (the page names them).
export type WorkflowDeleted = { ok: true } | { ok: false; code: "in_use"; usedBy: WorkflowUse[] };

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
  // null when no library is watched, so no scan is scheduled.
  waiting: { count: number; nextScanAt: string | null };
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

// GET /api/v2/attention (page 08). what, means and action.label are server sentences shown as they are;
// count and latestAt are values the UI formats. action is null when the row has no button; body, when present,
// is sent as JSON as it is.
export interface Attention {
  id: string;
  level: "warn" | "error";
  what: string;
  means: string;
  count: number;
  latestAt: string;
  action: { label: string; method: string; path: string; body?: unknown } | null;
  link: string | null;
}

export interface Summary {
  converting: { running: number; workers: number };
  queue: number;
  // RFC 3339 UTC times. lastDoneAt is null until a job has finished; nextScanAt is null when no library is watched.
  lastDoneAt: string | null;
  nextScanAt: string | null;
  diskFreeBytes: number;
  failed24h: number;
  workersOffline: number;
  newErrors: number;
  // The last error line the System log was opened past (0 on a fresh install). newErrors counts errors above it.
  seenSeq: number;
  // RFC 3339 time of the line seenSeq names; null while seenSeq is 0 (and on a fresh install).
  seenAt: string | null;
  healthFailing: boolean;
  // The all-workers switch. paused is true while it is off; finishing counts the files still running then (0 otherwise).
  workers: { paused: boolean; finishing: number };
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

// GET /api/v2/jobs/{id}/neighbours: the ids before and after a job in the list behind the panel.
export interface JobNeighbours {
  prev: string | null;
  next: string | null;
}

// POST /api/v2/jobs/actions (S3-6a): one action ("retry" | "cancel" | "top") for a list of jobs. "done" holds
// the ids that changed; a refused id changed nothing, and its "message" is the sentence the page shows.
export interface JobActionRefusal {
  id: string;
  code: "job_not_found" | "not_retryable" | "not_movable" | "file_busy" | "not_cancellable" | "internal";
  message: string;
}

export interface JobsActionDone {
  done: string[];
  refused: JobActionRefusal[];
}

export interface JobsParams {
  state?: JobState | "all";
  worker?: string; // a worker id; the jobs it ran or is running
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

// Workers (page 10 "Data and API"). GET /api/v2/workers returns Worker items; GET /api/v2/workers/{id} returns WorkerDetail.
export type WorkerState = "converting" | "idle" | "paused" | "offline";
// The graphics card's family, as the bitrate ladder names its card offsets.
export type GpuFamily = "qsv" | "nvenc" | "vaapi" | "amf";

export interface Worker {
  id: string;
  name: string;
  address: string; // "" for this computer
  local: boolean;
  state: WorkerState;
  since: string | null; // RFC 3339; when the state began (offline and paused show it)
  slots: number; // files at once
  gpuSlots: number;
  gpu: { name: string; family: GpuFamily } | null;
  now: { jobId: string; file: string; step: JobStepRef } | null;
  doneToday: number;
  // True while the all-workers switch is off. The worker's own state is kept, so it returns when the switch goes back on.
  pausedGlobally: boolean;
}

export interface WorkerLibrary {
  libraryId: string;
  name: string;
  serverPath: string;
  workerPath: string;
  mode: "read" | "read_write";
  test: { state: "untested" | "pending" | "ok" | "failed"; at?: string; message?: string };
}

// One encoder the worker has. Only the ones whose test passed are offered as a choice.
export interface WorkerEncoder {
  name: string; // e.g. "hevc_qsv"
  label: string;
  testOk: boolean;
}

export interface WorkerDetail extends Worker {
  hours: { from: string; to: string } | null; // "HH:MM" in the server's local time; null = any time. from > to runs overnight.
  primaryEncoder: string | null; // null = automatic; "cpu" or an encoder name
  encoders: WorkerEncoder[];
  // Remote workers only: the libraries it takes. This computer takes every library and sends none.
  libraries: WorkerLibrary[];
  // Libraries added since this worker's list was last saved and not on it. The next PATCH {libraries} clears it.
  newLibraries: { libraryId: string; name: string }[];
}

export interface Pairing {
  id: string;
  code: string;
  expiresAt: string; // RFC 3339
  command: string; // the line to run on the other machine
  plainHTTP: boolean; // the server is reached without TLS
}

// A worker asking to join after its command ran. Allow or Deny answers it.
export interface JoinRequest {
  id: string;
  name: string;
  address: string;
}

// ---- Settings (page 11). Shapes are the ones in pages/11-settings.md "Data and API".

export type SettingsSection = "general" | "appearance" | "processing" | "trash" | "connections" | "notifications" | "backup" | "health" | "access";

// First run and sign-in shapes (the server's goldens, workshop 4a59822c).
export type Setup = { needsAccount: boolean; starting: boolean };
export type SetupDone = { scanStarted: boolean };
export type Health = { ok: boolean; starting: boolean; version?: string };

// The first run's own reads: a starting workflow (page 05), the answers the guided page begins from,
// and the languages the app offers. ScanAllDone is POST /libraries/scan's answer; LibraryRemoved is
// DELETE /libraries/{id}'s, named for what it says so the two do not borrow each other's name.
export type Starter = { id: string; kind: string; name: string; line: string; icon: string; guided: boolean };
export type StarterDetail = Starter & { answers: Record<string, unknown> };
export type Language = { code: string; name: string };
export type ScanAllDone = { ok: true };
export type LibraryRemoved = { ok: true };

// Settings > Trash: an original kept after a conversion.
export interface TrashItem {
  id: string;
  file: string;
  library: string;
  trashedAt: string;
  daysLeft: number;
}

// One setting for the install, applied in every browser that signs in. accent null = the default green.
export interface AppearanceSettings {
  theme: "system" | "light" | "dark";
  accent: string | null;
}

export interface UpdateAvailable {
  version: string;
  notesUrl: string;
  checkedAt: string;
}

export interface GeneralSettings {
  serverName: string;
  language: "en";
  timeFormat: "24h" | "12h";
  dateFormat: "YYYY-MM-DD" | "DD.MM.YYYY" | "MM/DD/YYYY";
  checkForUpdates: boolean;
  // Read-only. The running version, and the newer release when one is found (null = up to date, never checked, or checks off).
  version: string;
  updateAvailable: UpdateAvailable | null;
  // My languages: three-letter codes, kept for the workflows that keep only some tracks.
  languages: string[];
}

export interface ProcessingSettings {
  parallelJobs: number;
  gpuJobsSeparate: boolean;
  scanSchedule: string;
  queueOrder: "newest" | "oldest" | "largest" | "smallest";
  lowPriority: boolean;
  keepOriginalsDays: number;
  lowSpaceGB: number;
  workflowsMayRunCommands: boolean;
  trashPath: string;
}

export interface LogSettings {
  jobDays: number;
  systemDays: number;
  rawDays: number;
}

export interface SettingsData {
  general: GeneralSettings;
  appearance: AppearanceSettings;
  processing: ProcessingSettings;
  logs: LogSettings;
  backupKeep: number;
}

// PATCH sends only the changed fields, nested by group.
export type SettingsPatch = {
  general?: Partial<Omit<GeneralSettings, "updateAvailable" | "version">>;
  appearance?: Partial<AppearanceSettings>;
  processing?: Partial<Omit<ProcessingSettings, "trashPath">>;
  logs?: Partial<LogSettings>;
  backupKeep?: number;
};

export type IntegrationKind = "sonarr" | "radarr" | "plex" | "jellyfin";

export interface PathMapping {
  theirPath: string;
  ourPath: string;
}

export interface LastResult {
  ok: boolean;
  message: string;
  at: string;
}

export interface Integration {
  id: string;
  kind: IntegrationKind;
  name: string;
  url: string;
  hasKey: boolean;
  pathMappings: PathMapping[];
  enabled: boolean;
  lastResult: LastResult | null;
  failingSince: string | null;
}

export interface IntegrationInput {
  kind: IntegrationKind;
  name?: string;
  url: string;
  apiKey?: string;
  pathMappings?: PathMapping[];
  enabled?: boolean;
}

export interface IntegrationTest {
  ok: boolean;
  message: string;
  version: string | null;
  folders: { theirPath: string; ourPath: string | null; inLibrary: boolean }[];
}

export type NotifyMode = "off" | "drained" | "daily" | "weekly";

export interface NotifyEvent {
  code: string;
  name: string;
}

export interface NotifyTarget {
  id: string;
  name: string;
  kind: "webhook" | "discord";
  urlShown: string;
  enabled: boolean;
  events: string[];
  lastResult: LastResult | null;
  failingSince: string | null;
}

export interface Notifications {
  targets: NotifyTarget[];
  summary: { mode: NotifyMode; at: string };
  linkBase: string | null;
}

export interface BackupItem {
  id: string;
  name: string;
  createdAt: string;
  reason: "manual" | "before_update" | "before_migration" | "before_import";
  appVersion: string;
  sizeBytes: number;
}

export interface BackupPreview {
  previewId: string;
  createdAt: string;
  appVersion: string;
  contents: { settings: boolean; workflows: number; libraries: number; shares: number; connections: number; jobs: number };
  warnings: string[];
}

export type CheckStatus = "ok" | "warn" | "bad" | "absent";

export interface HealthCheck {
  id: string;
  label: string;
  status: CheckStatus;
  detail: string;
  hint: string | null;
  fix: { label: string; method: "POST"; path: string } | null;
}

export interface Doctor {
  ranAt: string;
  checks: HealthCheck[];
}

// System log (page 12 "Data and API"). Items are the app-wide events of system/logging.md §3, with the kinds of §6.1.
export type LogLevel = "debug" | "info" | "warn" | "error";
export type LogKind =
  | "run_start" | "run_end" | "step_start" | "step_end" | "decision" | "command" | "probe" | "progress"
  | "size" | "move" | "warning" | "error" | "note"
  | "queued" | "picked" | "retry" | "requeued" | "cancelled" | "notified" | "original";

export interface LogEvent {
  seq: number;
  // RFC 3339 UTC, with milliseconds.
  ts: string;
  level: LogLevel;
  kind: LogKind;
  // Where it came from: Scanner, a worker by name, Web, Updates, Notifications, Health check.
  source: string;
  message: string;
  jobId: string | null;
  libraryId: string | null;
  workerId: string | null;
  // On an error or a failed run: what happens now or what the person can do.
  nextAction?: string;
  // Repeats folded by the server; count is every time this line happened.
  repeat?: { count: number; firstAt: string; lastAt: string };
}

export interface LogCounts {
  info: number;
  warn: number;
  error: number;
}

export interface LogPage {
  // Oldest first; the newest line is last.
  items: LogEvent[];
  counts: LogCounts;
}

export interface LogParams {
  level?: "info" | "warn" | "error";
  source?: string;
  library?: string;
  worker?: string;
  // RFC 3339 UTC.
  from?: string;
  to?: string;
  q?: string;
  // Returns the lines before this seq.
  before?: number;
  limit?: number;
  // Shows debug lines.
  detail?: boolean;
  // Only lines with a seq above this.
  since?: number;
}

// The body of any refusal (4xx/5xx): http.ts turns it into an ApiError.
export type ErrorReply = { error: { code: string; message: string; field?: string } };
