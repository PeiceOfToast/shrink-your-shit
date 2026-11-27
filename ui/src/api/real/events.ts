// Real client for the job log and history (GET /jobs/{id}/events, GET /jobs/{id}/history). The mock has no such
// functions yet (its job detail carries grouped logs and history), so these are typed from types.ts, not typeof mock.
import { request } from "../http.ts";
import type { HistoryLine, LogEvent } from "../types.ts";

export const getJobEvents = (id: string): Promise<LogEvent[]> =>
  request<LogEvent[]>("GET", `/jobs/${encodeURIComponent(id)}/events`);
export const getJobHistory = (id: string): Promise<HistoryLine[]> =>
  request<HistoryLine[]>("GET", `/jobs/${encodeURIComponent(id)}/history`);
