// One refresh for every open page: a live event (or a resync) asks each mounted useApi to load again. No page changes.
const listeners = new Set<() => void>();

export function onRefresh(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function refreshAll(): void {
  listeners.forEach((cb) => cb());
}
