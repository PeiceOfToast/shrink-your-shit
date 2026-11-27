import type { Attention, DaySeries, Overview, Summary } from "../types.ts";
import seriesRaw from "./series.json" with { type: "json" };
import { minsAgo, plus, trashUntil } from "./clock.ts";
import { nextScanFor } from "../schedule.ts";
import { clock } from "../../format/time.ts";
import { JOBS } from "./jobs.ts";

// Numbers from draft-b/dashboard.html and draft-b/jobs.html (the status line, KPIs, cards and table).
// series.json holds the chart heights: saved[0] freed and saved[1] in the trash (GB), converted[0..2] done, skipped, failed.
const raw = seriesRaw as { saved: { v: (number | null)[] }[]; converted: { v: (number | null)[] }[] };
const toBytes = (gb: number | null) => (gb == null ? null : Math.round(gb * 1e9));
const dayString = (offset: number) => {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};
const DAYS: DaySeries[] = raw.saved[0].v.map((_, i) => ({
  day: dayString(i - (raw.saved[0].v.length - 1)),
  freedBytes: toBytes(raw.saved[0].v[i]),
  trashBytes: toBytes(raw.saved[1].v[i]),
  done: raw.converted[0].v[i],
  skipped: raw.converted[1].v[i],
  failed: raw.converted[2].v[i],
}));

export const OVERVIEW: Overview = {
  freed: {
    totalBytes: 36_400_000_000,
    todayBytes: 1_900_000_000,
    // The newest done job (finished 12 min after it started, see jobs.ts j5) plus 30 days in the trash.
    nextReleaseBytes: 3_200_000_000,
    nextReleaseAt: trashUntil(plus(minsAgo(200), 708_000)),
    inTrashBytes: 61_000_000_000,
  },
  converted: { total: 4812, perDay: 11 },
  waiting: { count: 1284, nextScanAt: nextScanFor("daily") },
  failed24h: { count: 3, pct: 0.6 },
  avgShrink: { pct: 38, secPerGB: 130 },
  series: { days: DAYS },
  running: JOBS.filter((j) => j.state === "running"),
  workersOn: true,
  last24h: { done: 14, skipped: 2, failed: 3 },
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
  seenSeq: 0,
  seenAt: null,
  healthFailing: false,
};
