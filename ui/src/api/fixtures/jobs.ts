import type { Job } from "../types.ts";
import { finished, queuedSince, runningSince, skippedAt } from "./clock.ts";

// Sample rows from draft-b/jobs.html. The counts in mock.ts are the draft's; these are the rows shown.
export const JOBS: Job[] = [
  { id: "j1", state: "running", file: "Dune.Part.Two.2024.2160p.REMUX.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: "Converting video · step 3 of 5", progress: 62, ...runningSince(18), result: "about 18 GB", resultTone: "", tileLines: ["H.264","2160p"], hint: "Converting video · step 3 of 5 · about 18 GB saving" },
  { id: "j2", state: "running", file: "The.Bear.S03E04.1080p.WEB-DL.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: "Checking the result · step 4 of 5", progress: 94, ...runningSince(11), result: "about 1.1 GB", resultTone: "", tileLines: ["H.264","1080p"], hint: "Checking the result · step 4 of 5 · about 1.1 GB saving" },
  { id: "j3", state: "failed", file: "Wedding.Raw.2018.mov", library: "Home videos", workflow: "Keep surround", worker: "This computer", step: "Convert video · the file couldn't be read", progress: null, ...finished(95, 42000), result: "—", resultTone: "" },
  { id: "j4", state: "failed", file: "Arrival.2016.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: "Check result · the new file was shorter", progress: null, ...finished(150, 380000), result: "—", resultTone: "" },
  { id: "j5", state: "done", file: "Oppenheimer.2023.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: "—", progress: null, ...finished(200, 708000), result: "41% smaller · 5.9 GB freed", resultTone: "ok" },
  { id: "j6", state: "done", file: "Better.Call.Saul.S06E13.1080p.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: "—", progress: null, ...finished(260, 270000), result: "8% bigger · original kept", resultTone: "" },
  { id: "j7", state: "skipped", file: "Blade.Runner.2049.2017.2160p.DV.mkv", library: "Movies", workflow: "Shrink videos", worker: "", step: "Dolby Vision is left alone", progress: null, ...skippedAt(190), result: "—", resultTone: "" },
  { id: "j8", state: "done", file: "Severance.S02E07.1080p.WEB-DL.mkv", library: "TV Shows", workflow: "Shrink videos", worker: "NAS box", step: "—", progress: null, ...finished(330, 552000), result: "37% smaller · 1.4 GB freed", resultTone: "ok" },
  { id: "j9", state: "waiting", file: "Ghostbusters.1984.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "", step: "Waiting for a worker", progress: null, ...queuedSince(40), result: "—", resultTone: "" },
  { id: "j10", state: "done", file: "Alien.1979.1080p.BluRay.mkv", library: "Movies", workflow: "Shrink videos", worker: "This computer", step: "—", progress: null, ...finished(400, 365000), result: "52% smaller · 3.1 GB freed", resultTone: "ok" },
];
