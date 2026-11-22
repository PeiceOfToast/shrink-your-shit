// A non-OK answer as an Issue, so a failed save can never read as a success. Kept apart from api.ts so the tests
// can import it under node --test (api.ts touches window and location at load).
import type { Issue } from "./types/v1";

export function httpError(status: number, body: unknown): Issue {
  const e = typeof body === "object" && body !== null ? (body as { error?: unknown }).error : undefined;
  if (typeof e === "object" && e !== null && typeof (e as { message?: unknown }).message === "string") return e as Issue;
  if (typeof e === "string" && e.trim() !== "") return { code: "http_" + status, message: e };
  return { code: "http_" + status, message: "The server answered " + status + "." };
}
