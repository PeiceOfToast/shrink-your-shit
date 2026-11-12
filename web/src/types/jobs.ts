// Jobs as the server sends them today (#113 v2-jobs, #118/#122 v3a-c; captured from abe2b0e in
// proof/ui-v2/jobs/cap-abe2b0e). The list rows are S5's file rows plus state, subState and origin, so their fields
// differ by tab; the contract's full Job comes later.
import type { ID, Time } from "./v2";

export type JobState = "queued" | "preparing" | "running" | "validating" | "committing" | "completed" | "skipped" | "failed" | "cancelled" | "retry_wait";
export type Tab = "waiting" | "running" | "completed" | "skipped" | "failed";
export interface Reason { code: string; message: string } // #122: a fixed code; show the message
export interface Progress { stage: string; percent: number | null; workflowPercent: number; elapsedS: number; speed: number | null; etaS: number | null; fps: number | null; bitrateKbps: number | null }

// GET /api/v2/jobs?tab=&q=&libraryId=&limit=&offset= -> {items, total, counts}
export interface JobItem {
  id: ID;                 // the job id, or "skip-N" for a file left out by a scan (no detail behind it)
  jobId?: ID;
  origin: "job" | "scan";
  state: JobState;
  subState: string | null;
  name: string; folder: string; path: string; library: string; libraryId: ID;
  bytes: number; codec: string | null; resolution: string | null; durationSec: number | null;
  // waiting / retrying / skipped
  attempts?: number; retryAt?: Time | null; status?: string; detail?: string | null;
  priority?: number;       // the contract's Job.priority; not on list rows yet (asked of workshop with sort=queue)
  reason?: Reason | null; skippedId?: ID; // #130: {code, message} on every row (code for icon/colour; show the message)
  // running
  progress?: Progress; worker?: string; convertingTo?: string; encoder?: string; decoder?: string; percent?: number; etaSeconds?: number; fps?: number; bitrateKbps?: number;
  // done
  beforeBytes?: number; afterBytes?: number; finishedAt?: Time; flow?: { id: ID; name: string }; originalState?: string; keptDaysLeft?: number; keptId?: ID;
}
export interface JobsPage { items: JobItem[]; total: number; counts: Record<Tab, number> }

// GET /api/v2/jobs/{id}. A job removed with its library (or stopped) keeps its history: {id, state, reason, events,
// steps: [], removed: true}. Jobs from before the update have no events.
export interface JobStep { step: string; at: Time; ok: boolean; words: string } // v1's steps
export interface JobEvent { seq: number; at: Time; from: JobState | null; to: JobState; reason: string | null; workerId: ID | null }
export interface JobDetail {
  id: ID; state: JobState; removed: boolean; reason: Reason | null; events: JobEvent[]; steps: JobStep[];
  file?: { path: string; bytes: number }; attempts?: number; retryAt?: Time | null; error?: string | null; worker?: string | null;
  createdAt?: Time; updatedAt?: Time; library?: { id: ID; name: string }; workflow?: { id: ID; revisionId: ID; version: number };
  metadata?: { before: Probe | null; after: Probe | null };
}
export interface Probe { container?: string; videoCodec?: string; bitrateKbps?: number; durationSec?: number; sizeBytes?: number;
  streams?: { index: number; type: string; codec: string; Width?: number; Height?: number; channels?: number; channelLayout?: string; language?: string }[] }

// POST /api/v2/jobs/actions {action, ids, priority?} -> {results: [{id, ok, code?, message?}]}
export type JobAction = "retry" | "requeue" | "cancel" | "reprioritize";
export interface ActionResult { id: ID; ok: boolean; code?: string; message?: string }

// GET /api/v2/events (SSE): hello/resync {seq} (with id since #122), job.state, job.progress, processing.*.
export interface JobStateEvent { type: "job.state"; jobId: ID; at: Time; from: JobState | null; to: JobState; reason: Reason | null; workerId: ID | null }
export interface JobProgressEvent { type: "job.progress"; jobId: ID; state: JobState; progress: Progress }
