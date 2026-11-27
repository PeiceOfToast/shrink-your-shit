import type { Attention, Job, JobDetail, JobsPage, JobsParams, Overview, Summary } from "./types.ts";
import { ATTENTION, OVERVIEW, SUMMARY } from "./fixtures/index.ts";
import { JOBS } from "./fixtures/jobs.ts";
import { jobDetail } from "./fixtures/jobDetail.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, isFreshInstall, respond } from "./mockState.ts";
import { currentLibraries, reconnectShare } from "./mockLibraries.ts";
import { setAllWorkers, workerName } from "./mockWorkers.ts";
import { healthFailing } from "./mockSettings.ts";
import { getSeenAt, getSeenSeq, newErrorCount } from "./mockLog.ts";

// Mock implementation of the API. Route-named functions; shapes come from the page specs.
// The dev state switcher sets a forced state (mockState.ts); pages see it only through these calls.
export { setForcedState, getForcedState, onForcedStateChange } from "./mockState.ts";
export type { ForcedState } from "./mockState.ts";
export * from "./mockLibraries.ts";
export * from "./mockWorkflows.ts";
export * from "./mockWorkers.ts";
export * from "./mockSettings.ts";
export * from "./mockLog.ts";
export * from "./mockSetup.ts";

// The status line reads this even when a page's own call is forced to fail, so it keeps its counts.
export function getSummary(): Promise<Summary> {
  return respond(
    () => {
      const s = getForcedState();
      // New errors are error lines the System log hasn't been opened past yet (mockLog.ts).
      // Fresh install: no job has finished and no library is watched yet.
      const fresh = s === "fresh" || isFreshInstall() ? { lastDoneAt: null, nextScanAt: null } : {};
      // A fresh install has no log lines yet, so no new errors.
      const none = s === "empty" || s === "fresh";
      const seenAt = none ? null : getSeenAt();
      return { ...SUMMARY, ...fresh, newErrors: none ? 0 : newErrorCount(), seenSeq: getSeenSeq(), seenAt, healthFailing: healthFailing() };
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

// The generic action behind a Needs attention row: it calls the method and path the server named, sending the
// body as JSON when there is one. Only the routes this mock knows answer; anything else fails with a sentence the
// row shows as it is. The encoder test answers with one result sentence; the other routes answer with nothing to show.
export function runAction(method: string, path: string, body?: unknown): Promise<{ line?: string }> {
  const enc = /^\/api\/v2\/workers\/([^/]+)\/capabilities\/test$/.exec(path);
  if (method === "POST" && enc) return testEncoders(enc[1]);
  const share = /^\/api\/v2\/shares\/([^/]+)\/reconnect$/.exec(path);
  if (method === "POST" && share) {
    // The path carries the share's id; the library it belongs to is what reconnects.
    const lib = currentLibraries().find((l) => l.shareId === share[1]);
    if (!lib) return Promise.reject(new ApiError("share_not_found", "That share doesn't exist."));
    return reconnectShare(lib.id).then(() => ({}));
  }
  if (method === "POST" && path === "/api/v2/jobs/actions") {
    const b = body as { action?: unknown; ids?: unknown } | undefined;
    if (!b || b.action !== "retry" || !Array.isArray(b.ids) || b.ids.length === 0) {
      return Promise.reject(new ApiError("bad_request", "Pick the jobs to retry."));
    }
    return respond(() => ({}));
  }
  return Promise.reject(new ApiError("unknown_action", "That action isn't available here."));
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
