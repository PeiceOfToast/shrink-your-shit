import type { Attention, Job, JobDetail, JobsPage, JobsParams, Overview, Summary } from "./types.ts";
import { ATTENTION, OVERVIEW, SUMMARY } from "./fixtures/index.ts";
import { JOBS } from "./fixtures/jobs.ts";
import { jobDetail } from "./fixtures/jobDetail.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, respond } from "./mockState.ts";
import { currentLibraries } from "./mockLibraries.ts";
import { setAllWorkers, workerName } from "./mockWorkers.ts";
import { healthFailing } from "./mockSettings.ts";

// Mock implementation of the API. Route-named functions; shapes come from the page specs.
// The dev state switcher sets a forced state (mockState.ts); pages see it only through these calls.
export { setForcedState, getForcedState, onForcedStateChange } from "./mockState.ts";
export type { ForcedState } from "./mockState.ts";
export * from "./mockLibraries.ts";
export * from "./mockWorkflows.ts";
export * from "./mockWorkers.ts";
export * from "./mockSettings.ts";

// The status line reads this even when a page's own call is forced to fail, so it keeps its counts.
export function getSummary(): Promise<Summary> {
  return respond(
    () => {
      const s = getForcedState();
      // Error and running both show new errors, so the status line's "N new errors" link is reachable.
      // Fresh install: no job has finished and no library is watched yet.
      const fresh = s === "fresh" ? { lastDoneAt: null, nextScanAt: null } : {};
      return { ...SUMMARY, ...fresh, newErrors: s === "error" || s === "running" ? 2 : 0, healthFailing: healthFailing() };
    },
    { failInError: false },
  );
}

export function getOverview(): Promise<Overview> {
  return respond(() => {
    if (getForcedState() === "empty") {
      return {
        ...OVERVIEW,
        converted: { ...OVERVIEW.converted, total: 0, perDay: 0 },
        waiting: { ...OVERVIEW.waiting, count: 0 },
        running: [],
        failed24h: { ...OVERVIEW.failed24h, count: 0 },
        libraries: [],
      };
    }
    return { ...OVERVIEW, libraries: currentLibraries() };
  });
}

export function getAttention(): Promise<Attention[]> {
  return respond(() => (getForcedState() === "empty" ? [] : ATTENTION));
}

export function getJobs(params: JobsParams = {}): Promise<JobsPage> {
  return respond(() => {
    const counts = {
      all: 1440,
      waiting: 1284,
      running: 2,
      failed: 3,
      done: 142,
      skipped: 9,
    };
    const forced = getForcedState();
    if (forced === "empty") {
      return { counts: { ...counts, all: 0, waiting: 0, running: 0, failed: 0, done: 0, skipped: 0 }, items: [], next: null };
    }
    let items: Job[] = JOBS;
    if (forced === "running") items = JOBS.filter((j) => j.state === "running");
    const state = params.state ?? "all";
    if (state !== "all") items = items.filter((j) => j.state === state);
    // Jobs carry the worker's name, so a worker's page filters by it.
    if (params.worker) {
      const name = workerName(params.worker);
      items = items.filter((j) => j.worker === name);
    }
    return { counts: forced === "running" ? { ...counts, all: 2, waiting: 0, failed: 0, done: 0, skipped: 0 } : counts, items, next: null };
  });
}

export function runJobsAction(action: "retry" | "cancel" | "top", ids: string[]): Promise<{ ok: true; action: string; count: number }> {
  return respond(() => ({ ok: true as const, action, count: ids.length }));
}

export function runWorkersAction(action: "on" | "off"): Promise<{ ok: true; action: string }> {
  return respond(() => {
    setAllWorkers(action === "on");
    return { ok: true as const, action: `workers ${action}` };
  });
}

export function scanAllLibraries(): Promise<{ ok: true }> {
  return respond(() => ({ ok: true as const }));
}

// Test the encoders on one worker (POST /api/v2/workers/{id}/capabilities/test). The result is one line.
export function testEncoders(workerId: string): Promise<{ line: string }> {
  return respond(() => ({ line: `Test finished: the graphics encoder failed on ${workerName(workerId) ?? workerId}.` }));
}

// GET /api/v2/jobs/{id}: the head, Summary, Steps, log, media and history of one job.
export function getJob(id: string): Promise<JobDetail> {
  return respond(() => {
    const job = JOBS.find((j) => j.id === id);
    if (!job) throw new ApiError("job_not_found", "That job doesn't exist.");
    return jobDetail(job);
  });
}

// GET /api/v2/jobs/{id}/neighbours?state=: the ids before and after this one in the list behind the panel.
export function getJobNeighbours(id: string, state?: string): Promise<{ prev: string | null; next: string | null }> {
  return respond(() => {
    const list = state && state !== "all" ? JOBS.filter((j) => j.state === state) : JOBS;
    const i = list.findIndex((j) => j.id === id);
    return { prev: i > 0 ? list[i - 1].id : null, next: i >= 0 && i < list.length - 1 ? list[i + 1].id : null };
  });
}

// POST /api/v2/jobs/{id}/convert-again: queues the file again; the panel stays open on the new attempt.
export function convertAgain(id: string): Promise<{ ok: true; jobId: string }> {
  return respond(() => ({ ok: true as const, jobId: id }));
}
