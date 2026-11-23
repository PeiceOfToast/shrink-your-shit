// The job drawer's wording for the Result table and the Workflow line. Kept apart from Jobs.tsx so the tests can
// run under `node --test` without a DOM.

// A job that kept its original: the probe in metadata.after is the new file that was refused, not one that was saved.
export const afterHeader = (kept: boolean): string => (kept ? "New file (not kept)" : "After");

// The server sends workflow.queuedVersion only when the workflow changed between queueing and running.
export const queuedLine = (w: { version: number; queuedVersion?: number } | null | undefined): string | null =>
  w?.queuedVersion != null ? `The workflow changed after this job was queued (v${w.queuedVersion} → v${w.version}).` : null;

// The server ends a job with reason.code "workflow_stopped" when its own workflow stopped it ("Stop as failed", or
// "Convert again later" after its tries). That is not something breaking, so the drawer does not say "Why it failed".
// withFailure: the job has a failure block (the step, the ffmpeg lines); without one the plain "Why" section shows.
export const whyHeading = (code: string, withFailure: boolean): string =>
  code === "workflow_stopped" ? "Stopped by its workflow" : withFailure ? "Why it failed" : "Why";

// The list pill's hover for a workflow's own stop. The server's message usually starts "Stopped by this workflow: ",
// so it is shown as it is rather than with the prefix doubled.
export const stoppedTitle = (message: string): string =>
  /^stopped by/i.test(message) ? message : "Stopped by its workflow: " + message;

// The codec as the app writes it on both sides of the Result table: "H.264", "HEVC" (the server sends h264 / hevc).
export const codecName = (codec: string): string => codec.toUpperCase().replace("H264", "H.264");

// A done job's New file line ("hevc 1280x720 25.000 fps 8-bit, 0 audio, …") as the Video row's value: the codec as codecName writes it, then the rest up to the first comma.
export const newFileVideo = (line: string): string => line.split(",")[0].replace(/^\w+/, codecName);

// The worker as the drawer says it: the built-in worker is "This computer" (the Workers screen's wording); a job with no worker shows a dash.
export const workerLabel = (w: string | null | undefined): string => (!w ? "—" : w === "builtin" ? "This computer" : w);

// The Workflow line's rule: a rule is named only when the job's own workflow decided it (step N of its workflow, <name>).
export const ruleName = (jobWorkflow: { id: string } | null | undefined, name: string | null | undefined): string | null => (jobWorkflow && name ? name : null);

// A video stream as one line, the same on both sides of the Result table: "H.264 1280x720 25.000 fps 8-bit".
// The fields are the scan's stream fields (types/jobs.ts Probe.streams); a missing one is left out, not guessed.
export const videoDetail = (codec: string | undefined, s: { Width?: number; Height?: number; frameRate?: number; bitDepth?: number } | undefined): string | null => {
  if (!s && !codec) return null;
  const c = codec ? codecName(codec) : "";
  const size = s?.Width && s?.Height ? s.Width + "x" + s.Height : "";
  const fps = s?.frameRate ? s.frameRate.toFixed(3) + " fps" : "";
  const depth = s?.bitDepth ? s.bitDepth + "-bit" : "";
  return [c, size, fps, depth].filter(Boolean).join(" ") || null;
};

// Rounding for the header's size change: the percentage as a whole number, the sizes as the list row shows them.
export const savedPercent = (beforeBytes: number, afterBytes: number): number => Math.round((100 * (beforeBytes - afterBytes)) / beforeBytes);

// The subtitle count and the length clock from a check's "New file:" or "Original:" line ("hevc 1280x720 …, 0 audio, 2 subtitles, 0:00:20").
// A part the line doesn't carry is left out, not guessed; null when neither is there.
export const fileFacts = (line: string | undefined): { subtitles?: number; length?: string } | null => {
  if (!line) return null;
  const subs = /(\d+) subtitles?\b/.exec(line);
  const clock = /(\d+:\d{2}:\d{2}(?:\.\d+)?)\s*$/.exec(line.trim());
  return subs || clock ? { subtitles: subs ? Number(subs[1]) : undefined, length: clock ? clock[1] : undefined } : null;
};
