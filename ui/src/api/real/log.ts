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

// Follow: the server has no events frame for log lines, so the page polls the log. The first poll only remembers the
// newest seq (the page already shows those lines); later polls emit the lines above it, oldest first. The page's own
// filter decides what shows (detail=1 sends the debug lines too). A failed poll is skipped; the next tick retries.
export const subscribeAppEvents: typeof mock.subscribeAppEvents = (cb) => {
  let seen = -1;
  let busy = false;
  const poll = async (): Promise<void> => {
    if (busy) return;
    busy = true;
    try {
      const page = await getLog({ limit: 50, detail: true });
      const top = page.items.reduce((m, e) => Math.max(m, e.seq), 0);
      if (seen < 0) {
        seen = top;
        return;
      }
      for (const e of page.items.filter((x) => x.seq > seen).sort((a, b) => a.seq - b.seq)) cb(e);
      seen = Math.max(seen, top);
    } catch {
      // Skipped: the next tick polls again.
    } finally {
      busy = false;
    }
  };
  void poll();
  const timer = setInterval(() => void poll(), 4000);
  return () => clearInterval(timer);
};
