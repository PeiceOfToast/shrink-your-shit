// Needs attention (dashboard cards, slice 13 D2): one list of what needs a person, built from the page's own data.
// The card shows only when there is a row; the Status strip's "Nothing needs you" is exactly its absence (none).

export type AttentionFailure = { libraryId?: string | number | null; finishedAt?: string | null };
export type AttentionLibrary = { id: string | number; name: string; state: string };

// the Libraries page's words for a library that isn't running, and what each one means
export const LIBRARY_WORD: Record<string, [word: string, meaning: string]> = {
  no_workflow: ["No workflow", "Assign a workflow to start this library."],
  paused: ["Paused", "Nothing converts here until you start it again."],
  paused_share: ["Share not connected yet", "It starts when the network share connects."],
};

export function attentionRows<F extends AttentionFailure, L extends AttentionLibrary>(a: {
  failures: F[]; // the summary's recent failures, newest first
  libraryIds: Set<string> | null; // the libraries the page loaded; null while they have not loaded (keep every failure)
  healthShown: boolean; // a failed health check the person hasn't dismissed
  fallbacks: number; // encoder fall-back notes
  libraries: L[];
}) {
  // a failure from a library the page doesn't have (removed) is dropped; it stays in the Jobs log
  const ids = a.libraryIds;
  const loaded = ids === null ? a.failures : a.failures.filter((f) => f.libraryId != null && ids.has(String(f.libraryId)));
  const failures = loaded.slice().sort((x, y) => (y.finishedAt ?? "").localeCompare(x.finishedAt ?? "")).slice(0, 3);
  const libraries = a.libraries.filter((l) => l.state !== "active");
  const none = failures.length === 0 && !a.healthShown && a.fallbacks === 0 && libraries.length === 0;
  return { failures, health: a.healthShown, fallbacks: a.fallbacks, libraries, none };
}
