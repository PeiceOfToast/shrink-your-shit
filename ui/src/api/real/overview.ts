// Real client, overview (GET /overview): the Dashboard. Stage 1 fills every field from what exists (workshop, Q1).
import type * as mock from "../mock.ts";
import { request, type Method, type Out } from "../http.ts";
import { ApiError } from "../errors.ts";

export const getOverview: typeof mock.getOverview = () => request<Out<typeof mock.getOverview>>("GET", "/overview");

// Needs attention (GET /attention): an empty list in stage 1; the Dashboard must not show the mock's rows beside real data.
export const getAttention: typeof mock.getAttention = () => request<Out<typeof mock.getAttention>>("GET", "/attention");

// A Needs attention row's action: the server names its method, path and body. The path starts with /api/v2, which request()
// adds, so it is sent without that prefix. Only a reply with a line shows one.
const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"];
export const runAction: typeof mock.runAction = async (method, path, body) => {
  const prefix = "/api/v2";
  if (!path.startsWith(prefix + "/") || !METHODS.includes(method)) {
    throw new ApiError("invalid_request", "That action isn't one this app can send.");
  }
  const reply = await request<unknown>(method as Method, path.slice(prefix.length), body);
  const line = (reply as { line?: unknown } | null | undefined)?.line;
  return typeof line === "string" ? { line } : {};
};
