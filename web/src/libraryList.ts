// M3a: the pure rules behind the Libraries list's filter buttons and the progress bar. No React here, so Node can test them.
import type { Library } from "./types/v2";

export type LibFilter = "all" | "watching" | "attention" | "paused";

// Filter rules (from the status fields; the list's pills use the same ones):
// - Problem: the folder can't be written, the library has no workflow, its folder is missing or unreadable.
// - Needs attention: a Problem, or failed > 0. A library with failed jobs can be Needs attention and Watching at once.
// - Watching: started (enabled) and no Problem. Includes a library waiting for its share and one scanning.
// - Paused: enabled is off and it has been scanned before (the same test as the "Paused" pill). A library never scanned
//   and not enabled is "Not started": it is only in All.
function hasProblem(l: Library): boolean {
  return !l.writable || l.status === "no_workflow" || l.status === "path_missing" || l.status === "not_readable";
}
export function needsAttention(l: Library): boolean {
  return hasProblem(l) || l.counts.failed > 0;
}
export function inFilter(l: Library, f: LibFilter): boolean {
  if (f === "all") return true;
  if (f === "attention") return needsAttention(l);
  if (f === "watching") return l.enabled && !hasProblem(l);
  return !l.enabled && !!l.scan.lastScanAt && l.status !== "no_workflow"; // paused
}
export function filterCounts(ls: Library[]): Record<LibFilter, number> {
  const c = { all: 0, watching: 0, attention: 0, paused: 0 } as Record<LibFilter, number>;
  for (const l of ls) for (const f of Object.keys(c) as LibFilter[]) if (inFilter(l, f)) c[f]++;
  return c;
}

// Done share: converted-or-skipped files over all files, as 0..1. The server's files count (the last scan's videos)
// includes failed ones, so waiting, running and failed files are not done. With no files there is no share (null):
// the card shows no bar, never a made-up one. A library whose files all failed shows an empty bar.
export function doneShare(c: Library["counts"]): number | null {
  if (c.files <= 0) return null;
  const done = c.files - c.waiting - c.running - c.failed;
  return Math.min(1, Math.max(0, done / c.files));
}

// Select all acts on the libraries on screen only (the filter and the search). Checked when there is one shown and
// every shown one is selected.
export function allShownSelected(shown: Library[], sel: string[]): boolean {
  return shown.length > 0 && shown.every((l) => sel.includes(l.id));
}
// The selection after Select all is ticked or cleared: only the shown libraries change; a hidden one keeps its state.
export function selectShown(sel: string[], shown: Library[], on: boolean): string[] {
  const ids = new Set(shown.map((l) => l.id));
  return on ? [...new Set([...sel, ...ids])] : sel.filter((id) => !ids.has(id));
}

// When the filter or the search changes, the selection keeps only the libraries still shown (the intersection).
export function keepShown(sel: string[], shown: Library[]): string[] {
  const ids = new Set(shown.map((l) => l.id));
  return sel.filter((id) => ids.has(id));
}
