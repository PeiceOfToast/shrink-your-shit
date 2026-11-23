// M3b: the pure rules behind the Workflows list's filter buttons, counts and the rows on screen. No React here, so Node can test them.
import type { Workflow } from "./types/v2";

export type WfFilter = "all" | "enabled" | "problems";

// Filter rules (from the workflow's own fields):
// - Enabled: status is "enabled". A disabled or draft workflow is only in All (and With problems if it has any).
// - With problems: validation.errors + validation.warnings > 0.
export function hasProblems(w: Workflow): boolean {
  return w.validation.errors + w.validation.warnings > 0;
}
export function inWfFilter(w: Workflow, f: WfFilter): boolean {
  if (f === "all") return true;
  if (f === "enabled") return w.status === "enabled";
  return hasProblems(w);
}
export function wfFilterCounts(ws: Workflow[]): Record<WfFilter, number> {
  const c: Record<WfFilter, number> = { all: 0, enabled: 0, problems: 0 };
  for (const w of ws) for (const f of Object.keys(c) as WfFilter[]) if (inWfFilter(w, f)) c[f]++;
  return c;
}

// The rows on screen: the filter button, then the search (name and description, any case), the tag (empty = any) and
// Favourites. Counts always come from the whole loaded list, not from this.
export function shownWorkflows(ws: Workflow[], o: { filter: WfFilter; q: string; tag: string; fav: boolean }): Workflow[] {
  const q = o.q.toLowerCase();
  return ws.filter((w) => inWfFilter(w, o.filter)
    && (!q || (w.name + " " + w.description).toLowerCase().includes(q))
    && (!o.tag || w.tags.includes(o.tag))
    && (!o.fav || w.favourite));
}
