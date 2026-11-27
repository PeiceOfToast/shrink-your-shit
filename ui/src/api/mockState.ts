// The dev state switcher's forced state, and the delay every mock call waits. Shared by the mock modules.
import { ApiError } from "./errors.ts";

export type ForcedState = "none" | "empty" | "loading" | "error" | "running" | "fresh" | "setup" | "starting" | "update";

let forced: ForcedState = "none";
const listeners = new Set<() => void>();

// Set when the first run was finished without a library: the dashboard then shows its first-run look (page 13).
let freshInstall = false;
export function setFreshInstall(v: boolean): void {
  freshInstall = v;
}
export function isFreshInstall(): boolean {
  return freshInstall;
}
// No library yet: the dev "Empty" or "Fresh install" state, or a first run finished with Skip. Every screen reads this one check.
export function noLibrariesYet(): boolean {
  const s = getForcedState();
  return s === "empty" || s === "fresh" || freshInstall;
}

export function setForcedState(state: ForcedState): void {
  forced = state;
  listeners.forEach((cb) => cb());
}

export function getForcedState(): ForcedState {
  return forced;
}

export function onForcedStateChange(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export const DELAY_MS = 300;

// Resolves with value() after a short delay. In the forced error state it throws (a page's own call fails);
// loading never resolves. Pass failInError: false for calls the status line makes, so it can still show counts.
export async function respond<T>(value: () => T, opts: { failInError?: boolean } = {}): Promise<T> {
  if (forced === "loading") return new Promise<T>(() => {});
  await new Promise((r) => setTimeout(r, DELAY_MS));
  if (forced === "error" && opts.failInError !== false) throw new ApiError("unreachable", "Can't reach the server · trying again");
  return value();
}
