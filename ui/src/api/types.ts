import type { IconName } from "../ui/icon-paths.ts";

// Response shapes for the routes in each page's "Data and API" section (Plan/pages).

export type JobState = "waiting" | "running" | "done" | "failed" | "skipped";

export interface Series {
  c: "ok" | "accent" | "text2" | "bad";
  v: (number | null)[];
}

export interface Overview {
  freedGB: number;
  freedTodayGB: number;
  converted: number;
  convertedPerDay: number;
  waiting: number;
  nextScan: string;
  failed24h: number;
  failedRatePct: number;
  avgShrinkPct: number;
  convertSecPerGB: number;
  running: number;
  workersOn: boolean;
  trashGB: number;
  savedMoreOn16Nov: number;
  savedSeries: Series[];
  filesSeries: Series[];
  filesLast24h: { done: number; skipped: number; failed: number };
  libraries: LibrarySummary[];
}

export interface LibrarySummary {
  id: string;
  name: string;
  path: string;
  workflow: string;
  files: number;
  waiting: number;
  saved: string;
  status: { kind: "ok" | "run" | "warn"; label: string };
}

export interface Attention {
  id: string;
  tile: { icon: IconName; tone: "bad" | "warn" | "" } | { lines: string[] };
  title: string;
  titleHref?: string;
  chips?: { icon?: IconName; label: string }[];
  hint?: { text: string; link?: { label: string; href: string } };
  side: { text?: { label: string; tone: "bad" | "" }; pill: { label: string; tone: "bad" | "warn" | "ok" | ""; icon?: IconName } };
}

export interface Summary {
  converting: { running: number; workers: number };
  queue: number;
  lastDoneAt: string;
  nextScanAt: string;
  diskFreeBytes: number;
  failed24h: number;
  workersOffline: number;
  newErrors: number;
  healthFailing: boolean;
}

export interface Job {
  id: string;
  state: JobState;
  file: string;
  library: string;
  workflow: string;
  worker: string;
  step: string;
  progress: number | null;
  when: string;
  result: string;
  resultTone: "ok" | "bad" | "";
  // Converting-now card (dashboard) only.
  tileLines?: string[];
  hint?: string;
  started?: string;
}

export interface JobCounts {
  all: number;
  waiting: number;
  running: number;
  failed: number;
  done: number;
  skipped: number;
}

export interface JobsPage {
  counts: JobCounts;
  items: Job[];
  next: string | null;
}

export interface JobsParams {
  state?: JobState | "all";
  limit?: number;
  cursor?: string;
}
