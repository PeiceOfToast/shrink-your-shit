import type { HistoryLine, Job, JobDetail, JobStep, LogLine, LogStep, LogValue, MediaRow, StepOutcome } from "../types.ts";

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
const BEFORE_BYTES = 38.4e9;

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
      const at = job.step.startsWith("Check") ? 3 : 2;
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
  const t = (s: number) => `14:${String(2 + i).padStart(2, "0")}:${String(10 + s).padStart(2, "0")}.204`;
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
  // The result text carries the saving: "41% smaller" or "12% bigger".
  const pct = /(\d+)% (smaller|bigger)/.exec(job.result);
  const afterBytes =
    done && pct
      ? Math.round(BEFORE_BYTES * (pct[2] === "smaller" ? 1 - Number(pct[1]) / 100 : 1 + Number(pct[1]) / 100))
      : null;
  const sizeAfter = afterBytes != null ? `${(afterBytes / 1e9).toFixed(1)} GB` : null;
  const path = `/media/${job.library.toLowerCase().replace(/\s+/g, "-")}/${job.file}`;

  // Before and after, in the order the page groups them. Changed rows say what happened ("removed", "new").
  const media: MediaRow[] = [
    { group: "General", field: "Duration", before: "1:58:12", after: done ? "1:58:12" : null, changed: false },
    { group: "General", field: "Size", before: "38.4 GB", after: sizeAfter, changed: true },
    { group: "Video", field: "Codec", before: "H.264", after: done ? "HEVC" : null, changed: true },
    { group: "Video", field: "Resolution", before: "1920×1080", after: done ? "1920×1080" : null, changed: false },
    { group: "Audio", field: "Track 1 · English · AC-3 5.1", before: "AC-3 5.1", after: done ? "AC-3 5.1" : null, changed: false },
    { group: "Audio", field: "Track 2 · German · AC-3 2.0", before: "AC-3 2.0", after: done ? "removed" : null, changed: true },
    { group: "Audio", field: "Stereo · AAC 2.0", before: "—", after: done ? "new" : null, changed: true },
    { group: "Subtitles", field: "Track 1 · English · SRT", before: "SRT", after: done ? "SRT" : null, changed: false },
  ];
  const history: HistoryLine[] = [
    { time: "14:00", level: "info", text: `Queued by the ${job.library} scan` },
    { time: "14:01", level: "info", text: `Picked up by ${job.worker || "a worker"}` },
    ...(failed
      ? [{ time: "14:06", level: "error" as const, text: `Failed: ${job.step}` }]
      : done
        ? [{ time: "14:18", level: "info" as const, text: "Finished · original moved to the trash" }]
        : []),
  ];
  return {
    job,
    path,
    workflow: job.workflow,
    worker: job.worker || "—",
    attempt: failed ? 2 : 1,
    // A waiting or skipped job never started.
    startedAt: job.state === "waiting" || job.state === "skipped" ? null : (job.started ?? "14:02"),
    took: job.when.startsWith("took") ? job.when.replace("took ", "") : null,
    finishedAt: done ? "14:18" : failed ? "14:06" : null,
    sizeBefore: "38.4 GB",
    sizeAfter,
    sizeBeforeBytes: BEFORE_BYTES,
    sizeAfterBytes: afterBytes,
    writtenAt: done ? "14:18" : null,
    outputPath: done ? path : null,
    timeLeft: job.state === "running" ? "3 min left" : null,
    original: done ? "In the trash until 16 Nov" : job.state === "running" ? "Not yet" : failed ? "Kept as it was" : "Not touched",
    failure: failed
      ? { step: job.step.split(" · ")[0], reason: job.step.split(" · ")[1] ?? "The step didn't work", nextAction: "Check that the file plays in another player, then retry.", actionLabel: "Retry" }
      : null,
    skipReason: job.state === "skipped" ? job.step : null,
    steps,
    logs,
    media,
    history,
  };
}
