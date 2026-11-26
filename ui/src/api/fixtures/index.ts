import type { Attention, Overview, Summary } from "../types.ts";
import seriesRaw from "./series.json" with { type: "json" };
import { minsAgo, plus, trashUntil } from "./clock.ts";
import { nextScanFor } from "../schedule.ts";
import { clock } from "../../format/time.ts";

// Numbers from draft-b/dashboard.html and draft-b/jobs.html (the status line, KPIs, cards and table).
export const SERIES = seriesRaw as { saved: Overview["savedSeries"]; converted: Overview["filesSeries"] };

export const OVERVIEW: Overview = {
  freedGB: 36.4,
  freedTodayGB: 1.9,
  converted: 4812,
  convertedPerDay: 11,
  waiting: 1284,
  nextScan: nextScanFor("daily"),
  failed24h: 3,
  failedRatePct: 0.6,
  avgShrinkPct: 38,
  convertSecPerGB: 130,
  running: 2,
  workersOn: true,
  trashGB: 61,
  savedMoreGB: 3.2,
  // The newest done job (finished 12 min after it started, see jobs.ts j5) plus 30 days in the trash.
  savedMoreAt: trashUntil(plus(minsAgo(200), 708_000)),
  savedSeries: SERIES.saved,
  filesSeries: SERIES.converted,
  filesLast24h: { done: 14, skipped: 2, failed: 3 },
  libraries: [],
};

export const ATTENTION: Attention[] = [
  {
    id: "enc",
    tile: { icon: "alert", tone: "bad" },
    title: "This computer's graphics encoder failed 3 times today",
    hint: { text: `It converts on the processor until ${clock(plus(minsAgo(0), 2 * 60 * 60_000))}, which is slower.`, link: { label: "Test the encoders", action: "test-encoders" } },
    side: { pill: { label: "Slower", tone: "warn" } },
  },
  {
    id: "bear",
    tile: { lines: ["HEVC", "1080p"] },
    title: "The.Bear.S03E02.1080p.WEB-DL.mkv",
    titleHref: "/jobs?state=failed",
    chips: [{ icon: "folder", label: "TV Shows" }, { icon: "flow", label: "Shrink videos" }],
    side: { text: { label: "The new file was shorter than the original", tone: "bad" }, pill: { label: "Failed", tone: "bad", icon: "alert" } },
  },
  {
    id: "share",
    tile: { icon: "lib", tone: "warn" },
    title: "Home videos",
    titleHref: "/libraries",
    hint: { text: `Share not connected since ${clock(minsAgo(40))}. Nothing from it converts until it is.` },
    side: { pill: { label: "Share not connected", tone: "warn" } },
  },
];

export const SUMMARY: Summary = {
  converting: { running: 2, workers: 3 },
  queue: 1284,
  lastDoneAt: minsAgo(4),
  nextScanAt: nextScanFor("daily"),
  diskFreeBytes: 412e9,
  failed24h: 3,
  workersOffline: 1,
  newErrors: 0,
  healthFailing: false,
};
