import type { WorkflowRow } from "../types.ts";
import { minsAgo } from "./clock.ts";

// The workflow list rows (page 04). ids and names match the library panel's workflows (fixtures/libraries.ts);
// usedBy is not stored here: the mock derives it from the libraries, so it always matches the Libraries page.
// shrink-old-tv is an extra row that shows the "problems" state; no library uses it.
export const WORKFLOW_ROWS: (Omit<WorkflowRow, "usedBy">)[] = [
  { id: "shrink-videos", name: "Shrink videos", description: "Converts big files to HEVC on the graphics card", kind: "video", updatedAt: minsAgo(60 * 26), state: "published", problemCount: 0, madeWith: "guided", fitsGuided: true },
  { id: "keep-surround", name: "Keep surround", description: "Converts to HEVC and keeps the 5.1 and 7.1 audio", kind: "video", updatedAt: minsAgo(60 * 3), state: "published", problemCount: 0, madeWith: "guided", fitsGuided: true },
  { id: "shrink-anime", name: "Shrink anime", description: "HEVC tuned for animation", kind: "video", updatedAt: minsAgo(45), state: "draft", problemCount: 0, madeWith: "guided", fitsGuided: true },
  { id: "shrink-old-tv", name: "Shrink old TV", description: "Smaller files for old episodes", kind: "video", updatedAt: minsAgo(12), state: "problems", problemCount: 2, madeWith: "editor", fitsGuided: false },
];
