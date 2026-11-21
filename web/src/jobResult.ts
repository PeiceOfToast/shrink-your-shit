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
