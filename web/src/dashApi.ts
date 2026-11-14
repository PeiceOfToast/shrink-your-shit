// The Dashboard on the real server: GET /api/v2/summary (#122), start/stop the workers (/api/v2/workers, #132), scan
// every library (v1). With ?mock=1 the Dashboard reads the mock module directly.
import { call, failed, type Answer } from "./api";
import type { JobItem } from "./types/jobs";
import type { ActivityEvent, Worker } from "./types/v2";

// GET /api/v2/summary as the server sends it today: null where it CAN'T YET (perLibrary, freedLast7d); activeJobs and
// recentFailures are the jobs list's rows; workersRunning is a yes/no; workers are full Workers (#132).
export interface RealSummary {
  seq: number;
  badges: { failed24h: number; workersOffline: number | null; pluginUpdates: number; joinRequestsWaiting?: number };
  activity: { converting: number; waiting: number; workersRunning: boolean };
  jobCounts: Record<string, number>;
  last24h: { completed: number; failed: number; skipped: number; successRate: number | null };
  // V5c (#137): freed; waitingToFree = originals in .syp-trash, gone by their date; couldFree = originals left in place
  // (nothing removes them). Three separate figures, never added together (design 01:55Z).
  bytes: { freed: number; waitingToFree: number; couldFree?: number; grew?: number; freedLast7d: number | null; perLibrary: { libraryId: string; freed: number; waitingToFree: number; couldFree?: number; grew?: number }[] | null };
  workers: Worker[] | null;
  activeJobs: JobItem[]; recentFailures: JobItem[];
  throughput: { hourStart: string; completed: number; savedBytes: number }[];
  processingPaused: boolean; intakePaused: boolean;
}
export const getSummary = (): Promise<Answer<RealSummary>> => call("GET", "/api/v2/summary");
export async function scanAllLibraries(): Promise<Answer<unknown>> {
  const r = await call<{ items: { id: string }[] }>("GET", "/api/libraries");
  if (failed(r)) return r;
  for (const l of r.items) { const x = await call("POST", "/api/libraries/" + encodeURIComponent(l.id) + "/scan"); if (failed(x)) return x; }
  return {};
}

// GET /api/v2/activity?limit= (#131, V3d): the feed, newest first; each new entry also comes live as `activity`.
export async function getActivity(limit = 8): Promise<Answer<ActivityEvent[]>> {
  const r = await call<{ items: ActivityEvent[] }>("GET", "/api/v2/activity?limit=" + limit);
  return failed(r) ? r : r.items;
}
