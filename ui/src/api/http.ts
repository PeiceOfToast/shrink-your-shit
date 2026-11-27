// The real client's one request function (contract section 2): same origin, the X-SYP header on every call, and an
// error body {error:{code, message, field?}} becomes an ApiError whose message is shown as it is.
import { setConnection } from "./connection.ts";
import { ApiError } from "./errors.ts";
import { navigate } from "../router.tsx";

export type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

// Contract section 3: the codes the client acts on. Navigation is wired for the sign-in and first-run codes; the
// screens for the rest (the message on the sign-in form, the starting screen's retry) are not live yet.
export function onRouteCode(status: number, code: string): void {
  // Already on the page the code leads to: no navigation (a 401 on /signin would otherwise loop).
  const at = location.pathname;
  if (status === 401 && code === "signed_out" && !at.startsWith("/signin")) {
    navigate(`/signin?next=${encodeURIComponent(location.pathname + location.search)}`);
  } else if (status === 403 && code === "setup_needed" && at !== "/start") {
    navigate("/start");
  } else if (status === 409 && code === "setup_done" && at !== "/signin") {
    navigate("/signin");
  }
}

export async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch("/api/v2" + path, {
      method,
      credentials: "same-origin",
      headers: { "X-SYP": "1", ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    setConnection(false);
    throw new ApiError("unreachable", "Can't reach the server.");
  }
  setConnection(true);
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    if (!res.ok) throw new ApiError("unreachable", "The server sent something unexpected.");
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string; field?: string } } | null)?.error;
    if (!err?.code || !err.message) throw new ApiError("unreachable", "The server sent something unexpected.");
    onRouteCode(res.status, err.code);
    throw new ApiError(err.code, err.message, err.field);
  }
  return data as T;
}
