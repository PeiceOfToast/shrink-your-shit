// V6 Jobs on the real /api/v2/jobs (#113), or the sample jobs with ?mock=1 (mock/jobs.ts).
import { call, failed, MOCK, type Answer } from "./api";
import { mockAction, mockJob, mockJobs } from "./mock/jobs";
import type { ActionResult, JobAction, JobDetail, JobsPage, Tab } from "./types/jobs";

export async function listJobs(tab: Tab, q: string): Promise<Answer<JobsPage>> {
  if (MOCK) return mockJobs(tab, q);
  return call("GET", "/api/v2/jobs?tab=" + tab + "&limit=200" + (q ? "&q=" + encodeURIComponent(q) : ""));
}
export async function getJob(id: string): Promise<Answer<JobDetail>> {
  if (MOCK) return mockJob(id) ?? { error: { code: "not_found", message: "There is no such job." } };
  return call("GET", "/api/v2/jobs/" + encodeURIComponent(id));
}
export async function jobAction(action: JobAction, ids: string[], priority?: number): Promise<Answer<{ results: ActionResult[] }>> {
  if (MOCK) return mockAction(action, ids, priority);
  const r = await call<{ results: ActionResult[] }>("POST", "/api/v2/jobs/actions", { action, ids, ...(priority != null && { priority }) });
  return failed(r) ? r : r;
}
