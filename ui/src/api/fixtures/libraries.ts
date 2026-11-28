import type { Library, WorkflowSummary } from "../types.ts";
import { localAt, minsAgo } from "./clock.ts";
import { nextScanFor } from "../schedule.ts";

// The Home videos share's id (page 09 shareId, not the library id). Attention's reconnect path uses it.
export const HOME_VIDEOS_SHARE = "home-videos-share";

// Draft-b libraries table (draft-b/libraries.html) as page 09 "Data and API" rows. One folder per library.
export const LIBRARIES: Library[] = [
  { id: "movies", name: "Movies", path: "/media/movies", workflow: { id: "shrink-videos", name: "Shrink videos" }, files: 1204, waiting: 640, savedBytes: 8.1e9, savedPct: 0.4, lastScan: { at: minsAgo(20), found: 1204, new: 3, changed: 0 }, status: "watching", watch: "daily", paused: false, nextScanAt: nextScanFor("daily") },
  { id: "tv", name: "TV Shows", path: "/media/tv", workflow: { id: "shrink-videos", name: "Shrink videos" }, files: 2430, waiting: 598, savedBytes: 0, savedPct: 0, lastScan: { at: minsAgo(18), found: 2430, new: 12, changed: 1 }, status: "scanning", scanPct: 40, watch: "hourly", paused: false, nextScanAt: nextScanFor("hourly") },
  { id: "home-videos", name: "Home videos", path: "/media/home-videos", shareId: HOME_VIDEOS_SHARE, workflow: { id: "keep-surround", name: "Keep surround" }, files: 164, waiting: 46, savedBytes: 28.3e9, savedPct: 29, lastScan: { at: localAt(-1, 22, 10), found: 164, new: 0, changed: 0 }, status: "share_down", shareDownAt: minsAgo(40), watch: "weekly", paused: true, nextScanAt: null },
];

// The chosen workflow's three lines in the library panel (a-side-panel.html). The list itself is fixtures/workflowRows.ts.
// "original" for Keep surround is an assumption (its draft has no line for it).
export const WORKFLOW_SUMMARY: Record<string, WorkflowSummary["summary"]> = {
  "shrink-videos": { converts: "Converts files over 1 GB to HEVC on the graphics card, at Balanced quality.", leavesAlone: "Leaves alone files under 1 GB, samples and extras.", original: "The original goes to the trash for 30 days." },
  "keep-surround": { converts: "Converts to HEVC and keeps every 5.1 and 7.1 audio track.", leavesAlone: "Leaves alone files that are already HEVC.", original: "The original goes to the trash for 30 days." },
  "shrink-anime": { converts: "Converts files over 300 MB to HEVC tuned for animation, at Smaller quality.", leavesAlone: "Leaves alone files under 300 MB, openings and endings.", original: "The original goes to the trash for 30 days." },
};

// Preview counts per workflow (draft: "702 of 1,108 would be converted (2.4 TB); 406 left alone.").
export const PREVIEW: Record<string, { wouldConvert: number; total: number; skipped: { reason: string; count: number }[] }> = {
  "shrink-videos": { wouldConvert: 702, total: 1108, skipped: [{ reason: "nothing_to_do", count: 406 }] },
  "keep-surround": { wouldConvert: 737, total: 1108, skipped: [{ reason: "nothing_to_do", count: 371 }] },
  "shrink-anime": { wouldConvert: 702, total: 1108, skipped: [{ reason: "nothing_to_do", count: 406 }] },
};

// The folder browser on this server (a-side-panel.html tree). note is what the draft shows beside the folder.
export const FOLDERS: Record<string, { name: string; parent: string | null; children: string[]; note?: string; videos?: number; sizeBytes?: number }> = {
  "/media": { name: "/media", parent: null, children: ["/media/movies", "/media/tv", "/media/anime", "/media/concerts"] },
  "/media/movies": { name: "movies", parent: "/media", children: ["/media/movies/4k"], note: "library: Movies", videos: 1204, sizeBytes: 2.9e12 },
  "/media/movies/4k": { name: "4k", parent: "/media/movies", children: [], note: "inside Movies", videos: 88, sizeBytes: 0.9e12 },
  "/media/tv": { name: "tv", parent: "/media", children: [], note: "library: TV Shows", videos: 2430, sizeBytes: 4.2e12 },
  "/media/anime": { name: "anime", parent: "/media", children: [], note: "1,108 videos", videos: 1108, sizeBytes: 2.4e12 },
  "/media/concerts": { name: "concerts", parent: "/media", children: [], note: "86 videos", videos: 86, sizeBytes: 0.2e12 },
  "/downloads": { name: "/downloads", parent: null, children: [] },
};

export const SHARE_FOLDERS = ["anime", "concerts", "movies", "tv"];
