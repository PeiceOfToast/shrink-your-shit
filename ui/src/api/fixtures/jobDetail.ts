import type { HistoryLine, Job, JobDetail, JobStep, LogLine, LogStep, LogValue, MediaRow, StepOutcome } from "../types.ts";
import { minsAgo, plus, trashUntil } from "./clock.ts";

// One job's detail, built from its list row. Shapes follow page 03; the wording is mock text.
// Mock: finished jobs whose detailed log retention has dropped it (page 03 "no longer kept").
const LOG_GONE = new Set(["j10"]);
// The Shrink videos flow's own step names (drafts/workflows/editor-b.html).
const STEP_NAMES = ["Which files", "Clean up audio", "Convert video", "Check result", "Save the result", "Tell the media server"];
const DECISIONS = [
  "Found the file in its library; one video track, three audio tracks",
  "Kept English 5.1, dropped German 2.0, added a stereo track",
  "Chose HEVC on the graphics card; 1080p kept",
  "New file plays for the full length and is 41% smaller",
  "Written next to the original; original moved to the trash",
  "Told the media server to refresh this library",
];
const DURATIONS = [1100, 3200, 252000, 4000, 900, 1400];

// The settings and measurements each step used (page 03 "values used").
const VALUES: LogValue[][] = [
  [{ name: "Library", value: "Movies", from: "Library setting" }],
  [{ name: "Audio kept", value: "en 5.1, stereo", from: "Workflow setting" }],
  [
    { name: "Quality", value: "3 · Balanced", from: "Default quality" },
    { name: "Graphics card", value: "Intel (QSV)", from: "Worker" },
  ],
  [{ name: "Must be", value: "smaller than the original", from: "Workflow setting" }],
  [{ name: "Old file", value: "kept in the trash 7 days", from: "Workflow setting" }],
  [{ name: "Refresh", value: "this library", from: "Library setting" }],
];

function outcomesFor(job: Job): StepOutcome[] {
  switch (job.state) {
    case "done":
      return STEP_NAMES.map(() => "done");
    case "failed": {
      // The failed step's index (1-based) is the 0-based slot to mark.
      const at = job.step ? job.step.index - 1 : 2;
      return STEP_NAMES.map((_, i) => (i < at ? "done" : i === at ? "failed" : "skipped"));
    }
    case "running":
      return ["done", "done", "running", "waiting", "waiting", "waiting"];
    case "skipped":
    case "waiting":
      return STEP_NAMES.map(() => (job.state === "skipped" ? "skipped" : "waiting"));
  }
}

function linesFor(i: number, outcome: StepOutcome, job: Job): LogLine[] {
  // Each step's lines fall a minute apart from the job's start, a few seconds in.
  const base = job.startedAt ?? minsAgo(0);
  const t = (s: number) => plus(base, (i * 60 + 10 + s) * 1000 + 204);
  if (outcome === "failed") {
    return [
      { time: t(0), level: "info", text: `Started ${STEP_NAMES[i]}` },
      { time: t(4), level: "warn", text: "The first frames took longer than expected" },
      { time: t(9), level: "error", text: "The encoder stopped: the file couldn't be read past 00:41:12" },
    ];
  }
  return [
    { time: t(0), level: "info", text: `Started ${STEP_NAMES[i]}` },
    { time: t(3), level: "detail", text: "Probe: container mkv, codec h264, 1920x1080" },
    { time: t(6), level: "info", text: `Finished ${STEP_NAMES[i]} for ${job.file}` },
  ];
}

export function jobDetail(job: Job): JobDetail {
  const outcomes = outcomesFor(job);
  const steps: JobStep[] = STEP_NAMES.map((name, i) => ({
    index: i + 1,
    name,
    outcome: outcomes[i],
    durationMs: outcomes[i] === "done" || outcomes[i] === "nothing" || outcomes[i] === "failed" ? DURATIONS[i] : null,
  }));
  // A waiting job has no log yet (an empty list); a finished job in LOG_GONE has had its log dropped by retention.
  const logs: LogStep[] | null = LOG_GONE.has(job.id)
    ? null
    : steps
        .filter((s) => s.outcome !== "skipped" && s.outcome !== "waiting")
        .map((s) => ({
          ...s,
          decision: s.outcome === "failed" ? "Couldn't read the file past 00:41:12" : DECISIONS[s.index - 1],
          command: s.index === 3 ? `ffmpeg -i "${job.file}" -c:v hevc_qsv -global_quality 26 -c:a copy out.mkv` : null,
          values: VALUES[s.index - 1],
          lines: linesFor(s.index - 1, s.outcome, job),
        }));
  const done = job.state === "done";
  const failed = job.state === "failed";
  // Sizes come from the job's own result, in bytes.
  const beforeBytes = job.result.sizeBefore;
  const afterBytes = done ? job.result.sizeAfter : null;
  const path = `/media/${job.library.toLowerCase().replace(/\s+/g, "-")}/${job.file}`;

  // Finish = start + took, so Took always matches Started → Finished.
  const startedAt = job.startedAt;
  const finishedAt = (done || failed) && startedAt && job.tookMs != null ? plus(startedAt, job.tookMs) : null;

  // Before and after, in the order the page groups them. Changed rows say what happened ("removed", "new").
  const media: MediaRow[] = [
    { group: "General", field: "Duration", kind: "duration", before: 7_092_000, after: done ? 7_092_000 : null, changed: false },
    { group: "General", field: "Size", kind: "bytes", before: beforeBytes, after: afterBytes, changed: true },
    { group: "Video", field: "Codec", before: "H.264", after: done ? "HEVC" : null, changed: true },
    { group: "Video", field: "Resolution", before: "1920×1080", after: done ? "1920×1080" : null, changed: false },
    { group: "Audio", field: "Track 1 · English · AC-3 5.1", before: "AC-3 5.1", after: done ? "AC-3 5.1" : null, changed: false },
    { group: "Audio", field: "Track 2 · German · AC-3 2.0", before: "AC-3 2.0", after: done ? "removed" : null, changed: true },
    { group: "Audio", field: "Stereo · AAC 2.0", before: "—", after: done ? "new" : null, changed: true },
    { group: "Subtitles", field: "Track 1 · English · SRT", before: "SRT", after: done ? "SRT" : null, changed: false },
  ];
  const base = job.startedAt ?? job.queuedAt ?? minsAgo(0);
  const history: HistoryLine[] = [
    { time: plus(base, -2 * 60_000), level: "info", text: `Queued by the ${job.library} scan` },
    { time: plus(base, -60_000), level: "info", text: `Picked up by ${job.worker || "a worker"}` },
    ...(failed
      ? [{ time: finishedAt ?? base, level: "error" as const, text: `Failed: ${job.step?.name ?? "a step"}` }]
      : done
        ? [{ time: finishedAt ?? base, level: "info" as const, text: "Finished · original moved to the trash" }]
        : []),
  ];
  return {
    job,
    path,
    workflow: job.workflow,
    worker: job.worker || "—",
    attempt: failed ? 2 : 1,
    // A waiting or skipped job never started.
    startedAt,
    tookMs: job.tookMs,
    finishedAt,
    sizeBeforeBytes: beforeBytes,
    sizeAfterBytes: afterBytes,
    writtenAt: done ? finishedAt : null,
    outputPath: done ? path : null,
    timeLeftMs: job.state === "running" ? 180_000 : null,
    original: done && finishedAt
      ? { action: "trashed", until: trashUntil(finishedAt) }
      : { action: job.state === "running" ? "not_yet" : "not_touched", until: null },
    failure: failed
      ? { step: job.step?.name ?? "A step", reason: job.result.reason ?? "The step didn't work", nextAction: "Check that the file plays in another player, then retry.", actionLabel: "Retry" }
      : null,
    skipReason: job.state === "skipped" ? job.result.reason : null,
    steps,
    logs,
    media,
    history,
  };
}
