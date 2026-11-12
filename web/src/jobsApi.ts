// V6 Jobs on the real /api/v2/jobs (#113), or the sample jobs with ?mock=1 (mock/jobs.ts).
import { call, failed, MOCK, type Answer } from "./api";
import { mockAction, mockJob, mockJobs, mockConvertAgain } from "./mock/jobs";
import type { ActionResult, JobAction, JobDetail, JobsPage, Tab, Pin } from "./types/jobs";

export async function listJobs(tab: Tab, q: string): Promise<Answer<JobsPage>> {
  if (MOCK) return mockJobs(tab, q);
  // #134: the Waiting tab in the order the worker takes files, each with its position; the page never reorders it
  return call("GET", "/api/v2/jobs?tab=" + tab + (tab === "waiting" ? "&sort=queue" : "") + "&limit=200" + (q ? "&q=" + encodeURIComponent(q) : ""));
}
// review a8f85e0 P3: a skipped or stopped job's row in the Skipped tab when it isn't in the first page. Its own row went
// when it was skipped or stopped, so its detail has no file name to search by: page through the tab, 500 at a time
export async function findSkip(jobId: string): Promise<Answer<JobsPage>> {
  if (MOCK) return listJobs("skipped", "");
  for (let at = 0; ; at += 500) {
    const r = await call<JobsPage>("GET", "/api/v2/jobs?tab=skipped&limit=500&offset=" + at);
    if (failed(r) || r.items.some((x) => x.jobId === jobId) || !r.items.length || at + r.items.length >= r.total) return r;
  }
}
export async function getJob(id: string): Promise<Answer<JobDetail>> {
  if (MOCK) return mockJob(id) ?? { error: { code: "not_found", message: "There is no such job." } };
  return call("GET", "/api/v2/jobs/" + encodeURIComponent(id));
}
// reprioritize sends pin (#143): first, last, or none (back to its normal place).
// POST /api/v2/jobs/actions takes job ids only (dev-qa 09:12Z: "skip-N" is "There is no such job."). A file a scan left
// out ("skip-N", N = its skipped id) is converted again by v1's POST /api/skipped/{N}/convert-again: it forgets the
// decision and the next scan looks at the file again (or says where a renamed file went). Same per-file results.
export async function jobAction(action: JobAction, ids: string[], pin?: Pin | "none"): Promise<Answer<{ results: ActionResult[] }>> {
  const skips = action === "requeue" ? ids.filter((i) => i.startsWith("skip-")) : [], rest = ids.filter((i) => !skips.includes(i));
  const results: ActionResult[] = [];
  for (const id of skips) {
    const n = id.slice(5), r = MOCK ? mockConvertAgain(n) : await call<{ ok: boolean }>("POST", "/api/skipped/" + encodeURIComponent(n) + "/convert-again");
    results.push(failed(r) ? { id, ok: false, code: r.error.code, message: r.error.message } : { id, ok: true });
  }
  if (rest.length) {
    const r = MOCK ? mockAction(action, rest, pin) : await call<{ results: ActionResult[] }>("POST", "/api/v2/jobs/actions", { action, ids: rest, ...(pin && { pin }) });
    if (failed(r)) return r;
    results.push(...r.results);
  }
  return { results };
}

// G2 (one UI): a replaced original kept in .syp-trash can be got back. GET /api/activity/kept lists them (keptId, the
// original's path and size, the new file's name when it differs, the delete date); POST /api/kept/{id}/restore puts it
// back and deletes the smaller version, answering in its own words. The sample install keeps nothing.
export interface Kept { keptId: string; path: string; originalBytes: number; nowBytes: number; deleteOn: string; newPath?: string }
export async function listKept(): Promise<Answer<{ items: Kept[]; days: number }>> {
  if (MOCK) return { items: [], days: 7 };
  return call("GET", "/api/activity/kept");
}
export async function restoreKept(id: string): Promise<Answer<{ ok: boolean; path: string; identityConfirmed: boolean; message: string }>> {
  if (MOCK) return { error: { code: "not_available", message: "The sample install keeps no originals." } };
  return call("POST", "/api/kept/" + encodeURIComponent(id) + "/restore", {});
}
