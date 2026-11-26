import type { Job } from "../types.ts";

// Fixture times are offsets from when the mock loads, so the screens tell the same story at any hour.
const START = Date.now();
const MIN = 60_000;

export const minsAgo = (m: number) => new Date(START - m * MIN).toISOString();
export const plus = (iso: string, ms: number) => new Date(Date.parse(iso) + ms).toISOString();
// A local clock time on a day relative to today (-1 = yesterday).
export const localAt = (dayOffset: number, h: number, m: number) => {
  const d = new Date(START);
  d.setDate(d.getDate() + dayOffset);
  d.setHours(h, m, 0, 0);
  return d.toISOString();
};

// The timing fields a job row carries, by state.
type Timing = Pick<Job, "queuedAt" | "startedAt" | "finishedAt" | "tookMs">;
export const finished = (startMin: number, tookMs: number): Timing => {
  const startedAt = minsAgo(startMin);
  return { queuedAt: null, startedAt, finishedAt: plus(startedAt, tookMs), tookMs };
};
export const runningSince = (startMin: number): Timing => ({ queuedAt: null, startedAt: minsAgo(startMin), finishedAt: null, tookMs: null });
export const queuedSince = (min: number): Timing => ({ queuedAt: minsAgo(min), startedAt: null, finishedAt: null, tookMs: null });
export const skippedAt = (min: number): Timing => ({ queuedAt: null, startedAt: null, finishedAt: minsAgo(min), tookMs: null });
