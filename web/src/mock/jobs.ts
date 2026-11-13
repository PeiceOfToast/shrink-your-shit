// V6 Jobs sample data in the server's shapes (types/jobs.ts, from the abe2b0e captures), for ?mock=1. Actions change
// it the way the server does: cancel and requeue move rows between tabs, retry sends a waiting retry now.
import type { ActionResult, JobAction, JobDetail, JobItem, JobState, JobsPage, Probe, Tab } from "../types/jobs";

const GB = 1e9, now = Date.now(), at = (min: number) => new Date(now - min * 60e3).toISOString();
const MOV = ["Movies", "1"] as const, TV = ["TV Shows", "2"] as const, HOME = ["Home videos", "3"] as const;
const base = (id: string, name: string, folder: string, lib: readonly [string, string], state: JobState, extra: Partial<JobItem>): JobItem => ({
  id, jobId: id.startsWith("skip-") ? undefined : id, origin: id.startsWith("skip-") ? "scan" : "job", state, subState: null, name, folder,
  path: "/media/" + lib[0].toLowerCase().replace(" ", "") + "/" + (folder === "." ? "" : folder + "/") + name, library: lib[0], libraryId: lib[1],
  bytes: 8 * GB, codec: "H.264", resolution: "1080p", durationSec: 7200, ...extra,
});
const prog = (pct: number, fps: number, el: number, eta: number) => ({ stage: "Converting video", percent: pct, workflowPercent: Math.round(5 + pct * 0.8), elapsedS: el, speed: 0.9, etaS: eta, fps, bitrateKbps: 8300 });

let items: JobItem[] = [
  base("j-48213", "Dune.Part.Two.2024.2160p.REMUX.mkv", "Dune Part Two (2024)", MOV, "running", { subState: "converting", worker: "builtin", progress: prog(62, 41, 728, 450), convertingTo: "HEVC · 10-bit · MKV", bytes: 58 * GB, resolution: "2160p" }),
  base("j-48214", "The.Bear.S03E04.1080p.WEB-DL.mkv", "Season 3", TV, "validating", { subState: "checking", worker: "nas", progress: { ...prog(100, 9, 222, 0), stage: "Checking the result", percent: null, workflowPercent: 90 }, bytes: 2.1 * GB }),
  base("48220", "Oppenheimer.2023.1080p.BluRay.mkv", "Oppenheimer (2023)", MOV, "queued", { subState: "waiting", attempts: 0, retryAt: null, status: "waiting", detail: null, bytes: 14.9 * GB }),
  base("48221", "Heat.1995.1080p.mkv", "Heat (1995)", MOV, "queued", { subState: "waiting", attempts: 0, retryAt: null, status: "waiting", detail: null, bytes: 12.1 * GB }),
  base("48222", "Wedding.2019.mp4", ".", HOME, "retry_wait", { subState: "retrying", attempts: 1, retryAt: new Date(now + 25 * 60e3).toISOString(), status: "retrying", reason: { code: "low_disk_space", message: "there is not enough free disk space next to this file: about 65.2 MB is needed to convert it safely, 592.0 KB is free" },
    detail: "there is not enough free disk space next to this file: about 65.2 MB is needed to convert it safely, 592.0 KB is free", bytes: 1.1 * GB }),
  base("48160", "Arrival.2016.1080p.BluRay.mkv", "Arrival (2016)", MOV, "completed", { beforeBytes: 11.4 * GB, afterBytes: 5.9 * GB, bytes: 5.9 * GB, codec: "HEVC", finishedAt: at(40), flow: { id: "w1", name: "Shrink videos" }, originalState: "kept", keptDaysLeft: 7, keptId: "9" }),
  base("41002", "Alien.1979.1080p.mkv", "Alien (1979)", MOV, "completed", { beforeBytes: 9.8 * GB, afterBytes: 4.4 * GB, bytes: 4.4 * GB, codec: "HEVC", finishedAt: at(60 * 24 * 9), flow: { id: "w1", name: "Shrink videos" }, originalState: "removed" }),
  base("skip-1", "Blade.Runner.2049.2017.2160p.DV.mkv", "Blade Runner 2049 (2017)", MOV, "skipped", { reason: { code: "dolby_vision", message: "It has Dolby Vision (profile 5), which can't be kept when it is converted." }, detail: "It has Dolby Vision (profile 5), which can't be kept when it is converted.", skippedId: "1", codec: "HEVC", resolution: "2160p" }),
  base("skip-2", "Severance.S02E07.1080p.WEB-DL.mkv", "Season 2", TV, "cancelled", { jobId: "47002", // #149: a stopped job keeps its job (steps, tries)
 reason: { code: "user_stopped", message: "You stopped this file. It is left as it is until you press Convert again." }, detail: "You stopped it.", skippedId: "2" }),
  base("48190", "Wedding.Raw.2018.mov", ".", HOME, "failed", { detail: "The new file is 4 s shorter than the original. The original is kept.", reason: { code: "convert_failed", message: "The new file is 4 s shorter than the original. The original is kept." } }),
  base("48191", "Crash.Test.2024.mp4", "Crash Test (2024)", HOME, "failed", { detail: "There isn't enough free space to write the new file. About 571 MB is needed, 101 MB is free.", reason: { code: "no_space", message: "There isn't enough free space to write the new file. About 571 MB is needed, 101 MB is free." } }),
  base("no-log", "Old.Movie.2020.mp4", "Old Movies", HOME, "completed", { beforeBytes: 8 * GB, afterBytes: 4 * GB, bytes: 4 * GB, codec: "HEVC", finishedAt: at(60 * 24 * 60), flow: { id: "w1", name: "Shrink videos" }, originalState: "removed" }),
  base("old-log-imported", "Imported.Log.mkv", "Old Imports", HOME, "completed", { beforeBytes: 3 * GB, afterBytes: 1.5 * GB, bytes: 1.5 * GB, codec: "HEVC", finishedAt: at(60 * 24 * 2), flow: { id: "w1", name: "Shrink videos" }, originalState: "kept", keptDaysLeft: 3 }),
  base("failed-replace", "Replace.Fail.mkv", "Failed Saves", HOME, "failed", { detail: "The new file was converted, but saving it in place of the original failed.", reason: { code: "stalled", message: "The new file was converted, but saving it in place of the original failed." } }),
];
const TAB: Record<Tab, JobState[]> = { waiting: ["queued", "retry_wait"], running: ["preparing", "running", "validating", "committing"], completed: ["completed"], skipped: ["skipped", "cancelled"], failed: ["failed"] };
const tabOf = (s: JobState) => (Object.keys(TAB) as Tab[]).find((t) => TAB[t].includes(s))!;
export function mockJobs(tab: Tab, q: string, path?: string): JobsPage {
  const counts = Object.fromEntries((Object.keys(TAB) as Tab[]).map((t) => [t, items.filter((j) => tabOf(j.state) === t).length])) as Record<Tab, number>;
  let rows = items.filter((j) => tabOf(j.state) === tab && (!q || j.name.toLowerCase().includes(q.toLowerCase())) && (!path || j.path === path));
  if (tab === "waiting") { // as the server with sort=queue (#134): taken-now files by priority, then age; a retry waiting for its time has no place
    const rank = (j: JobItem) => (j.pin === "first" ? 2 : j.pin === "last" ? 0 : 1); // #143: pinned first, then by priority, pinned last at the end
    const now = rows.filter((j) => j.state === "queued").sort((a, b) => rank(b) - rank(a) || (b.priority ?? 0) - (a.priority ?? 0));
    rows = [...now.map((j, i) => ({ ...j, position: i + 1 })), ...rows.filter((j) => j.state !== "queued").map((j) => ({ ...j, position: null }))];
  }
  return { items: rows, total: rows.length, counts };
}
// A removed job (its library was removed) and one from before the update, for the drawer's two special cases.
const REMOVED: JobDetail = { id: "47001", state: "cancelled", removed: true, reason: { code: "removed", message: "Its library was removed, so this file wasn't converted." },
  events: [{ seq: 3, at: at(300), from: null, to: "queued", reason: null, workerId: null }, { seq: 9, at: at(200), from: "queued", to: "cancelled", reason: "removed", workerId: null }], steps: [] };
// The sample's log by step: the steps the job went through, the failing one with its last lines
export function mockJobLog(id: string): { state: string; steps: { name: string; startedAt: string | null; tookMs: number | null; outcome: string; command: string | null; probe: Probe | null; check: { score: number; min: number | null; passed: boolean | null } | null; outputLines: number; output: string | string[]; result: string | null }[]; final: string | null } | { error: { code: string; message: string } } {
  const j = mockJob(id);
  if (!j) return { error: { code: "not_found", message: "There is no such job." } };
  const failed = j.state === "failed";
  const f48190 = id === "48190", f48191 = id === "48191";
  const out = f48190 ? ["[hevc @ 0x55d1] Error while decoding stream #0:0: Invalid data found when processing input", "Conversion failed!"] : [];
  const step = (name: string, outcome: string, extra: Record<string, unknown>) => ({ name, startedAt: at(300), tookMs: outcome === "ok" ? 120000 : outcome === "failed" && f48191 ? 0 : 252000, outcome, command: null, probe: null, check: null, outputLines: 0, output: [], result: outcome === "ok" ? "Done" : "Failed", ...extra });
  const conv: Record<string, unknown> = f48191 ? {} : { command: "ffmpeg -i in.mkv -c:v libx265 -crf 23 out.mkv", probe: { videoCodec: "h264", bitrateKbps: 12109, durationSec: 7200, sizeBytes: j.file?.bytes ?? 0, streams: [{ index: 0, type: "video", codec: "h264", Width: 1920, Height: 1080 }] }, check: failed ? { score: 0.912, min: 0.95, passed: false } : { score: 0.973, min: 0.95, passed: true }, outputLines: f48190 ? 2 : 184, output: out };
  if (id === "no-log") return { state: "none", steps: [], final: null };
  if (id === "old-log-imported") {
    const lines = Array.from({ length: 250 }, (_, i) => "line " + (i + 1) + ": FFMPEG frame output for this old job");
    return { state: "complete", steps: [{ name: "imported", startedAt: null, tookMs: null, outcome: "done", command: null, probe: null, check: null, outputLines: 250, output: lines.slice(-200), result: "Done" }], final: "done" };
  }
  if (id === "failed-replace") {
    return { state: "complete", steps: [
      { name: "encode", startedAt: "2026-11-11T14:00:00Z", tookMs: 120000, outcome: "ok", command: "ffmpeg -i in.mkv -c:v libx265 -crf 22 out.mkv", probe: { videoCodec: "h264", bitrateKbps: 8000, durationSec: 3600, sizeBytes: 3 * GB, streams: [{ index: 0, type: "video", codec: "h264", Width: 1920, Height: 1080 }] }, check: { score: 0.967, min: 0.95, passed: true }, outputLines: 10, output: ["[hevc @ 0x55d1] output file #0 does not contain any stream"], result: "Done" },
      { name: "check", startedAt: "2026-11-11T14:02:00Z", tookMs: 5000, outcome: "ok", command: null, probe: null, check: { score: 0.967, min: 0.95, passed: true }, outputLines: 0, output: [], result: "Passed" },
      { name: "replace", startedAt: "2026-11-11T14:02:05Z", tookMs: 500, outcome: "failed", command: null, probe: null, check: null, outputLines: 0, output: [], result: "Failed" },
    ], final: "The new file was converted, but saving it in place of the original failed." };
  }
  return { state: "complete", final: f48191 ? "kept" : "done", steps: [
    step("encode", f48190 ? "failed" : f48191 ? "failed" : "ok", { ...conv, result: failed ? "Failed" : "Done · 41% smaller" }),
  ] };
}
export function mockJob(id: string): JobDetail | null {
  if (id === REMOVED.id) return REMOVED;
  const j = items.find((x) => x.id === id || (x.jobId === id && x.origin === "scan"));
  if (!j || (j.origin === "scan" && !j.jobId)) return null; // a scan skip has no job; a skip with a job (#149) has its steps
  const old = id === "41002" || id === "no-log"; // finished before the update: no history
  const reason = j.reason ?? null;
  return {
    // removed: as the server, a job-backed skip's own row is gone (removed: true)
    id, state: j.state, removed: j.origin === "scan", reason, attempts: j.attempts ?? 0,
    // the sample's failure (E-API-SHAPE): the sentence is the reason's; the server sends raw step name 'encode' or 'check'
    ...(j.state === "failed" && j.id === "48190" ? { failure: { step: "encode", stepIndex: 0, stepCount: 1, afterMs: 252000, mediaTime: "00:41:12", hint: "The file is corrupt or uses an unsupported codec.", ffmpegRan: true, exitCode: 1, keyLines: ["[hevc @ 0x55d1] Error while decoding stream #0:0: Invalid data found when processing input", "Conversion failed!"] } } : {}),
    ...(j.state === "failed" && j.id === "48191" ? { failure: { step: "encode", stepIndex: 0, stepCount: 1, afterMs: 0, mediaTime: null, hint: "Free some space on that disk, then press Retry.", ffmpegRan: false, exitCode: null, keyLines: [] } } : {}),
    ...(j.state === "failed" && j.id === "failed-replace" ? { failure: { step: "replace", stepIndex: 2, stepCount: 3, afterMs: 500, mediaTime: null, hint: null, ffmpegRan: true, exitCode: 1, keyLines: ["rename /mnt/shrunk/file.tmp /mnt/shrunk/file.mkv: permission denied"] } } : {}), retryAt: j.retryAt ?? null, error: null, worker: j.worker ?? null,
    file: { path: j.path, bytes: j.beforeBytes ?? j.bytes }, createdAt: at(300), updatedAt: at(5),
    library: { id: j.libraryId, name: j.library }, workflow: { id: "w1", revisionId: "w1r4", version: 4 },
    events: old ? [] : [{ seq: 1, at: at(300), from: null, to: "queued", reason: null, workerId: null },
      ...(j.state !== "queued" ? [{ seq: 5, at: at(20), from: "queued" as JobState, to: "running" as JobState, reason: null, workerId: "builtin" }] : []),
      ...(["completed", "failed"].includes(j.state) ? [{ seq: 8, at: at(10), from: "running" as JobState, to: j.state, reason: j.state === "failed" ? "convert_failed" : null, workerId: "builtin" }] : [])],
    steps: old ? [] : [{ step: "found", at: at(300), ok: true, words: "Found in " + j.path.replace(/\/[^/]+$/, "") + "." }, { step: "checked", at: at(300), ok: true, words: "To be converted: H.264 at 12109 kbps." },
      ...(j.state === "completed" ? [{ step: "converted", at: at(12), ok: true, words: "Converted: HEVC · 8-bit · MKV." }, { step: "verified", at: at(11), ok: true, words: "Every check passed: the new file is complete, plays cleanly and looks like the original." }] : []),
      ...(j.state === "failed" && j.id === "48190" ? [{ step: "converted", at: at(12), ok: false, words: "The new file is 4 s shorter than the original. The original is kept." }] : []),
      ...(j.state === "failed" && j.id === "48191" ? [{ step: "converted", at: at(12), ok: false, words: "There isn't enough free space to write the new file. About 571 MB is needed, 101 MB is free." }] : []),
      ...(j.state === "failed" && j.id === "failed-replace" ? [{ step: "found", at: at(300), ok: true, words: "Found in /media/home/Failed Saves." }, { step: "checked", at: at(100), ok: true, words: "To be converted: H.264 at 8000 kbps." }, { step: "converted", at: at(15), ok: true, words: "Converted: HEVC · 8-bit · MKV." }, { step: "verified", at: at(14), ok: true, words: "Every check passed." }, { step: "saved", at: at(13), ok: false, words: "Saving in place failed: permission denied." }] : [])],
    metadata: { before: old ? null : { container: "matroska,webm", videoCodec: "h264", bitrateKbps: 12109, durationSec: 7200, sizeBytes: j.beforeBytes ?? j.bytes, streams: [{ index: 0, type: "video", codec: "h264", Width: 1920, Height: 1080 }, { index: 1, type: "audio", codec: "dts", channels: 6, channelLayout: "5.1" }] },
      after: j.state === "completed" ? { container: "matroska,webm", videoCodec: "hevc", bitrateKbps: 5300, durationSec: 7200, sizeBytes: j.afterBytes, streams: [{ index: 0, type: "video", codec: "hevc", Width: 1920, Height: 1080 }, { index: 1, type: "audio", codec: "aac", channels: 2, channelLayout: "stereo" }] } : null },
  };
}
// POST /api/skipped/{id}/convert-again (v1): forget the decision; the next scan looks at the file again (here: it goes
// back to Waiting after a moment, as a scan would queue it).
export function mockConvertAgain(skippedId: string): { ok: boolean } | { error: { code: string; message: string } } {
  const j = items.find((x) => x.id === "skip-" + skippedId);
  if (!j) return { error: { code: "not_found", message: "That file is not in the skipped list any more. Nothing was changed." } };
  items = items.filter((x) => x !== j);
  setTimeout(() => { items = [...items, { ...j, id: "9" + skippedId, jobId: "9" + skippedId, origin: "job", state: "queued", subState: "waiting", detail: null, reason: null }]; }, 1500);
  return { ok: true };
}
export function mockAction(action: JobAction, ids: string[], pin?: "first" | "last" | "none"): { results: ActionResult[] } {
  const results = ids.map((id): ActionResult => {
    if (id.startsWith("skip-")) return { id, ok: false, code: "not_found", message: "There is no such job." }; // as the server: job ids only
    const j = items.find((x) => x.id === id);
    if (!j) return { id, ok: false, code: "not_found", message: "There is no such job." };
    if (action === "cancel") {
      if (!["queued", "retry_wait", "preparing", "running", "validating", "committing"].includes(j.state)) return { id, ok: false, code: "not_active", message: "Only a waiting or running file can be stopped." };
      items = items.map((x) => (x === j ? { ...x, id: "skip-" + x.id, jobId: undefined, origin: "scan", state: "cancelled", reason: { code: "user_stopped", message: "You stopped this file. It is left as it is until you press Convert again." }, detail: "You stopped it.", progress: undefined } : x));
      return { id, ok: true };
    }
    if (action === "retry") {
      if (j.state === "retry_wait") { items = items.map((x) => (x === j ? { ...x, state: "queued", subState: "waiting", retryAt: null, status: "waiting" } : x)); return { id, ok: true }; }
      if (j.state === "queued") return { id, ok: false, code: "already_waiting", message: "It is already waiting." };
      if (j.state !== "failed") return { id, ok: false, code: "not_failed", message: "Only a failed file can be tried again." };
      items = items.map((x) => (x === j ? { ...x, state: "queued", subState: "waiting", detail: null, reason: null } : x)); return { id, ok: true };
    }
    if (action === "requeue") {
      if (!["failed", "skipped", "cancelled", "completed"].includes(j.state)) return { id, ok: false, code: "not_finished", message: "It is already in the queue." };
      items = items.map((x) => (x === j ? { ...x, id: x.id.replace("skip-", "9"), origin: "job", state: "queued", subState: "waiting", detail: null, reason: null } : x)); return { id, ok: true };
    }
    if (j.state !== "queued") return { id, ok: false, code: "not_waiting", message: "Only a waiting job can be moved in the queue." };
    items = items.map((x) => (x === j ? { ...x, pin: pin === "none" ? null : pin } : x)); return { id, ok: true };
  });
  return { results };
}
