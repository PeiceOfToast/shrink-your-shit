// Real client, System log (GET /api/v2/log, /log/sources, /log/download; POST /log/seen). The page reads through these once the
// log flag is on (groups.ts). The download is plain text.
import type * as mock from "../mock.ts";
import { query, request, requestText, type Out } from "../http.ts";

export const getLog: typeof mock.getLog = (params = {}) =>
  request<Out<typeof mock.getLog>>("GET", "/log" + query({ ...params }));
export const getLogSources: typeof mock.getLogSources = () => request<Out<typeof mock.getLogSources>>("GET", "/log/sources");
export const getLogDownload: typeof mock.getLogDownload = (params = {}) => requestText("/log/download" + query({ ...params }));
export const markLogSeen: typeof mock.markLogSeen = (seq) =>
  request<Out<typeof mock.markLogSeen>>("POST", "/log/seen", { seq });
