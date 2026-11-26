import type { Job, JobOriginal } from "../api/types.ts";
import { dayOf } from "./time.ts";

// Text the job list and panels build from a job's structured fields. The server sends names and numbers only.

// "5.9 GB" under 10 GB, "18 GB" above. Sizes in bytes.
export function sizeText(bytes: number): string {
  const gb = bytes / 1e9;
  return gb < 10 ? `${gb.toFixed(1)} GB` : `${Math.round(gb)} GB`;
}

// "Converting video · step 3 of 5"; "Waiting for a worker" while queued; "—" otherwise.
export function jobStepText(job: Job): string {
  if (job.step) return `${job.step.name} · step ${job.step.index} of ${job.step.count}`;
  return job.state === "waiting" ? "Waiting for a worker" : "—";
}

// "41% smaller · 5.9 GB freed", "8% bigger · original kept", "about 18 GB saving" while running,
// the one-line reason for a failure. "—" when there is nothing to say yet.
export function jobResultText(job: Job): string {
  const r = job.result;
  const warn = r.warnings > 0 ? ` · ${r.warnings} warning${r.warnings === 1 ? "" : "s"}` : "";
  switch (r.outcome) {
    case "smaller": {
      const freed = r.sizeBefore - (r.sizeAfter ?? r.sizeBefore);
      return `${Math.round((freed / r.sizeBefore) * 100)}% smaller · ${sizeText(freed)} freed${warn}`;
    }
    case "bigger_kept": {
      const grew = (r.sizeAfter ?? r.sizeBefore) - r.sizeBefore;
      return `${Math.round((grew / r.sizeBefore) * 100)}% bigger · original kept${warn}`;
    }
    case "failed":
      return `${r.reason ?? "Failed"}${warn}`;
    default:
      return job.state === "running" && r.sizeAfter != null ? `about ${sizeText(r.sizeBefore - r.sizeAfter)} saving` : "—";
  }
}

export function jobResultTone(job: Job): "ok" | "bad" | "" {
  return job.result.outcome === "smaller" ? "ok" : job.result.outcome === "failed" ? "bad" : "";
}

// The converting card's line: "Converting video · step 3 of 5 · about 18 GB saving".
export function jobHint(job: Job): string {
  const result = job.state === "running" ? jobResultText(job) : "—";
  return [jobStepText(job), result === "—" ? "" : result].filter(Boolean).join(" · ");
}

// The Summary's "Original" line, from the server's {action, until}.
export function originalText(o: JobOriginal): string {
  switch (o.action) {
    case "not_yet":
      return "Not yet";
    case "not_touched":
      return "Not touched";
    case "trashed":
      return o.until ? `In the trash until ${dayOf(o.until)}` : "In the trash";
    case "deleted":
      return "Deleted";
    case "restored":
      return "Restored";
    default: {
      const unknown: never = o.action;
      throw new Error(`Unknown original action: ${unknown}`);
    }
  }
}
