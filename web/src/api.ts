// The real server's /api, with the sign-in cookie from POST /api/login. ?mock=1 (or ?mock=fresh, a new install) loads
// the page on the mock module instead, until reload; screens with no endpoint yet always use the mock and keep the
// Prototype banner. A 401 goes to the sign-in page; it never falls back to the mock.
import { navigate } from "./router";
import type { Issue } from "./types/v1";

export const MOCK = /^(1|fresh)$/.test(new URLSearchParams(location.search).get("mock") ?? "");
export type Answer<T> = T | { error: Issue };
export const failed = <T,>(r: Answer<T>): r is { error: Issue } => typeof r === "object" && r !== null && "error" in r;

export class SignedOut extends Error {}
const OFFLINE: Issue = { code: "offline", message: "Can't reach the server. Check that it is running, then try again." };

// One request. Writes always send JSON (the server refuses other writes). Errors come back as {error: {code, message}}.
export async function call<T>(method: "GET" | "POST" | "PATCH" | "PUT" | "DELETE", path: string, body?: unknown, headers: Record<string, string> = {}): Promise<Answer<T>> {
  let r: Response;
  try {
    r = await fetch(path, method === "GET" ? { credentials: "same-origin", headers }
      : { method, credentials: "same-origin", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(body ?? {}) });
  } catch {
    return { error: OFFLINE };
  }
  if (r.status === 401 && path !== "/api/login") {
    const here = location.pathname.replace(/^\/next/, "") + location.search;
    if (!here.startsWith("/signin")) navigate("/signin?next=" + encodeURIComponent(here));
    throw new SignedOut();
  }
  const j = await r.json().catch(() => null);
  if (!r.ok) return { error: j?.error ?? { code: "http_" + r.status, message: "The server answered " + r.status + "." } };
  return j as T;
}
