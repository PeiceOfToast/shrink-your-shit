// GET /api/v2/overview (#177, user review 11-12 item 5): the Dashboard as an overview. Per library the NET figures of
// the Libraries page (saved = freed; bytesBefore = bytesNow + freed); the trash is GROSS and never added to them; CPU/GPU
// null = not measured yet; a day before history is recorded:false (not recorded, never estimated); days are the
// server's (timeZone). ?mock=1 builds the same fields from the sample install, nothing more.
import { call, MOCK, type Answer } from "./api";
import * as mock from "./mock";
import type { WorkerStatus } from "./types/v2";

export interface OverviewJob { jobId: string; name: string; percent: number | null; fps: number | null; speed: number | null; etaS: number | null }
export interface Overview {
  headline: { freed: number; waitingToFree: number; nextFreeAt: string | null; couldFree: number; grew?: number; filesConverted: number; filesWaiting: number };
  trash: { bytes: number; files: number; nextDeleteAt: string | null };
  workers: { id: string; name: string; kind: string; status: WorkerStatus; slots: { cpu?: { used: number; total: number }; used?: number; total?: number }; jobs: OverviewJob[]; cpuPercent: number | null; gpuPercent: number | null }[];
  active: { jobId: string; library: { id: string; name: string } | null; name: string; percent: number | null; etaS: number | null; expectedSavingBytes: number | null; expectedSavingEstimated?: boolean }[];
  libraries: { id: string; name: string; state: "active" | "paused" | "paused_share"; bytesBefore: number; bytesNow: number; saved: number; savedPercent: number | null; waitingToFree: number; couldFree: number; grew?: number;
    counts: { done: number; waiting: number; skipped: number; failed: number } }[];
  daily: { day: string; recorded: boolean; completed: number; skipped: number; failed: number; freedBytes: number; savedBytes?: number; waitingBytes?: number; couldFreeBytes?: number }[];
  // workshop #309 (11-19): savedBytes = NET saving of conversions finished that day; waitingBytes = the part of it whose
  // original is still in the trash (<= savedBytes). freedBytes is dated by the delete day. Optional until #309 merges.
  // workshop #367: couldFreeBytes = the part of savedBytes whose original was kept in place, dated like savedBytes.
  timeZone: string;
  averages: { fileShrinkPercent: number | null; secondsPerGB: number | null };
  // #323: graphics-encoder fall-backs today, per worker; pausedUntil is set after three in a row (an hour on the processor)
  encoderFallbacks?: { workerId: string; worker: string; encoder: string; lastError: string; count: number; at: string; pausedUntil: string | null }[];
}

export function getOverview(): Promise<Answer<Overview>> {
  return MOCK ? Promise.resolve(sample()) : call<Overview>("GET", "/api/v2/overview");
}

// The sample install as an overview: the agreed fields only, from the sample's own workers, jobs and libraries.
function sample(): Overview {
  const s = mock.mockState(), sum = mock.summary(s), GB = 1e9;
  const jobs = sum.activeJobs.map((j) => ({ jobId: j.id, name: j.file.name, percent: j.progress?.percent ?? null, fps: j.progress?.fps ?? null, speed: j.progress?.speed ?? null, etaS: j.progress?.etaS ?? null }));
  const days = Array.from({ length: 30 }, (_, i) => { const d = new Date(Date.now() - (29 - i) * 864e5); return d.toISOString().slice(0, 10); });
  const libs = s.libraries, tot = (f: (l: (typeof libs)[number]) => number) => libs.reduce((a, l) => a + f(l), 0);
  const fell = new URLSearchParams(location.search).has("fallback"); // ?mock=1&fallback: a paused graphics encoder
  return {
    headline: { freed: tot((l) => l.bytes.freed), waitingToFree: tot((l) => l.bytes.waitingToFree), nextFreeAt: s.trash.nextDeleteAt, couldFree: 0, grew: tot((l) => l.bytes.grew ?? 0),
      filesConverted: tot((l) => l.counts.completed), filesWaiting: tot((l) => l.counts.waiting) },
    trash: { bytes: s.trash.bytes, files: s.trash.files, nextDeleteAt: s.trash.nextDeleteAt },
    workers: s.workers.map((w) => ({ id: w.id, name: w.name, kind: w.id === "local" ? "builtin" : "remote", status: w.status, slots: w.slots,
      jobs: jobs.filter((j) => w.currentJobIds.includes(j.jobId)), cpuPercent: w.live.cpuPercent, gpuPercent: w.live.gpu[0]?.utilPercent ?? null })),
    active: sum.activeJobs.map((j) => ({ jobId: j.id, library: j.library, name: j.file.name, percent: j.progress?.percent ?? null, etaS: j.progress?.etaS ?? null, expectedSavingBytes: j.expectedSavingBytes ?? null, expectedSavingEstimated: j.expectedSavingEstimated ?? false })),
    libraries: s.libraries.map((l) => {
      const now = l.counts.files * 1.4 * GB, saved = l.bytes.freed;
      return { id: l.id, name: l.name, state: l.status === "paused_share" ? "paused_share" : l.enabled ? "active" : "paused", bytesBefore: now + saved + l.bytes.waitingToFree, bytesNow: now, saved, savedPercent: saved > 0 ? Math.round((saved / (now + saved + l.bytes.waitingToFree)) * 1000) / 10 : null,
        waitingToFree: l.bytes.waitingToFree, couldFree: 0, grew: l.bytes.grew ?? 0, counts: { done: l.counts.completed, waiting: l.counts.waiting, skipped: l.counts.skipped, failed: l.counts.failed } };
    }),
    daily: days.map((day, i) => i < 6 ? { day, recorded: false, completed: 0, skipped: 0, failed: 0, freedBytes: 0 }
      // the last week's originals are still in the trash (CT 124's shape): that saving is waiting, not freed
      : { day, recorded: true, completed: 6 + ((i * 7) % 13), skipped: i % 3, failed: i % 7 === 0 ? 1 : i % 11 === 0 ? 3 : 0,
          freedBytes: i >= 13 && i < 30 ? Math.round((0.4 + (((i - 7) * 5) % 9) / 6) * GB) : 0,
          savedBytes: Math.round((0.4 + ((i * 5) % 9) / 6) * GB), waitingBytes: i < 23 ? 0 : Math.round((0.4 + ((i * 5) % 9) / 6) * GB), couldFreeBytes: 0 }),
    timeZone: "Europe/London",
    averages: { fileShrinkPercent: 38, secondsPerGB: 130 },
    encoderFallbacks: fell ? [{ workerId: s.workers[0].id, worker: s.workers[0].name, encoder: "hevc_qsv", lastError: "ffmpeg exited with code 1",
      count: 3, at: new Date(Date.now() - 6e5).toISOString(), pausedUntil: new Date(Date.now() + 50 * 6e4).toISOString() }] : [],
  };
}
