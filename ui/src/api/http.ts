// The real client's one request function (contract section 2): same origin, the X-SYP header on every call, and an
// error body {error:{code, message, field?}} becomes an ApiError whose message is shown as it is.
import { setConnection } from "./connection.ts";
import { ApiError } from "./errors.ts";

export type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

// The app sets the navigator once (main.tsx, to the router's navigate). Kept out of this module so the request code
// runs in a plain node test without the router's JSX.
let navigator: (to: string) => void = () => {};
export function setCodeNavigator(fn: (to: string) => void): void {
  navigator = fn;
}

// Contract section 3: the codes the client acts on. Navigation is wired for the sign-in and first-run codes. 503
// starting is not navigated here: the starting screen shows it and retries every 2 s (its screen is not live yet).
export function onRouteCode(status: number, code: string): void {
  const here = typeof location === "undefined" ? "/" : location.pathname + location.search;
  const at = typeof location === "undefined" ? "/" : location.pathname;
  // Already on the page the code leads to: no navigation (a 401 on /signin would otherwise loop).
  if (status === 401 && code === "signed_out" && !at.startsWith("/signin")) {
    navigator(`/signin?next=${encodeURIComponent(here)}`);
  } else if (status === 403 && code === "setup_needed" && at !== "/start") {
    navigator("/start");
  } else if (status === 409 && code === "setup_done" && at !== "/signin") {
    navigator("/signin");
  }
}

// opts.signal lets a page stop a request. A stopped request rejects with its AbortError, and the page decides what to show.
export interface RequestOpts {
  signal?: AbortSignal;
}

// One fetch to the API: the same-origin request with the X-SYP header, and the body read as text. A failed call to the
// server (no answer) is "unreachable"; anything the server answered is returned for the caller to read.
async function send(method: Method, path: string, body: unknown, opts: RequestOpts): Promise<{ res: Response; text: string }> {
  let res: Response;
  try {
    res = await fetch("/api/v2" + path, {
      method,
      credentials: "same-origin",
      headers: { "X-SYP": "1", ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: opts.signal,
    });
  } catch (e) {
    if (opts.signal?.aborted) throw e;
    setConnection(false);
    throw new ApiError("unreachable", "Can't reach the server.");
  }
  setConnection(true);
  return { res, text: await res.text() };
}

// An error answer: the server's {error:{code, message, field}} becomes an ApiError whose message is shown as it is.
function refuse(res: Response, text: string): never {
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    throw new ApiError("unreachable", "The server sent something unexpected.");
  }
  const err = (data as { error?: { code?: string; message?: string; field?: string } } | null)?.error;
  if (!err?.code || !err.message) throw new ApiError("unreachable", "The server sent something unexpected.");
  onRouteCode(res.status, err.code);
  throw new ApiError(err.code, err.message, err.field);
}

export async function request<T>(method: Method, path: string, body?: unknown, opts: RequestOpts = {}): Promise<T> {
  const { res, text } = await send(method, path, body, opts);
  if (!res.ok) refuse(res, text);
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    if (!res.ok) throw new ApiError("unreachable", "The server sent something unexpected.");
  }
  return data as T;
}

// A plain-text answer (the log download): the text as it is.
export async function requestText(path: string): Promise<string> {
  const { res, text } = await send("GET", path, undefined, {});
  if (!res.ok) refuse(res, text);
  return text;
}

// A query string from the defined values: undefined, null and "" are left out.
export function query(params: Record<string, unknown>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    q.set(k, String(v));
  }
  const s = q.toString();
  return s ? "?" + s : "";
}

// The return type of a mock function, so a real module types its result the same way as the mock.
export type Out<F> = F extends (...a: never[]) => Promise<infer T> ? T : never;
