// The first-run wizard's step indicator (design start-step1..3: "1 Add a library — 2 Create a workflow — 3 Your worker").
// Steps before the one in front are done; no React here, so Node can test it.
export type StepState = "done" | "current" | "todo";

export const stepStates = (current: number): StepState[] => [1, 2, 3].map((n) => (n < current ? "done" : n === current ? "current" : "todo"));

// The library's Done line shows a count only once a scan has counted something; before that it is just the path.
export const videoSuffix = (n: number): string => (n > 0 ? " · " + (n === 1 ? "1 video" : n + " videos") : "");
