// U-B: the kept originals that Empty trash now couldn't delete. Pure rules, so they can be tested without a DOM.
import type { Unreachable } from "./types/v2.ts";

// only a file the server can't find can be forgotten; the others are still on disk or may be in use
export const canForget = (u: Unreachable) => u.code === "not_found";
export const fileName = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path;
export const unreachableLine = (n: number) => `${n} kept ${n === 1 ? "original" : "originals"} couldn't be deleted.`;
// the id of the first row at or after `from` that can be forgotten, or null when none is left
export const nextForgetId = (list: Unreachable[], from: number): number | null => list.slice(from).find(canForget)?.id ?? null;
