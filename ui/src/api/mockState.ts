// The dev state switcher's forced state, and the delay every mock call waits. Shared by the mock modules.
import { ApiError } from "./errors.ts";

export type ForcedState = "none" | "empty" | "loading" | "error" | "running" | "fresh";

let forced: ForcedState = "none";
const listeners = new Set<() => void>();

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

const DELAY_MS = 300;

// Resolves with value() after a short delay. In the forced error state it throws (a page's own call fails);
// loading never resolves. Pass failInError: false for calls the status line makes, so it can still show counts.
export async function respond<T>(value: () => T, opts: { failInError?: boolean } = {}): Promise<T> {
  if (forced === "loading") return new Promise<T>(() => {});
  await new Promise((r) => setTimeout(r, DELAY_MS));
  if (forced === "error" && opts.failInError !== false) throw new ApiError("unreachable", "Can't reach the server · trying again");
  return value();
}
