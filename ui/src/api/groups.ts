// The switch: one flag per route group (contract section 1). A group uses its real module only when its flag is on.
// Workshop's api PR flips a flag in the same PR that adds the routes. ?api=real or ?api=mock sets every group for this tab.
export const REAL = {
  access: true,
  libraries: true,
  jobs: true,
  events: true,
  summary: true,
  workflows: true,
  overview: true,
  settings: true,
  trash: true,
} as const;

export type Group = keyof typeof REAL;

function override(): "real" | "mock" | null {
  if (typeof location === "undefined") return null;
  const v = new URLSearchParams(location.search).get("api");
  return v === "real" || v === "mock" ? v : null;
}

// The ?api override as the page loaded: the api object is built once from it, so the banners read the same value.
const atLoad = override();

// True where a page's data is the mock's made-up sample in this build: its group is off (or it has none yet) and the
// tab isn't on ?api=mock, where everything is sample on purpose.
export function sampleOnly(group?: Group): boolean {
  if (atLoad === "mock") return false;
  if (!group) return true;
  return atLoad ? atLoad !== "real" : !REAL[group];
}

export function isReal(group: Group): boolean {
  const o = override();
  if (o) return o === "real";
  return REAL[group];
}
