// M1: the top bar's "Converting N files" link and the red problem count on the Workers nav item. Pure, so the node tests
// run them. The plural is inlined (format.ts's plural is the same): node's test runner cannot import it without a .ts path.
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

// "Converting 2 files", or null when nothing is converting (the link is hidden).
export function convertingLabel(n: number | undefined): string | null {
  return n && n > 0 ? "Converting " + plural(n, "file", "files") : null;
}

// The red count on Workers: how many workers have a problem (offline or stalled), or null when none.
export function problemWorkers(n: number | null | undefined): { count: number; words: string } | null {
  return n && n > 0 ? { count: n, words: plural(n, "worker", "workers") + " with a problem" } : null;
}
