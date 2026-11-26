import type { HistoryLine, Job, JobDetail, JobStep, LogLine, LogStep, MediaRow, StepOutcome } from "../types.ts";

// One job's detail, built from its list row. Shapes follow page 03; the wording is mock text.
// Mock: finished jobs whose detailed log retention has dropped it (page 03 "no longer kept").
const LOG_GONE = new Set(["j10"]);
const STEP_NAMES = ["Library", "Audio", "Convert video", "Check the result", "Save"];
const DECISIONS = [
  "Found the file in its library; one video track, one audio track",
  "Kept 2 of 5 audio tracks: en, de",
  "Chose HEVC on the graphics card; 1080p kept",
  "New file plays for the full length and is 41% smaller",
  "Written next to the original; original moved to the trash",
];
const DURATIONS = [1100, 3200, 252000, 4000, 900];

function outcomesFor(job: Job): StepOutcome[] {
  switch (job.state) {
    case "done":
      return ["done", "nothing", "done", "done", "done"];
    case "failed": {
      const at = job.step.startsWith("Check") ? 3 : 2;
      return STEP_NAMES.map((_, i) => (i < at ? "done" : i === at ? "failed" : "skipped"));
    }
    case "running":
      return ["done", "done", "running", "waiting", "waiting"];
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
          lines: linesFor(s.index - 1, s.outcome, job),
        }));
  const pct = /(\d+)% smaller/.exec(job.result);
  const failed = job.state === "failed";
  const media: MediaRow[] = [
    { group: "General", field: "Duration", before: "1:58:12", after: job.state === "done" ? "1:58:12" : null, changed: false },
    { group: "General", field: "Size", before: "38.4 GB", after: job.state === "done" ? "21.9 GB" : null, changed: true },
    { group: "Video", field: "Codec", before: "H.264", after: job.state === "done" ? "HEVC" : null, changed: true },
    { group: "Video", field: "Resolution", before: "1920×1080", after: job.state === "done" ? "1920×1080" : null, changed: false },
    { group: "Audio", field: "Track 1 · English · AC-3 5.1", before: "kept", after: job.state === "done" ? "kept" : null, changed: false },
    { group: "Audio", field: "Track 2 · German · AC-3 2.0", before: "present", after: job.state === "done" ? "removed" : null, changed: true },
    { group: "Subtitles", field: "Track 1 · English · SRT", before: "present", after: job.state === "done" ? "present" : null, changed: false },
  ];
  const history: HistoryLine[] = [
    { time: "14:00", level: "info", text: `Queued by the ${job.library} scan` },
    { time: "14:01", level: "info", text: `Picked up by ${job.worker || "a worker"}` },
    ...(failed
      ? [{ time: "14:06", level: "error" as const, text: `Failed: ${job.step}` }]
      : job.state === "done"
        ? [{ time: "14:18", level: "info" as const, text: "Finished · original moved to the trash" }]
        : []),
  ];
  return {
    job,
    path: `/media/${job.library.toLowerCase().replace(/\s+/g, "-")}/${job.file}`,
    workflow: job.workflow,
    worker: job.worker || "—",
    attempt: failed ? 2 : 1,
    // A waiting or skipped job never started.
    startedAt: job.state === "waiting" || job.state === "skipped" ? null : (job.started ?? "14:02"),
    took: job.when.startsWith("took") ? job.when.replace("took ", "") : null,
    sizeBefore: "38.4 GB",
    sizeAfter: job.state === "done" ? "21.9 GB" : null,
    savedPct: pct ? Number(pct[1]) : null,
    original: job.state === "done" ? "In the trash until 16 Nov" : job.state === "running" ? "Not yet" : failed ? "Kept as it was" : "Not touched",
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
