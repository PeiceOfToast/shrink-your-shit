// Settings for the mock (pages/11-settings.md "Data and API"). Writes change in-memory copies, so a session behaves like the app.
import type {
  BackupItem,
  BackupPreview,
  Doctor,
  Integration,
  IntegrationInput,
  IntegrationTest,
  Notifications,
  NotifyEvent,
  NotifyTarget,
  SettingsData,
  SettingsPatch,
  WorkflowVariable,
} from "./types.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, noLibrariesYet, respond } from "./mockState.ts";

const clone = <T>(v: T): T => structuredClone(v);
const hoursAgo = (h: number) => new Date(Date.parse("2026-11-26T23:00:00Z") - h * 3600_000).toISOString();

// The allowed values for each log setting. systemDays is the open point: confirm with orch-lead before changing.
export const LOG_CHOICES = {
  jobDays: [0, 7, 30, 90],
  systemDays: [0, 7, 14, 30, 90],
  rawDays: [0, 7, 30, 90],
} as const;

// ---- General, Processing and logs

let settings: SettingsData = {
  general: {
    serverName: "shrink-server",
    language: "en",
    timeFormat: "24h",
    dateFormat: "YYYY-MM-DD",
    theme: "system",
    checkForUpdates: true,
    updateAvailable: null,
  },
  processing: {
    parallelJobs: 1,
    gpuJobsSeparate: true,
    scanSchedule: "02:00",
    queueOrder: "newest",
    lowPriority: true,
    keepOriginalsDays: 7,
    lowSpaceGB: 10,
    languages: ["eng", "ger"],
    workflowsMayRunCommands: false,
    trashPath: "/media/.syp-trash",
  },
  logs: { jobDays: 30, systemDays: 14, rawDays: 7 },
  backupKeep: 10,
};

const KNOWN: Record<string, string[]> = {
  general: ["serverName", "language", "timeFormat", "dateFormat", "theme", "checkForUpdates"],
  processing: ["parallelJobs", "gpuJobsSeparate", "scanSchedule", "queueOrder", "lowPriority", "keepOriginalsDays", "lowSpaceGB", "languages", "workflowsMayRunCommands"],
  logs: ["jobDays", "systemDays", "rawDays"],
};

const bad = (field: string, message: string) => new ApiError("invalid_setting", message, field);
const inRange = (v: unknown, min: number, max: number) => typeof v === "number" && Number.isInteger(v) && v >= min && v <= max;

// Checks every field in the patch before anything is saved: all or nothing. One problem at a time.
function validate(next: SettingsData) {
  const g = next.general;
  if (!g.serverName.trim()) throw bad("general.serverName", "Give the server a name.");
  const p = next.processing;
  if (!inRange(p.parallelJobs, 1, 8)) throw bad("processing.parallelJobs", "Pick from 1 to 8 files at once.");
  if (p.scanSchedule !== "" && !/^([01]\d|2[0-3]):[0-5]\d$/.test(p.scanSchedule)) throw bad("processing.scanSchedule", "Use a time like 02:00, or leave it empty for no daily scan.");
  if (!inRange(p.keepOriginalsDays, 0, 365)) throw bad("processing.keepOriginalsDays", "Keep originals from 0 to 365 days.");
  if (!inRange(p.lowSpaceGB, 1, 1000)) throw bad("processing.lowSpaceGB", "Pick a limit from 1 to 1000 GB.");
  if (!p.languages.every((l) => /^[a-z]{3}$/.test(l))) throw bad("processing.languages", "Use three-letter codes like eng, ger, fra.");
  const l = next.logs;
  if (!(LOG_CHOICES.jobDays as readonly number[]).includes(l.jobDays)) throw bad("logs.jobDays", "Pick 7, 30, 90 days or forever.");
  if (!(LOG_CHOICES.systemDays as readonly number[]).includes(l.systemDays)) throw bad("logs.systemDays", "Pick a number of days from the list.");
  if (!(LOG_CHOICES.rawDays as readonly number[]).includes(l.rawDays)) throw bad("logs.rawDays", "Pick 7, 30, 90 days or forever.");
  // 0 means forever, so it is longer than any number of days.
  const rawLonger = l.jobDays !== 0 && (l.rawDays === 0 || l.rawDays > l.jobDays);
  if (rawLonger) throw bad("logs.rawDays", "Raw tool output can't be kept longer than the job log it belongs to.");
  if (!inRange(next.backupKeep, 1, 50)) throw bad("backupKeep", "Keep from 1 to 50 backups.");
}

export function getSettings(): Promise<SettingsData> {
  return respond(() => clone(settings));
}

export function patchSettings(patch: SettingsPatch): Promise<SettingsData> {
  return respond(() => {
    const next = clone(settings);
    const target = next as unknown as Record<string, Record<string, unknown>>;
    for (const [group, values] of Object.entries(patch)) {
      if (values === undefined) continue;
      if (group === "backupKeep") {
        next.backupKeep = values as number;
        continue;
      }
      const allowed = KNOWN[group];
      if (!allowed) throw new ApiError("unknown_setting", "That setting doesn't exist.");
      for (const [key, value] of Object.entries(values as Record<string, unknown>)) {
        if (!allowed.includes(key)) throw new ApiError("unknown_setting", `That setting doesn't exist: ${group}.${key}.`);
        target[group][key] = value;
      }
    }
    validate(next);
    settings = next;
    return clone(settings);
  });
}

// ---- Connections (media servers and downloaders)

let integrations: Integration[] = [
  {
    id: "sonarr",
    kind: "sonarr",
    name: "Sonarr",
    url: "http://10.10.2.20:8989",
    hasKey: true,
    pathMappings: [{ theirPath: "/tv", ourPath: "/media/tv" }],
    enabled: true,
    lastResult: { ok: true, message: "Connected. Sonarr 4.0.2.", at: hoursAgo(1) },
    failingSince: null,
  },
  {
    id: "plex",
    kind: "plex",
    name: "Plex",
    url: "http://10.10.2.21:32400",
    hasKey: true,
    pathMappings: [],
    enabled: true,
    lastResult: { ok: false, message: "Didn't answer. Is Plex running?", at: hoursAgo(5) },
    failingSince: hoursAgo(5),
  },
];

const KINDS = ["sonarr", "radarr", "plex", "jellyfin"];
const URL_RE = /^https?:\/\/[^\s/]+/;

function checkIntegration(input: { kind?: string; url?: string; apiKey?: string; pathMappings?: { theirPath: string; ourPath: string }[] }, needKey: boolean) {
  if (input.kind !== undefined && !KINDS.includes(input.kind)) throw new ApiError("invalid_kind", "Pick Sonarr, Radarr, Plex or Jellyfin.", "kind");
  if (input.url !== undefined && !URL_RE.test(input.url)) throw new ApiError("invalid_url", "Start the address with http:// or https://.", "url");
  if (needKey && !input.apiKey) throw new ApiError("missing_key", "Paste the API key from that app.", "apiKey");
  for (const m of input.pathMappings ?? []) {
    if (!m.theirPath.trim() || !m.ourPath.trim()) throw new ApiError("invalid_mapping", "Fill in both paths for each mapping.", "pathMappings");
  }
}

export function getIntegrations(): Promise<Integration[]> {
  return respond(() => clone(integrations));
}

export function createIntegration(input: IntegrationInput): Promise<Integration> {
  return respond(() => {
    checkIntegration(input, true);
    const item: Integration = {
      id: `int-${integrations.length + 1}-${Date.now().toString(36)}`,
      kind: input.kind,
      name: input.name?.trim() || input.kind[0].toUpperCase() + input.kind.slice(1),
      url: input.url,
      hasKey: true,
      pathMappings: input.pathMappings ?? [],
      enabled: input.enabled ?? true,
      lastResult: null,
      failingSince: null,
    };
    integrations = [...integrations, item];
    return clone(item);
  });
}

export function patchIntegration(id: string, patch: Partial<IntegrationInput> & { name?: string; enabled?: boolean }): Promise<Integration> {
  return respond(() => {
    const cur = integrations.find((i) => i.id === id);
    if (!cur) throw new ApiError("not_found", "That connection doesn't exist any more.");
    checkIntegration(patch, false);
    const next: Integration = {
      ...cur,
      ...(patch.name !== undefined ? { name: patch.name } : {}),
      ...(patch.url !== undefined ? { url: patch.url } : {}),
      ...(patch.apiKey ? { hasKey: true } : {}),
      ...(patch.pathMappings !== undefined ? { pathMappings: patch.pathMappings } : {}),
      ...(patch.enabled !== undefined ? { enabled: patch.enabled } : {}),
    };
    integrations = integrations.map((i) => (i.id === id ? next : i));
    return clone(next);
  });
}

export function deleteIntegration(id: string): Promise<void> {
  return respond(() => {
    if (!integrations.some((i) => i.id === id)) throw new ApiError("not_found", "That connection doesn't exist any more.");
    integrations = integrations.filter((i) => i.id !== id);
  });
}

// Test sends the saved key for a saved id, or the typed values for a new one.
export function testIntegration(input: { id?: string; kind?: string; url?: string; apiKey?: string; pathMappings?: { theirPath: string; ourPath: string }[] }): Promise<IntegrationTest> {
  return respond(() => {
    const cur = input.id ? integrations.find((i) => i.id === input.id) : undefined;
    const url = input.url ?? cur?.url ?? "";
    if (!URL_RE.test(url)) throw new ApiError("invalid_url", "Start the address with http:// or https://.", "url");
    if (url.includes("down")) {
      return { ok: false, message: "Didn't answer. Check the address, and that the app is running.", version: null, folders: [] };
    }
    const folders = [
      { theirPath: "/tv", ourPath: "/media/tv", inLibrary: true },
      { theirPath: "/tv-4k", ourPath: null, inLibrary: false },
    ];
    return { ok: true, message: "Connected. Sonarr 4.0.2.", version: "4.0.2", folders };
  });
}

// ---- Notifications

export const NOTIFY_EVENTS: NotifyEvent[] = [
  { code: "job.failed", name: "A job failed" },
  { code: "job.warnings", name: "A job finished with warnings" },
  { code: "job.done", name: "A job finished" },
  { code: "batch.done", name: "A batch finished" },
  { code: "worker.offline", name: "A worker went offline" },
  { code: "encoder.fell_back", name: "A file fell back to the processor" },
  { code: "share.down", name: "A share dropped" },
  { code: "health.failed", name: "A health check failed" },
];

let notifications: Notifications = {
  targets: [
    {
      id: "discord-media",
      name: "Discord, #media",
      kind: "discord",
      urlShown: "https://discord.com/api/webhooks/…4f2a",
      enabled: true,
      events: ["job.failed", "job.warnings", "health.failed"],
      lastResult: { ok: true, message: "Message delivered.", at: hoursAgo(2) },
      failingSince: null,
    },
    {
      id: "home-hook",
      name: "Home webhook",
      kind: "webhook",
      urlShown: "http://10.10.2.9:8123/hook/…",
      enabled: true,
      events: ["worker.offline", "share.down"],
      lastResult: { ok: false, message: "Didn't answer. Check the address.", at: hoursAgo(9) },
      failingSince: hoursAgo(9),
    },
  ],
  summary: { mode: "daily", at: "08:00" },
  linkBase: "http://10.10.2.201:8484",
};

export function getNotifications(): Promise<Notifications> {
  return respond(() => clone(notifications));
}

export function patchNotifications(patch: { summary?: Partial<Notifications["summary"]>; linkBase?: string | null }): Promise<Notifications> {
  return respond(() => {
    const summary = { ...notifications.summary, ...patch.summary };
    if (summary.mode !== "off" && !/^([01]\d|2[0-3]):[0-5]\d$/.test(summary.at)) {
      throw new ApiError("invalid_setting", "Use a time like 08:00.", "summary.at");
    }
    notifications = { ...notifications, summary, linkBase: patch.linkBase !== undefined ? patch.linkBase : notifications.linkBase };
    return clone(notifications);
  });
}

export function getNotifyEvents(): Promise<NotifyEvent[]> {
  return respond(() => clone(NOTIFY_EVENTS));
}

export function createTarget(input: { name?: string; kind: "webhook" | "discord"; url: string; enabled?: boolean; events?: string[] }): Promise<NotifyTarget> {
  return respond(() => {
    if (input.kind !== "webhook" && input.kind !== "discord") throw new ApiError("invalid_kind", "Pick a webhook or Discord.", "kind");
    if (!URL_RE.test(input.url)) throw new ApiError("invalid_url", "Start the address with http:// or https://.", "url");
    const t: NotifyTarget = {
      id: `t-${notifications.targets.length + 1}-${Date.now().toString(36)}`,
      name: input.name?.trim() || (input.kind === "discord" ? "Discord" : "Webhook"),
      kind: input.kind,
      urlShown: input.url.replace(/^(https?:\/\/[^/]+).*$/, "$1/…"),
      enabled: input.enabled ?? true,
      events: input.events ?? [],
      lastResult: null,
      failingSince: null,
    };
    notifications = { ...notifications, targets: [...notifications.targets, t] };
    return clone(t);
  });
}

export function patchTarget(id: string, patch: { name?: string; enabled?: boolean; events?: string[] }): Promise<NotifyTarget> {
  return respond(() => {
    const cur = notifications.targets.find((t) => t.id === id);
    if (!cur) throw new ApiError("not_found", "That target doesn't exist any more.");
    const next = { ...cur, ...patch };
    notifications = { ...notifications, targets: notifications.targets.map((t) => (t.id === id ? next : t)) };
    return clone(next);
  });
}

export function deleteTarget(id: string): Promise<void> {
  return respond(() => {
    if (!notifications.targets.some((t) => t.id === id)) throw new ApiError("not_found", "That target doesn't exist any more.");
    notifications = { ...notifications, targets: notifications.targets.filter((t) => t.id !== id) };
  });
}

export function testTarget(input: { id?: string; kind?: string; url?: string }): Promise<{ ok: boolean; message: string }> {
  return respond(() => {
    const saved = input.id ? notifications.targets.find((t) => t.id === input.id) : undefined;
    if (input.id && !saved) throw new ApiError("not_found", "That target doesn't exist any more.");
    if (saved) {
      return saved.failingSince ? { ok: false, message: "Didn't answer. Check the address." } : { ok: true, message: "Message delivered." };
    }
    if (input.url !== undefined && !URL_RE.test(input.url)) throw new ApiError("invalid_url", "Start the address with http:// or https://.", "url");
    return input.url?.includes("down") ? { ok: false, message: "Didn't answer. Check the address." } : { ok: true, message: "Message delivered." };
  });
}

// ---- Variables

let variables: WorkflowVariable[] = [
  { name: "my_languages", type: "string", value: "eng, ger", description: "The languages to keep", usedBy: [{ type: "workflow", id: "keep-surround", name: "Keep surround" }] },
  { name: "max_height", type: "number", value: 1080, description: "The tallest picture height", usedBy: [{ type: "workflow", id: "shrink-videos", name: "Shrink videos" }, { type: "library", id: "movies", name: "Movies" }] },
  { name: "quality", type: "enum", value: "balanced", options: ["smaller", "balanced", "sharper"], usedBy: [] },
  { name: "scratch_folder", type: "path", value: "/media/scratch", usedBy: [{ type: "workflow", id: "shrink-old-tv", name: "Shrink old TV" }] },
  { name: "keep_subtitles", type: "boolean", value: true, usedBy: [] },
];

const NAME_RE = /^[a-z][a-z0-9_]*$/;

export function getVariables(): Promise<{ items: WorkflowVariable[]; total: number }> {
  return respond(() => ({ items: clone(variables), total: variables.length }));
}

// PUT replaces the whole list. usedBy is kept for names that stay.
export function putVariables(input: WorkflowVariable[]): Promise<{ variables: WorkflowVariable[]; total: number; saved: boolean; affects: { workflows: string[]; libraries: number } }> {
  return respond(() => {
    const issues: { code: string; message: string }[] = [];
    const seen = new Set<string>();
    for (const v of input) {
      if (!NAME_RE.test(v.name)) issues.push({ code: "invalid_variable_name", message: `"${v.name}" isn't a name. Use lowercase letters, numbers and _, starting with a letter.` });
      else if (seen.has(v.name)) issues.push({ code: "duplicate_variable", message: `There are two variables called "${v.name}".` });
      seen.add(v.name);
    }
    if (issues.length) {
      throw Object.assign(new ApiError(issues[0].code, issues[0].message), { issues });
    }
    const usage = new Map(variables.map((v) => [v.name, v.usedBy]));
    variables = input.map((v) => ({ ...v, usedBy: usage.get(v.name) ?? [] }));
    return { variables: clone(variables), total: variables.length, saved: true, affects: { workflows: ["keep-surround"], libraries: 1 } };
  });
}

// ---- Backup

let backups: BackupItem[] = [
  { id: "bk-1126-2300", name: "Backup 26 Nov, 23:00", createdAt: hoursAgo(0), reason: "manual", appVersion: "0.9.4", sizeBytes: 412_000 },
  { id: "bk-1120-0900", name: "Backup 20 Nov, 09:00", createdAt: "2026-11-20T09:00:00Z", reason: "before_update", appVersion: "0.9.3", sizeBytes: 398_000 },
  { id: "bk-1112-1500", name: "Backup 12 Nov, 15:00", createdAt: "2026-11-12T15:00:00Z", reason: "manual", appVersion: "0.9.2", sizeBytes: 371_000 },
];

const previews = new Map<string, BackupPreview>();

export function getBackups(): Promise<{ backups: BackupItem[]; keep: number }> {
  return respond(() => ({ backups: clone(backups), keep: settings.backupKeep }));
}

export function createBackup(): Promise<BackupItem> {
  return respond(() => {
    const b: BackupItem = { id: `bk-${Date.now().toString(36)}`, name: "Backup 26 Nov, 23:30", createdAt: "2026-11-26T23:30:00Z", reason: "manual", appVersion: "0.9.4", sizeBytes: 412_500 };
    backups = [b, ...backups];
    return clone(b);
  });
}

export function deleteBackup(id: string): Promise<void> {
  return respond(() => {
    backups = backups.filter((b) => b.id !== id);
  });
}

// The file the browser saves. The mock builds a small JSON file.
export function downloadBackup(id: string): Promise<Blob> {
  return respond(() => {
    const b = backups.find((x) => x.id === id);
    if (!b) throw new ApiError("not_found", "That backup doesn't exist any more.");
    return new Blob([JSON.stringify({ backup: b.id, settings, note: "mock backup" }, null, 2)], { type: "application/json" });
  });
}

// POST /preview with a file (the mock reads its name only) or {backupId}. Kept for 15 minutes in the real app.
export function previewBackup(input: { backupId?: string; file?: File }): Promise<BackupPreview> {
  return respond(() => {
    const source = input.backupId ? backups.find((b) => b.id === input.backupId) : undefined;
    if (input.file && !input.file.name.endsWith(".json")) throw new ApiError("not_a_backup", "That file isn't a backup from this app.");
    const createdAt = source?.createdAt ?? "2026-11-20T09:00:00Z";
    const p: BackupPreview = {
      previewId: `pv-${Date.now().toString(36)}`,
      createdAt,
      appVersion: source?.appVersion ?? "0.9.3",
      contents: { settings: true, workflows: 4, libraries: 3, shares: 1, connections: 2, jobs: 1440 },
      warnings: [],
    };
    previews.set(p.previewId, p);
    return clone(p);
  });
}

export function applyBackup(previewId: string): Promise<{ restored: BackupPreview["contents"]; safetyBackup: BackupItem }> {
  return respond(() => {
    const p = previews.get(previewId);
    if (!p) throw new ApiError("preview_expired", "That backup check has run out. Pick the backup again.");
    if (getForcedState() === "running") throw new ApiError("jobs_running", "Jobs are running. Wait for them to finish, then restore.");
    previews.delete(previewId);
    const safetyBackup: BackupItem = { id: `bk-safety-${Date.now().toString(36)}`, name: "Before restore, 26 Nov, 23:31", createdAt: "2026-11-26T23:31:00Z", reason: "before_import", appVersion: "0.9.4", sizeBytes: 412_500 };
    backups = [safetyBackup, ...backups];
    return { restored: p.contents, safetyBackup };
  });
}

// ---- Health check

let doctor: Doctor = {
  ranAt: "2026-11-26T23:00:00Z",
  checks: [
    { id: "disk", label: "Disk space", status: "ok", detail: "412 GB free on the library disks.", hint: null, fix: null },
    { id: "tools", label: "Tools found", status: "ok", detail: "ffmpeg 7.1 and ffprobe are installed.", hint: null, fix: null },
    { id: "gpu", label: "Graphics card", status: "absent", detail: "No graphics card found.", hint: "Files convert on the processor.", fix: null },
    {
      id: "shares",
      label: "Shares",
      status: "bad",
      detail: "NAS box has been unreachable since 22:10.",
      hint: null,
      fix: { label: "Reconnect the share", method: "POST", path: "/api/v2/shares/nas-box/reconnect" },
    },
  ],
};

// A fresh install (no library yet, or the First run state) has run no check that could fail: its health list has no bad rows.
function freshNow(): boolean {
  return noLibrariesYet() || getForcedState() === "setup";
}

export function getDoctor(retest = false): Promise<Doctor> {
  return respond(() => {
    if (retest) doctor = { ...doctor, ranAt: "2026-11-26T23:32:00Z" };
    const checks = freshNow() ? doctor.checks.filter((c) => c.status !== "bad") : doctor.checks;
    return clone({ ...doctor, checks });
  });
}

// Runs a check's fix (the path comes from the check's own fix.path). The mock repairs the share it names.
export function runHealthFix(path: string): Promise<Doctor> {
  return respond(() => {
    if (path.endsWith("/reconnect")) {
      doctor = { ...doctor, checks: doctor.checks.map((c) => (c.id === "shares" ? { ...c, status: "ok", detail: "NAS box is reachable.", fix: null } : c)) };
    }
    return clone(doctor);
  });
}

// True when a check is failing. The shell's red dot reads this.
export function healthFailing(): boolean {
  return !freshNow() && doctor.checks.some((c) => c.status === "bad");
}

// ---- Access (one sign-in, decision A1)

let token: string | null = "syp_7Hk2mQ9xR4vL0pZa8Wb3nJ6tF1cE5dG";

// The access token the sign-in link carries (sign-in checks it synchronously in the mock).
export function currentToken(): string | null {
  return token;
}

export function changePassword(password: string): Promise<void> {
  return respond(() => {
    if (password.length < 8) throw new ApiError("password_too_short", "Use at least 8 characters.", "password");
    if (new TextEncoder().encode(password).length > 72) throw new ApiError("password_too_long", "Use 72 bytes or fewer.", "password");
  });
}

export function getToken(): Promise<{ token: string }> {
  return respond(() => {
    if (!token) throw new ApiError("no_token", "There's no access token yet.");
    return { token };
  });
}

export function rotateToken(): Promise<{ token: string }> {
  return respond(() => {
    token = `syp_${Math.random().toString(36).slice(2, 10)}${Math.random().toString(36).slice(2, 14)}`;
    return { token };
  });
}
