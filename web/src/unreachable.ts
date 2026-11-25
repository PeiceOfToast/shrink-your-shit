// U-B: the kept originals that Empty trash now couldn't delete. Pure rules, so they can be tested without a DOM.
import type { Unreachable } from "./types/v2.ts";

// only a file the server can't find can be forgotten; the others are still on disk or may be in use
export const canForget = (u: Unreachable) => u.code === "not_found";
// the server's reasons start lowercase (they follow "Its file" or "its" in a sentence); shown on their own, they need a capital
export const reasonLine = (reason: string) => reason.charAt(0).toUpperCase() + reason.slice(1);
export const fileName = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path;
export const unreachableLine = (n: number) => `${n} kept ${n === 1 ? "original" : "originals"} couldn't be deleted.`;
// the id of the first forgettable row at or after `from` (the list is already without the removed row), else the first one
// before it, so focus never jumps to the warning line while a Forget button is still on screen; null only when none is left
export const nextForgetId = (list: Unreachable[], from: number): number | null =>
  (list.slice(from).find(canForget) ?? list.slice(0, from).find(canForget))?.id ?? null;
