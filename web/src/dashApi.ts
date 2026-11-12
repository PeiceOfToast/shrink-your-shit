// The Dashboard on the real server: GET /api/v2/summary (#122), start/stop the workers (/api/v2/workers, #132), scan
// every library (v1). With ?mock=1 the Dashboard reads the mock module directly.
import { call, failed, type Answer } from "./api";
import type { JobItem } from "./types/jobs";
import type { ActivityEvent, Worker } from "./types/v2";

// GET /api/v2/summary as the server sends it today: null where it CAN'T YET (perLibrary, freedLast7d); activeJobs and
// recentFailures are the jobs list's rows; workersRunning is a yes/no; workers are full Workers (#132).
export interface RealSummary {
  seq: number;
  badges: { failed24h: number; workersOffline: number | null; pluginUpdates: number };
  activity: { converting: number; waiting: number; workersRunning: boolean };
  jobCounts: Record<string, number>;
  last24h: { completed: number; failed: number; skipped: number; successRate: number | null };
  bytes: { freed: number; waitingToFree: number; freedLast7d: number | null; perLibrary: { libraryId: string; freed: number; waitingToFree: number }[] | null };
  workers: Worker[] | null;
  activeJobs: JobItem[]; recentFailures: JobItem[];
  throughput: { hourStart: string; completed: number; savedBytes: number }[];
  processingPaused: boolean; intakePaused: boolean;
}
export const getSummary = (): Promise<Answer<RealSummary>> => call("GET", "/api/v2/summary");
// Start all / Stop all: each worker's own action. Stop all with now false is "Stop after these files": a busy worker
// drains (finishes its files, then stops), an idle one stops (review ab98ef3: Stop all never throws a conversion away
// without asking). Every worker is tried; the failures are named.
async function eachWorker(verb: (w: Worker) => "start" | "stop" | "drain" | null): Promise<Answer<unknown>> {
  const r = await call<{ items: Worker[] }>("GET", "/api/v2/workers");
  if (failed(r)) return r;
  const bad: string[] = [];
  for (const w of r.items) { const v = verb(w); if (!v) continue; const x = await call("POST", "/api/v2/workers/" + encodeURIComponent(w.id) + "/" + v); if (failed(x)) bad.push(w.name + ": " + x.error.message); }
  return bad.length ? { error: { code: "some_failed", message: "Not changed: " + bad.join(" ") } } : {};
}
const connected = (w: Worker) => w.status !== "offline" && w.status !== "stalled";
export const startAllWorkers = () => eachWorker((w) => (w.status === "paused" ? "start" : null));
export const stopAllWorkers = (now: boolean) => eachWorker((w) => (!connected(w) || w.status === "paused" ? null : !now && w.currentJobIds.length ? "drain" : "stop"));
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
