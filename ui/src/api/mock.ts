import type { Attention, Job, JobsPage, JobsParams, Overview, Summary } from "./types.ts";
import { ATTENTION, LIBRARIES, OVERVIEW, SUMMARY } from "./fixtures/index.ts";
import { JOBS } from "./fixtures/jobs.ts";

// Mock implementation of the API. Route-named functions; shapes come from the page specs.
// The dev state switcher sets a forced state here; pages see it only through these calls.

export type ForcedState = "none" | "empty" | "loading" | "error" | "running";

let forced: ForcedState = "none";
const listeners = new Set<() => void>();

export function setForcedState(state: ForcedState): void {
  forced = state;
  listeners.forEach((cb) => cb());
}

export function getForcedState(): ForcedState {
  return forced;
}

export function onForcedStateChange(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

const DELAY_MS = 300;

async function respond<T>(value: () => T): Promise<T> {
  if (forced === "loading") return new Promise<T>(() => {});
  await new Promise((r) => setTimeout(r, DELAY_MS));
  if (forced === "error") throw new Error("Can't reach the server · trying again");
  return value();
}

export function getSummary(): Promise<Summary> {
  return respond(() => ({ ...SUMMARY }));
}

export function getOverview(): Promise<Overview> {
  return respond(() => {
    if (forced === "empty") {
      return { ...OVERVIEW, converted: 0, convertedPerDay: 0, waiting: 0, running: 0, failed24h: 0, libraries: [] };
    }
    return { ...OVERVIEW, libraries: LIBRARIES };
  });
}

export function getAttention(): Promise<Attention[]> {
  return respond(() => (forced === "empty" ? [] : ATTENTION));
}

export function getLibraries(): Promise<typeof LIBRARIES> {
  return respond(() => (forced === "empty" ? [] : LIBRARIES));
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
    if (forced === "empty") {
      return { counts: { ...counts, all: 0, waiting: 0, running: 0, failed: 0, done: 0, skipped: 0 }, items: [], next: null };
    }
    let items: Job[] = JOBS;
    if (forced === "running") items = JOBS.filter((j) => j.state === "running");
    const state = params.state ?? "all";
    if (state !== "all") items = items.filter((j) => j.state === state);
    return { counts: forced === "running" ? { ...counts, all: 2, waiting: 0, failed: 0, done: 0, skipped: 0 } : counts, items, next: null };
  });
}

export function runJobsAction(action: "retry" | "cancel" | "top", ids: string[]): Promise<{ ok: true; action: string; count: number }> {
  return respond(() => ({ ok: true as const, action, count: ids.length }));
}

export function runWorkersAction(action: "on" | "off"): Promise<{ ok: true; action: string }> {
  return respond(() => ({ ok: true as const, action: `workers ${action}` }));
}

export function scanAllLibraries(): Promise<{ ok: true }> {
  return respond(() => ({ ok: true as const }));
}

// Test the encoders on one worker (POST /api/v2/workers/{id}/capabilities/test). The result is one line.
export function testEncoders(workerId: string): Promise<{ line: string }> {
  return respond(() => ({ line: `Test finished: the graphics encoder failed on ${workerId === "this-computer" ? "This computer" : workerId}.` }));
}
