import type { WorkerDetail } from "../types.ts";
import { minsAgo } from "./clock.ts";

// The three workers the status line counts ("converting 2 of 3 workers", one offline). Ids and names match the
// Dashboard's Converting now ("This computer", "NAS box") and the jobs fixtures. The NAS box address is a mock value.
export const WORKERS: WorkerDetail[] = [
  {
    id: "this-computer",
    name: "This computer",
    address: "",
    local: true,
    state: "converting",
    since: null,
    slots: 2,
    gpuSlots: 2,
    gpu: { name: "Intel (QSV)" },
    now: { jobId: "j1", file: "Dune.Part.Two.2024.2160p.REMUX.mkv", step: { index: 3, count: 5, name: "Converting video" } },
    doneToday: 9,
    hours: { from: 0, to: 24 },
    encoder: "auto",
    libraries: [],
  },
  {
    id: "nas-box",
    name: "NAS box",
    address: "10.10.2.30",
    local: false,
    state: "converting",
    since: null,
    slots: 1,
    gpuSlots: 0,
    gpu: null,
    now: { jobId: "j2", file: "The.Bear.S03E04.1080p.WEB-DL.mkv", step: { index: 4, count: 5, name: "Checking the result" } },
    doneToday: 5,
    hours: { from: 0, to: 24 },
    encoder: "processor",
    libraries: [
      { libraryId: "movies", name: "Movies", serverPath: "/media/movies", workerPath: "/volume1/media/movies", mode: "read_write", test: { state: "ok", at: minsAgo(3) } },
      { libraryId: "tv", name: "TV Shows", serverPath: "/media/tv", workerPath: "/volume1/media/tv", mode: "read_write", test: { state: "untested" } },
    ],
  },
  {
    id: "office-pc",
    name: "Office PC",
    address: "10.10.2.41",
    local: false,
    state: "offline",
    since: minsAgo(95),
    slots: 1,
    gpuSlots: 0,
    gpu: null,
    now: null,
    doneToday: 0,
    hours: { from: 9, to: 17 },
    encoder: "auto",
    libraries: [],
  },
];
