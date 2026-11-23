import type { Tab } from "./types/jobs.ts";

// The list tab that holds a job in the given state: the drawer looks its row up there when the page it was opened
// from didn't load it (a bare ?job= link). Cancelled jobs sit in the Skipped tab with skipped ones.
export function tabForState(state?: string): Tab | null {
  switch (state) {
    case "completed": return "completed";
    case "failed": return "failed";
    case "skipped":
    case "cancelled": return "skipped";
    default: return null;
  }
}
