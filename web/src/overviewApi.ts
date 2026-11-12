// GET /api/v2/overview (#177, user review 11-12 item 5): the Dashboard as an overview. Per library the NET figures of
// the Libraries page (saved = freed; bytesBefore = bytesNow + freed); the trash is GROSS and never added to them; CPU/GPU
// null = not measured yet; a day before history is recorded:false (not recorded, never estimated); days are the
// server's (timeZone). ?mock=1 builds the same fields from the sample install, nothing more.
import { call, MOCK, type Answer } from "./api";
import * as mock from "./mock";
import type { WorkerStatus } from "./types/v2";

export interface OverviewJob { jobId: string; name: string; percent: number | null; fps: number | null; speed: number | null; etaS: number | null }
export interface Overview {
  headline: { freed: number; waitingToFree: number; nextFreeAt: string | null; couldFree: number; filesConverted: number; filesWaiting: number };
  trash: { bytes: number; files: number; nextDeleteAt: string | null };
  workers: { id: string; name: string; kind: string; status: WorkerStatus; slots: { cpu?: { used: number; total: number }; used?: number; total?: number }; jobs: OverviewJob[]; cpuPercent: number | null; gpuPercent: number | null }[];
  active: { jobId: string; library: { id: string; name: string } | null; name: string; percent: number | null; etaS: number | null; expectedSavingBytes: number | null }[];
  libraries: { id: string; name: string; state: "active" | "paused"; bytesBefore: number; bytesNow: number; saved: number; savedPercent: number | null; waitingToFree: number; couldFree: number;
    counts: { done: number; waiting: number; skipped: number; failed: number } }[];
  daily: { day: string; recorded: boolean; completed: number; skipped: number; failed: number; freedBytes: number }[];
  timeZone: string;
  averages: { fileShrinkPercent: number | null; secondsPerGB: number | null };
}

export function getOverview(): Promise<Answer<Overview>> {
  return MOCK ? Promise.resolve(sample()) : call<Overview>("GET", "/api/v2/overview");
}

// The sample install as an overview: the agreed fields only, from the sample's own workers, jobs and libraries.
function sample(): Overview {
  const s = mock.mockState(), sum = mock.summary(s), GB = 1e9;
  const jobs = sum.activeJobs.map((j) => ({ jobId: j.id, name: j.file.name, percent: j.progress?.percent ?? null, fps: j.progress?.fps ?? null, speed: j.progress?.speed ?? null, etaS: j.progress?.etaS ?? null }));
  const days = Array.from({ length: 30 }, (_, i) => { const d = new Date(Date.now() - (29 - i) * 864e5); return d.toISOString().slice(0, 10); });
  return {
    headline: { freed: sum.bytes.freed, waitingToFree: s.trash.bytes, nextFreeAt: s.trash.nextDeleteAt, couldFree: sum.bytes.waitingToFree, filesConverted: sum.jobCounts.completed ?? 0, filesWaiting: sum.activity.waiting },
    trash: { bytes: s.trash.bytes, files: s.trash.files, nextDeleteAt: s.trash.nextDeleteAt },
    workers: s.workers.map((w) => ({ id: w.id, name: w.name, kind: w.id === "local" ? "builtin" : "remote", status: w.status, slots: w.slots,
      jobs: jobs.filter((j) => w.currentJobIds.includes(j.jobId)), cpuPercent: null, gpuPercent: null })),
    active: sum.activeJobs.map((j) => ({ jobId: j.id, library: j.library, name: j.file.name, percent: j.progress?.percent ?? null, etaS: j.progress?.etaS ?? null, expectedSavingBytes: null })),
    libraries: s.libraries.map((l) => {
      const now = l.counts.files * 1.4 * GB, saved = l.bytes.freed;
      return { id: l.id, name: l.name, state: l.enabled ? "active" : "paused", bytesBefore: now + saved, bytesNow: now, saved, savedPercent: now + saved > 0 ? Math.round((saved / (now + saved)) * 1000) / 10 : null,
        waitingToFree: l.bytes.waitingToFree, couldFree: 0, counts: { done: l.counts.completed, waiting: l.counts.waiting, skipped: l.counts.skipped, failed: l.counts.failed } };
    }),
    daily: days.map((day, i) => i < 6 ? { day, recorded: false, completed: 0, skipped: 0, failed: 0, freedBytes: 0 }
      : { day, recorded: true, completed: 6 + ((i * 7) % 13), skipped: i % 3, failed: i % 7 === 0 ? 1 : 0, freedBytes: (0.4 + ((i * 5) % 9) / 6) * GB }),
    timeZone: "Europe/London",
    averages: { fileShrinkPercent: 38, secondsPerGB: 130 },
  };
}
