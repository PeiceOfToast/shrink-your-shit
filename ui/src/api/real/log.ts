// Real client, System log (GET /api/v2/log, /log/sources, /log/download; POST /log/seen). The page reads through these once the
// log flag is on (groups.ts). The download is plain text.
import type * as mock from "../mock.ts";
import { query, request, requestText, type Out } from "../http.ts";
import type { LogParams } from "../types.ts";

// The server reads detail as 1 (debug lines) or leaves it out; a boolean would go out as "true" and be refused (400).
const logQuery = (params: LogParams) => query({ ...params, detail: params.detail ? 1 : undefined });

export const getLog: typeof mock.getLog = (params = {}) => request<Out<typeof mock.getLog>>("GET", "/log" + logQuery(params));
export const getLogSources: typeof mock.getLogSources = () => request<Out<typeof mock.getLogSources>>("GET", "/log/sources");
export const getLogDownload: typeof mock.getLogDownload = (params = {}) => requestText("/log/download" + logQuery(params));
export const markLogSeen: typeof mock.markLogSeen = (seq) =>
  request<Out<typeof mock.markLogSeen>>("POST", "/log/seen", { seq });
