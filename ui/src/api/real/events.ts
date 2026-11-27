// Real client for the job log and history (GET /jobs/{id}/events, GET /jobs/{id}/history). Typed against the mock, so tsc
// checks the same signatures.
import type * as mock from "../mock.ts";
import { request, type Out } from "../http.ts";

export const getJobEvents: typeof mock.getJobEvents = (id) =>
  request<Out<typeof mock.getJobEvents>>("GET", `/jobs/${encodeURIComponent(id)}/events`);
export const getJobHistory: typeof mock.getJobHistory = (id) =>
  request<Out<typeof mock.getJobHistory>>("GET", `/jobs/${encodeURIComponent(id)}/history`);
