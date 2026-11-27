// The switch: one flag per route group (contract section 1). A group uses its real module only when its flag is on.
// Workshop's api PR flips a flag in the same PR that adds the routes. ?api=real or ?api=mock sets every group for this tab.
export const REAL = {
  access: true,
  libraries: true,
  jobs: true,
  events: true,
  summary: true,
  overview: false,
} as const;

export type Group = keyof typeof REAL;

function override(): "real" | "mock" | null {
  if (typeof location === "undefined") return null;
  const v = new URLSearchParams(location.search).get("api");
  return v === "real" || v === "mock" ? v : null;
}

export function isReal(group: Group): boolean {
  const o = override();
  if (o) return o === "real";
  return REAL[group];
}
