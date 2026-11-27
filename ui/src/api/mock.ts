import type { Attention, HistoryLine, Job, JobDetail, JobsPage, JobsParams, LogEvent, Overview, Summary } from "./types.ts";
import { ATTENTION, OVERVIEW, SUMMARY } from "./fixtures/index.ts";
import { JOBS } from "./fixtures/jobs.ts";
import { jobDetail } from "./fixtures/jobDetail.ts";
import { ApiError } from "./errors.ts";
import { getForcedState, noLibrariesYet, respond } from "./mockState.ts";
import { currentLibraries, reconnectShare } from "./mockLibraries.ts";
import { workerName, workersSwitch } from "./mockWorkers.ts";
import { healthFailing } from "./mockSettings.ts";
import { getSeenAt, getSeenSeq, newErrorCount } from "./mockLog.ts";
import { LANGUAGES } from "../format/languages.ts";

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
export * from "./mockFlow.ts";
export * from "./mockTrash.ts";

// The status line reads this even when a page's own call is forced to fail, so it keeps its counts.
// With no libraries nothing converts, queues or fails, and the log is empty.
// The languages the app offers, as workshop's GET /api/v2/languages gives them: [{code, name}], three-letter codes.
export function getLanguages(): Promise<{ code: string; name: string }[]> {
  return Promise.resolve(LANGUAGES);
}

// The all-workers switch as the summary reports it. While it is off, converting counts only the files still finishing.
function withSwitch(s: Summary): Summary {
  const sw = workersSwitch();
  return { ...s, converting: { ...s.converting, running: sw.paused ? sw.finishing : s.converting.running }, workers: sw };
}

export function getSummary(): Promise<Summary> {
  return respond(
    () => {
      if (noLibrariesYet()) {
        return withSwitch({
          ...SUMMARY,
          converting: { running: 0, workers: SUMMARY.converting.workers },
          queue: 0,
          lastDoneAt: null,
          nextScanAt: null,
          failed24h: 0,
          workersOffline: 0,
          newErrors: 0,
          seenSeq: getSeenSeq(),
          seenAt: null,
          healthFailing: healthFailing(),
        });
      }
      return withSwitch({ ...SUMMARY, newErrors: newErrorCount(), seenSeq: getSeenSeq(), seenAt: getSeenAt(), healthFailing: healthFailing() });
    },
    { failInError: false },
  );
}

export function getOverview(): Promise<Overview> {
  return respond(() => {
    if (noLibrariesYet()) {
      return {
        ...OVERVIEW,
        freed: { ...OVERVIEW.freed, totalBytes: 0, todayBytes: 0, nextReleaseBytes: 0, inTrashBytes: 0 },
        converted: { ...OVERVIEW.converted, total: 0, perDay: 0 },
        waiting: { ...OVERVIEW.waiting, count: 0, nextScanAt: null },
        avgShrink: { pct: 0, secPerGB: 0 },
        series: { days: [] },
        running: [],
        failed24h: { ...OVERVIEW.failed24h, count: 0, pct: 0 },
        last24h: { done: 0, skipped: 0, failed: 0 },
        libraries: [],
        workersOn: !workersSwitch().paused,
      };
    }
    // While the switch is off, only the files still finishing are converting; once they are done nothing is.
    const sw = workersSwitch();
    const running = sw.paused ? OVERVIEW.running.slice(0, sw.finishing) : OVERVIEW.running;
    return { ...OVERVIEW, running, libraries: currentLibraries(), workersOn: !sw.paused };
  });
}

export function getAttention(): Promise<Attention[]> {
  return respond(() => (noLibrariesYet() ? [] : ATTENTION));
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
    // While the switch is off only the files still finishing are running; the other running jobs are over.
    const sw = workersSwitch();
    const over = new Set(sw.paused ? items.filter((j) => j.state === "running").slice(sw.finishing).map((j) => j.id) : []);
    items = items.filter((j) => !over.has(j.id));
    const state = params.state ?? "all";
    if (state !== "all") items = items.filter((j) => j.state === state);
    // Jobs carry the worker's name, so a worker's page filters by it.
    if (params.worker) {
      const name = workerName(params.worker);
      items = items.filter((j) => j.worker === name);
    }
    const base = forced === "running" ? { ...counts, all: 2, waiting: 0, failed: 0, done: 0, skipped: 0 } : counts;
    return { counts: { ...base, all: base.all - over.size, running: base.running - over.size }, items, next: null };
  });
}

// POST /api/v2/jobs/actions: {done, refused[{id, code, message}]}. The mock refuses nothing.
export function runJobsAction(action: "retry" | "cancel" | "top", ids: string[]): Promise<{ done: number; refused: { id: string; code: string; message: string }[] }> {
  // The mock refuses nothing, so every id is done.
  void action;
  return respond(() => ({ done: ids.length, refused: [] }));
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

// GET /api/v2/jobs/{id}/events: the job log as flat events, oldest first. Derived from the job's logged steps: run start,
// each step's start, its lines, and its end, then the run end.
export function getJobEvents(id: string): Promise<LogEvent[]> {
  return respond(() => {
    const job = JOBS.find((j) => j.id === id);
    if (!job) throw new ApiError("job_not_found", "That job doesn't exist.");
    const d = jobDetail(job);
    const out: LogEvent[] = [];
    let seq = 0;
    const push = (ts: string, level: LogEvent["level"], kind: LogEvent["kind"], message: string) =>
      out.push({ seq: ++seq, ts, level, kind, source: job.worker, message, jobId: id, libraryId: null, workerId: null });
    const logs = d.logs ?? [];
    const first = logs[0]?.lines[0]?.time ?? job.startedAt ?? job.queuedAt ?? "";
    const last = logs[logs.length - 1]?.lines.at(-1)?.time ?? job.finishedAt ?? first;
    if (logs.length) push(first, "info", "run_start", `Run started for ${job.file}`);
    for (const s of logs) {
      const t = s.lines[0]?.time ?? first;
      push(t, "info", "step_start", `${s.index}. ${s.name}`);
      for (const l of s.lines) push(l.time, l.level === "detail" ? "info" : l.level, "note", l.text);
      push(s.lines.at(-1)?.time ?? t, "info", "step_end", `${s.index}. ${s.name}`);
    }
    if (logs.length) push(last, job.state === "failed" ? "error" : "info", "run_end", `Run ${job.state}`);
    return out;
  });
}

// GET /api/v2/jobs/{id}/history: the job's history lines, oldest first.
export function getJobHistory(id: string): Promise<HistoryLine[]> {
  return respond(() => {
    const job = JOBS.find((j) => j.id === id);
    if (!job) throw new ApiError("job_not_found", "That job doesn't exist.");
    return jobDetail(job).history;
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

// POST /api/v2/jobs/{id}/convert-again: a finished job (done or skipped) makes a NEW job with a new id, so the
// old job keeps its record. (Retry on a failed job keeps the id; see runJobsAction.) The panel moves to the new job.
let againSeq = 0;
export function convertAgain(id: string): Promise<{ ok: true; jobId: string }> {
  return respond(() => {
    const job = JOBS.find((j) => j.id === id);
    if (!job) throw new ApiError("job_not_found", "That job doesn't exist.");
    if (job.state !== "done" && job.state !== "skipped") throw new ApiError("not_finished", "Only a finished job can run again.");
    const jobId = `${id}-again-${++againSeq}`;
    const fresh = {
      ...job,
      id: jobId,
      state: "waiting" as const,
      step: null,
      progress: null,
      queuedAt: new Date().toISOString(),
      startedAt: null,
      finishedAt: null,
      tookMs: null,
      result: { ...job.result, sizeAfter: null, outcome: null, failedStep: null, reason: null },
    };
    JOBS.unshift(fresh);
    return { ok: true as const, jobId };
  });
}
