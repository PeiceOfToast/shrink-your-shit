// Tells the shell that the status line's numbers changed (for example, the System log was opened and new errors are seen).
const listeners = new Set<() => void>();

export function notifySummaryChanged(): void {
  listeners.forEach((cb) => cb());
}

export function onSummaryChanged(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}
