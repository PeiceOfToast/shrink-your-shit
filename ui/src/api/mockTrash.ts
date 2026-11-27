// Settings > Trash (page 11): the originals kept after a conversion. Restore puts the original back and removes the
// converted file. Three refusals (API: GET /trash, POST /trash/{id}/restore, 409 codes in Appendix A).
import { ApiError } from "./errors.ts";
import { respond } from "./mockState.ts";
import type { TrashItem } from "./types.ts";

let items: TrashItem[] = [
  { id: "t-media", file: "media.mkv", library: "Movies", trashedAt: "2026-11-27T02:00:00Z", daysLeft: 7 },
  { id: "t-gone", file: "The.Bear.S03E01.mkv", library: "TV Shows", trashedAt: "2026-11-26T20:10:00Z", daysLeft: 6 },
  { id: "t-changed", file: "Dune.2024.mkv", library: "Movies", trashedAt: "2026-11-26T09:30:00Z", daysLeft: 6 },
  { id: "t-taken", file: "Holiday.mkv", library: "Home videos", trashedAt: "2026-11-25T18:45:00Z", daysLeft: 5 },
  { id: "t-old", file: "Old.Show.S01E02.mkv", library: "TV Shows", trashedAt: "2026-11-21T08:00:00Z", daysLeft: 1 },
];

// The sample items for the three refusals. Each one answers with its code and sentence.
const REFUSALS: Record<string, { code: string; message: string }> = {
  "t-gone": { code: "original_gone", message: "The original isn't in the trash any more." },
  "t-changed": { code: "converted_changed", message: "The converted file was changed after it was made, so it's left alone." },
  "t-taken": { code: "name_taken", message: "Another file now has this name in the folder." },
};

// GET /trash: newest first.
export function getTrash(): Promise<TrashItem[]> {
  return respond(() => [...items].sort((a, b) => b.trashedAt.localeCompare(a.trashedAt)).map((i) => ({ ...i })));
}

// After one of these, a Restore on the same row can't work again: the row goes at the next reload. name_taken can
// be retried once the other file is renamed.
const NO_RETRY = new Set(["original_gone", "converted_changed"]);
export function restoreCanRetry(code: string): boolean {
  return !NO_RETRY.has(code);
}

// POST /trash/{id}/restore: the original goes back and the converted file is removed; the row leaves the list.
export function restoreTrash(id: string): Promise<{ ok: true }> {
  return respond(() => {
    const hit = items.find((i) => i.id === id);
    if (!hit) throw new ApiError("trash_not_found", "That file isn't in the trash.");
    const refusal = REFUSALS[id];
    if (refusal) throw new ApiError(refusal.code, refusal.message);
    items = items.filter((i) => i.id !== id);
    return { ok: true as const };
  });
}
