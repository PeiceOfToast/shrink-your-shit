// Real client, jobs group (contract section 1): the list, one job, and its neighbours in the list behind the panel.
import type * as mock from "../mock.ts";
import { request, query, type Out } from "../http.ts";

export const getJobs: typeof mock.getJobs = (params = {}) =>
  request<Out<typeof mock.getJobs>>("GET", "/jobs" + query({ ...params }));
export const getJob: typeof mock.getJob = (id) => request<Out<typeof mock.getJob>>("GET", `/jobs/${encodeURIComponent(id)}`);
// POST /jobs/actions answers 200 with the ids that changed and the ones it refused, each with its sentence.
export const runJobsAction: typeof mock.runJobsAction = (action, ids) =>
  request<Out<typeof mock.runJobsAction>>("POST", "/jobs/actions", { action, ids });
// POST /jobs/{id}/convert-again answers the same job's detail, now waiting for its new attempt.
export const convertAgain: typeof mock.convertAgain = (id) =>
  request<Out<typeof mock.convertAgain>>("POST", `/jobs/${encodeURIComponent(id)}/convert-again`);
export const getJobNeighbours: typeof mock.getJobNeighbours = (id, state) =>
  request<Out<typeof mock.getJobNeighbours>>("GET", `/jobs/${encodeURIComponent(id)}/neighbours` + query({ state }));
