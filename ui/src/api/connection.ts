import { useSyncExternalStore } from "react";
import { ApiError } from "./errors.ts";

// Whether the last API call reached the server. The status line shows the lost-connection text when false.
let ok = true;
const listeners = new Set<() => void>();

// What a failed page load says about the connection. Only "the server can't be reached" means it is lost: an
// error the server answered (a 404, a 400, a 403) proves the connection is up, and http.ts already recorded that.
export function connectionAfterError(e: unknown): boolean | null {
  if (e instanceof ApiError) return e.code === "unreachable" ? false : null;
  return null;
}

export function setConnection(next: boolean): void {
  if (next === ok) return;
  ok = next;
  listeners.forEach((cb) => cb());
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function useConnectionOk(): boolean {
  return useSyncExternalStore(subscribe, () => ok, () => true);
}
