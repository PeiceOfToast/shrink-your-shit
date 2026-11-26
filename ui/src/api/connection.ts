import { useSyncExternalStore } from "react";

// Whether the last API call reached the server. The status line shows the lost-connection text when false.
let ok = true;
const listeners = new Set<() => void>();

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
