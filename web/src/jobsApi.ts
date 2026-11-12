// V6 Jobs on the real /api/v2/jobs (#113), or the sample jobs with ?mock=1 (mock/jobs.ts).
import { call, failed, MOCK, type Answer } from "./api";
import { mockAction, mockJob, mockJobs, mockConvertAgain } from "./mock/jobs";
import type { ActionResult, JobAction, JobDetail, JobsPage, Tab, Pin } from "./types/jobs";

export async function listJobs(tab: Tab, q: string): Promise<Answer<JobsPage>> {
  if (MOCK) return mockJobs(tab, q);
  // #134: the Waiting tab in the order the worker takes files, each with its position; the page never reorders it
  return call("GET", "/api/v2/jobs?tab=" + tab + (tab === "waiting" ? "&sort=queue" : "") + "&limit=200" + (q ? "&q=" + encodeURIComponent(q) : ""));
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
