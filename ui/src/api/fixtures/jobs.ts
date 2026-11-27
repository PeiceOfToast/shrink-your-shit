import type { Job } from "../types.ts";
import { finished, queuedSince, runningSince, skippedAt } from "./clock.ts";
import type { JobResult } from "../types.ts";

// A job result: sizes in bytes; the outcome and reason are what the list text is built from.
const res = (sizeBefore: number, sizeAfter: number | null, outcome: JobResult["outcome"] = null, failedStep: string | null = null, reason: string | null = null): JobResult =>
  ({ sizeBefore, sizeAfter, outcome, failedStep, reason, warnings: 0 });

// Sample rows from draft-b/jobs.html. The counts in mock.ts are the draft's; these are the rows shown.
export const JOBS: Job[] = [
  { id: "j1", state: "running", file: "Dune.Part.Two.2024.2160p.REMUX.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: { index: 3, count: 5, name: "Converting video" }, progress: { pct: 62, speed: 4_200_000, etaMs: 180_000 }, ...runningSince(18), result: res(40e9, 22e9), tileLines: ["H.264", "2160p"] },
  { id: "j2", state: "running", file: "The.Bear.S03E04.1080p.WEB-DL.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: { index: 4, count: 5, name: "Checking the result" }, progress: { pct: 94, speed: 900_000, etaMs: 30_000 }, ...runningSince(11), result: res(4.5e9, 3.4e9), tileLines: ["H.264", "1080p"] },
  { id: "j3", state: "failed", file: "Wedding.Raw.2018.mov", library: "Home videos", workflow: "Keep surround", worker: "This computer", step: { index: 3, count: 5, name: "Convert video" }, progress: null, ...finished(95, 42000), result: res(1.2e9, null, "failed", "Convert video", "the file couldn't be read") },
  { id: "j4", state: "failed", file: "Arrival.2016.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: { index: 4, count: 5, name: "Check result" }, progress: null, ...finished(150, 380000), result: res(30e9, null, "failed", "Check result", "the new file was shorter") },
  { id: "j12", state: "done", file: "Holiday.Clip.2019.1080p.mov", library: "Home videos", workflow: "Shrink videos", worker: "This computer", step: null, progress: null, ...finished(120, 90000), result: res(4e9, null, "bigger_kept", null, "The playback test of the new file played nothing from its sample, so the original is kept.") },
  { id: "j11", state: "failed", file: "The.Bear.S03E02.1080p.WEB-DL.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: { index: 4, count: 5, name: "Check result" }, progress: null, ...finished(95, 300000), result: res(4.1e9, null, "failed", "Check result", "the new file was shorter than the original") },
  { id: "j5", state: "done", file: "Oppenheimer.2023.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: null, progress: null, ...finished(200, 708000), result: res(14.4e9, 8.5e9, "smaller") },
  { id: "j6", state: "done", file: "Better.Call.Saul.S06E13.1080p.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: null, progress: null, ...finished(260, 270000), result: res(10e9, 10.8e9, "bigger_kept") },
  { id: "j7", state: "skipped", file: "Blade.Runner.2049.2017.2160p.DV.mkv", library: "Movies", workflow: "Shrink videos", worker: "", step: null, progress: null, ...skippedAt(190), result: res(22e9, null, null, null, "Dolby Vision is left alone") },
  { id: "j8", state: "done", file: "Severance.S02E07.1080p.WEB-DL.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: null, progress: null, ...finished(330, 552000), result: res(3.8e9, 2.4e9, "smaller") },
  { id: "j9", state: "waiting", file: "Ghostbusters.1984.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "", step: null, progress: null, ...queuedSince(40), result: res(12e9, null) },
  { id: "j10", state: "done", file: "Alien.1979.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: null, progress: null, ...finished(400, 365000), result: res(6e9, 2.9e9, "smaller") },
];
