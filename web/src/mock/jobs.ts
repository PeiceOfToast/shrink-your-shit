// V6 Jobs sample data in the server's shapes (types/jobs.ts, from the abe2b0e captures), for ?mock=1. Actions change
// it the way the server does: cancel and requeue move rows between tabs, retry sends a waiting retry now.
import type { JobLog, LogLine, LogSection } from "../jobsApi";
import type { ActionResult, JobAction, JobDetail, JobItem, JobState, JobsPage, Tab } from "../types/jobs";

const GB = 1e9, now = Date.now(), at = (min: number) => new Date(now - min * 60e3).toISOString();
const MOV = ["Movies", "l1"] as const, TV = ["TV Shows", "l2"] as const, HOME = ["Home videos", "l3"] as const; // ids as in the mock library list
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
  base("48165", "Severance.S02E01.1080p.WEB-DL.mkv", "Season 2", TV, "completed", { beforeBytes: 4.2 * GB, afterBytes: 1.9 * GB, bytes: 1.9 * GB, codec: "HEVC", finishedAt: at(50), flow: { id: "w1", name: "Shrink videos" }, originalState: "left_in_place" }),
  base("48166", "Dune.2021.1080p.mkv", "Dune (2021)", MOV, "completed", { beforeBytes: 7.5 * GB, afterBytes: 3.1 * GB, bytes: 3.1 * GB, codec: "HEVC", finishedAt: at(70), flow: { id: "w1", name: "Shrink videos" }, originalState: "restored" }),
  base("41002", "Alien.1979.1080p.mkv", "Alien (1979)", MOV, "completed", { beforeBytes: 9.8 * GB, afterBytes: 4.4 * GB, bytes: 4.4 * GB, codec: "HEVC", finishedAt: at(60 * 24 * 9), flow: { id: "w1", name: "Shrink videos" }, originalState: "deleted" }),
  base("skip-1", "Blade.Runner.2049.2017.2160p.DV.mkv", "Blade Runner 2049 (2017)", MOV, "skipped", { reason: { code: "dolby_vision", message: "It has Dolby Vision (profile 5), which can't be kept when it is converted." }, detail: "It has Dolby Vision (profile 5), which can't be kept when it is converted.", skippedId: "1", codec: "HEVC", resolution: "2160p" }),
  base("skip-2", "Severance.S02E07.1080p.WEB-DL.mkv", "Season 2", TV, "cancelled", { jobId: "47002", // #149: a stopped job keeps its job (steps, tries)
 reason: { code: "user_stopped", message: "You stopped this file. It is left as it is until you press Convert again." }, detail: "You stopped it.", skippedId: "2" }),
  base("48190", "Wedding.Raw.2018.mov", ".", HOME, "failed", { detail: "The new file is 4 s shorter than the original. The original is kept.", reason: { code: "convert_failed", message: "The new file is 4 s shorter than the original. The original is kept." } }),
  base("48191", "Crash.Test.2024.mp4", "Crash Test (2024)", HOME, "failed", { detail: "There isn't enough free space to write the new file. About 571 MB is needed, 101 MB is free.", reason: { code: "no_space", message: "There isn't enough free space to write the new file. About 571 MB is needed, 101 MB is free." } }),
  base("no-log", "Old.Movie.2020.mp4", "Old Movies", HOME, "completed", { beforeBytes: 8 * GB, afterBytes: 4 * GB, bytes: 4 * GB, codec: "HEVC", finishedAt: at(60 * 24 * 60), flow: { id: "w1", name: "Shrink videos" }, originalState: "kept_beside" }),
  base("old-log-imported", "Imported.Log.mkv", "Old Imports", HOME, "completed", { beforeBytes: 3 * GB, afterBytes: 1.5 * GB, bytes: 1.5 * GB, codec: "HEVC", finishedAt: at(60 * 24 * 2), flow: { id: "w1", name: "Shrink videos" }, originalState: "kept", keptDaysLeft: 3 }),
  // a check kept the original (state failed, code kept_original, no failure summary): its account is in the check step's found lines
  base("48195", "Holiday.2021.1080p.mkv", "Holiday (2021)", HOME, "failed", { detail: "Kept the original: the picture changed too much (0.912, 0.95 required).", reason: { code: "kept_original", message: "Kept the original: the picture changed too much (0.912, 0.95 required)." } }),
  base("48196", "Concert.2022.1080p.mkv", "Concert (2022)", HOME, "failed", { detail: "Kept the original: the new file didn't play to the end.", reason: { code: "kept_original", message: "Kept the original: the new file didn't play to the end." } }),
  base("48197", "Old.Tape.1998.avi", "Old Tapes", HOME, "failed", { detail: "Kept the original: the original is damaged in 7 places.", reason: { code: "kept_original", message: "Kept the original: the original is damaged in 7 places." } }),
  base("48198", "Lecture.2020.mkv", "Lecture (2020)", HOME, "completed", {}),
  base("48199", "Old.Movie.2018.mkv", "Old Movie (2018)", HOME, "completed", {}),
  base("failed-replace", "Replace.Fail.mkv", "Failed Saves", HOME, "failed", { detail: "The new file was converted, but saving it in place of the original failed.", reason: { code: "stalled", message: "The new file was converted, but saving it in place of the original failed." } }),
];
// ?jobs=many: 450 more waiting files, to show the list past its first 200 (Show more)
if (new URLSearchParams(location.search).get("jobs") === "many") items = [...items, ...Array.from({ length: 450 }, (_, i) => base("5" + String(i).padStart(4, "0"), "Home.Video." + String(i + 1).padStart(3, "0") + ".mp4", ".", HOME, "queued", { subState: "waiting", attempts: 0, retryAt: null, status: "waiting", detail: null, bytes: 2 * GB }))];
const TAB: Record<Tab, JobState[]> = { waiting: ["queued", "retry_wait"], running: ["preparing", "running", "validating", "committing"], completed: ["completed"], skipped: ["skipped", "cancelled"], failed: ["failed"] };
const tabOf = (s: JobState) => (Object.keys(TAB) as Tab[]).find((t) => TAB[t].includes(s))!;
export function mockJobs(tab: Tab, q: string, path?: string, limit = 200, library = "", kept = false, left = false): JobsPage {
  const mine = items.filter((j) => !library || j.libraryId === library); // as the server: ?libraryId= narrows the rows and the tab counts
  const counts = Object.fromEntries((Object.keys(TAB) as Tab[]).map((t) => [t, mine.filter((j) => tabOf(j.state) === t).length])) as Record<Tab, number>;
  // as the server with ?kept=1 or ?left=1 (Done only): the rows whose original is still kept, or still left beside or in place; the counts stay the tab's own
  let rows = mine.filter((j) => tabOf(j.state) === tab && (!q || j.name.toLowerCase().includes(q.toLowerCase())) && (!path || j.path === path) && (!kept || (tab === "completed" && j.originalState === "kept")) && (!left || (tab === "completed" && (j.originalState === "left_in_place" || j.originalState === "kept_beside"))));
  if (tab === "waiting") { // as the server with sort=queue (#134): taken-now files by priority, then age; a retry waiting for its time has no place
    const rank = (j: JobItem) => (j.pin === "first" ? 2 : j.pin === "last" ? 0 : 1); // #143: pinned first, then by priority, pinned last at the end
    const now = rows.filter((j) => j.state === "queued").sort((a, b) => rank(b) - rank(a) || (b.priority ?? 0) - (a.priority ?? 0));
    rows = [...now.map((j, i) => ({ ...j, position: i + 1 })), ...rows.filter((j) => j.state !== "queued").map((j) => ({ ...j, position: null }))];
  }
  return { items: rows.slice(0, limit), total: rows.length, counts };
}
// A removed job (its library was removed) and one from before the update, for the drawer's two special cases.
const REMOVED: JobDetail = { id: "47001", state: "cancelled", removed: true, reason: { code: "removed", message: "Its library was removed, so this file wasn't converted." },
  events: [{ seq: 3, at: at(300), from: null, to: "queued", reason: null, workerId: null }, { seq: 9, at: at(200), from: "queued", to: "cancelled", reason: "removed", workerId: null }], steps: [] };
// The sample's log by step: the steps the job went through, the failing one with its last lines
// the sectioned log (contract L1): each line is { at, step, level, stream, text }. A skipped job has no sections. The encode
// section has ffmpeg's stderr lines (dimmed in the UI), a libva line (stream info) and the stall warning (L2, R9: an ordinary line)
const ln = (min: number, step: number, level: LogLine["level"], stream: LogLine["stream"], text: string): LogLine => ({ at: at(min), step, level, stream, text });
const sectionsFor = (result: string): LogSection[] => result === "skipped" ? [] : [
  { id: "input", title: "Input", lines: [ln(30, 0, "info", "app", "Reading the file")] },
  { id: "encode", title: "Encode", lines: [
    ln(29, 0, "info", "app", "Starting ffmpeg: HEVC, Balanced quality"),
    ln(29, 0, "info", "stderr", "ffmpeg version 6.1.1 Copyright (c) 2000-2023 the FFmpeg developers"),
    ln(28, 0, "info", "info", "libva info: VA-API version 1.20.0"),
    ln(20, 0, "info", "stderr", "frame= 43200 fps=142 q=24.0 size= 2150400kB time=00:30:00.00 bitrate=9800.1kbits/s speed=3.4x"),
    ln(14, 0, "warning", "app", "No progress for 30 s; the encode is still running"),
    ln(13, 0, "info", "stderr", "frame= 86400 fps=141 q=24.0 size= 4300800kB time=01:00:00.00 bitrate=9800.1kbits/s speed=3.4x"),
  ] },
  ...(result === "converted" ? [{ id: "replace", title: "Replace", lines: [ln(1, 2, "info", "app", "Saved the new file in place of the original")] }] : []),
];
// the start, variables and summary of a job that ran with full logs (contract v2); result: converted | kept | failed | skipped
const runInfo = (result: string) => ({
  sections: sectionsFor(result),
  start: { reason: "New file found by the scan", appVersion: "1.4.0", workflow: { name: "Shrink videos", revision: 7 } },
  variables: [
    { name: "api_key", changedBy: null, secret: true },
    { name: "audio_languages", start: "", end: result === "converted" ? "eng, jpn" : "", changedBy: result === "converted" ? "audio" : null, secret: false },
    { name: "file_name", start: "Sample.2020.mkv", end: "Sample.2020.mkv", changedBy: null, secret: false },
    { name: "resolution", start: "1080p", end: "1080p", changedBy: null, secret: false },
    { name: "target_codec", start: "", end: "hevc", changedBy: "encode", secret: false },
    { name: "title", start: "Sample (2020)", end: "Sample (2020)", changedBy: null, secret: false },
  ],
  summary: { result, originalBytes: 8 * GB, finalBytes: result === "converted" ? 4.7 * GB : 0, savedPercent: result === "converted" ? 41 : 0, outputPath: "/media/movies/Sample (2020).mkv", tookMs: 912000, tempRemoved: result === "converted", ...(result === "converted" && { passes: 1, bytesWritten: 4.7 * GB, slowestStep: { name: "encode", tookMs: 612000 } }),
    before: { video: "H.264 1920x1080, 8.0 Mbps", audio: "2 tracks: eng, jpn", subtitles: "English, English SDH, German", durationSec: 6312.4 },
    after: result === "converted" ? { video: "HEVC 1920x1080, 2.9 Mbps", audio: "1 track: eng AAC stereo", subtitles: "German inside · English, English SDH as .srt", durationSec: 6312.4 } : undefined },
});
// the server keeps a step's warnings and errors only in the sections (v2joblog.go: steps have no such keys), so a job with
// sections has none on its steps: they move into section lines. A job without sections keeps them on its steps, as before.
export function mockJobLog(id: string): JobLog | { error: { code: string; message: string } } {
  const log = mockJobLogRaw(id);
  if ("error" in log || !log.sections?.length) return log;
  const sections: LogSection[] = log.sections.map((s) => ({ ...s, lines: [...s.lines] }));
  const into = (secId: string, title: string, lines: LogLine[]) => { let s = sections.find((x) => x.id === secId); if (!s) { s = { id: secId, title, lines: [] }; sections.push(s); } s.lines.push(...lines); };
  const STEP: Record<string, number> = { encode: 0, check: 1, replace: 2 }; // the server's step index (worker.go)
  const steps = log.steps.map((st) => {
    const { warnings, errors, ...rest } = st;
    if (warnings?.length) into("encode", "Encode", warnings.map((t) => ln(2, STEP[st.name] ?? 0, "warning", "app", t)));
    if (errors?.length) into("check", "Check", errors.map((t) => ln(2, STEP[st.name] ?? 0, "error", "check", t)));
    return rest;
  });
  return { ...log, steps, sections };
}
function mockJobLogRaw(id: string): JobLog | { error: { code: string; message: string } } {
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
    return { state: "complete", ...runInfo("failed"), steps: [
      { name: "encode", startedAt: "2026-11-11T14:00:00Z", tookMs: 120000, outcome: "ok", command: "ffmpeg -i in.mkv -c:v libx265 -crf 22 out.mkv", probe: { videoCodec: "h264", bitrateKbps: 8000, durationSec: 3600, sizeBytes: 3 * GB, streams: [{ index: 0, type: "video", codec: "h264", Width: 1920, Height: 1080 }] }, check: { score: 0.967, min: 0.95, passed: true }, outputLines: 10, output: ["[hevc @ 0x55d1] output file #0 does not contain any stream"], result: "Done" },
      { name: "check", startedAt: "2026-11-11T14:02:00Z", tookMs: 5000, outcome: "ok", command: null, probe: null, check: { score: 0.967, min: 0.95, passed: true }, outputLines: 0, output: [], result: "Passed" },
      { name: "replace", startedAt: "2026-11-11T14:02:05Z", tookMs: 500, outcome: "failed", command: null, probe: null, check: null, outputLines: 0, output: [], result: "Failed" },
    ], final: "The new file was converted, but saving it in place of the original failed." };
  }
  if (id === "48198") return { state: "complete", final: "skipped", ...runInfo("skipped"), steps: [
    step("filter", "skipped", { tookMs: 800, decision: "Skipped: already HEVC at 1080p", reason: "The workflow converts only files that aren't HEVC yet.", result: "Skipped" }),
  ] };
  const found: Record<string, string[]> = {
    // the server's own formats (worker verify.go ssimDetail/playback, worker.go encoderDetail/fileSummary, 11-18)
    "48195": ["Check: picture comparison (SSIM) at 5 places, 10 s each: 0:01:00, 0:24:00, 0:48:00, 1:12:00, 1:36:00",
      "Scores: 0:01:00 0.958, 0:24:00 0.912 (0.95 required); 0:48:00 not measured, 1:12:00 not measured, 1:36:00 not measured",
      "Alignment: the new file was also tried one frame earlier and later; the best score counts", "Worst frames: 0:24:03 (0.801), 0:24:04 (0.823), 0:24:07 (0.840)",
      "Encoder: hevc_qsv, -global_quality 25, hardware: qsv=hw", "Original: h264 1920x1080 23.976 fps 8-bit, 2 audio, 3 subtitles, 1:48:12", "New file: hevc 1920x1080 23.976 fps 8-bit, 2 audio, 3 subtitles, 1:48:12"],
    "48196": ["Check: playback test, 30 s sample from 0:41:00", "Where: playback reached frame 61802", "Errors: 3 error lines from ffmpeg, 2 different",
      "[hevc @ 0x5581] Could not find ref with POC 41", "[hevc @ 0x5581] Error constructing the frame RPS.", "libva info: VA-API version 1.20.0", "libva info: Trying to open /usr/lib/x86_64-linux-gnu/dri/iHD_drv_video.so",
      "Encoder: hevc_vaapi, -qp 24, hardware: vaapi=va:/dev/dri/renderD128", "Original: h264 1920x1080 25.000 fps 8-bit, 1 audio, 0 subtitles, 1:02:40", "New file: hevc 1920x1080 25.000 fps 8-bit, 1 audio, 0 subtitles, 1:02:40"],
    // workshop 11-18 01:07Z: the damaged-original check (step 1 'check'): the Check and Errors lines, then up to 5 of ffmpeg's
    "48197": ["Check: damage in the original, read while converting", "Errors: 7 damaged pictures; up to 3 are tolerated",
      "[h264 @ 0x55e0] error while decoding MB 41 22, bytestream -7", "[h264 @ 0x55e0] concealing 812 DC, 812 AC, 812 MV errors in P frame"],
  };
  // a job from before full logs: no start, variables or summary, so only its steps show (with the muted line)
  if (id === "48199") return { state: "complete", final: "done", steps: [step("encode", "ok", { ...conv, result: "Done · 41% smaller" })] };
  if (found[id]) return { state: "complete", final: j.reason?.message ?? null, ...runInfo("kept"), steps: [
    step("encode", "ok", { ...conv, check: null, result: "Done", decision: "Converted the video to HEVC", reason: "The file is H.264, and the workflow converts to HEVC.", warnings: ["The hardware encoder stopped once and was restarted."], speedX: 2.1, avgKbps: 3100 }),
    step("check", "failed", { found: found[id], result: j.reason?.message ?? "Failed", errors: [j.reason?.message ?? "The picture check failed."] }),
  ] };
  if (failed) return { state: "complete", final: f48191 ? "kept" : "done", ...runInfo("failed"), steps: [
    step("encode", "failed", { ...conv, result: "Failed" }),
  ] };
  return { state: "complete", final: "done", ...runInfo("converted"), steps: [
    step("audio", "ok", { tookMs: 4000, decision: "Kept English and Japanese audio; removed 2 others", reason: "They aren't in your languages.", set: [{ name: "audio_languages", value: "eng, jpn" }], result: "Done" }),
    step("encode", "ok", { ...conv, decision: "Converted the video to HEVC at Balanced quality", reason: "The file is H.264, and the workflow converts to HEVC.", speedX: 3.4, avgKbps: 2450, set: [{ name: "target_codec", value: "hevc" }, { name: "api_key" }], result: "Done · 41% smaller" }),
    step("replace", "ok", { tookMs: 1500, decision: "Saved the new file in place of the original", reason: "It is smaller and passed the picture check.", moves: [{ action: "move", from: "/media/movies/Sample (2020).mkv", to: "/media/movies/.syp-trash/Sample (2020).mkv", ok: true, error: null }, { action: "move", from: "/media/movies/Sample (2020).new.mkv", to: "/media/movies/Sample (2020).mkv", ok: true, error: null }], result: "Done" }),
  ] };
}
const FELL = "Intel Quick Sync (hevc_qsv) failed: ffmpeg exited with code 1. Converted on the processor instead."; // #323's step words
const NOT_TOLD = "Sonarr didn't answer in 60 s; it will notice the file at its next scan."; // #333's media_refresh words
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
      ...(j.id === "48160" ? [{ step: "encoder_fallback", at: at(30), ok: false, words: FELL }] : []),
      ...(j.state === "completed" ? [{ step: "converted", at: at(12), ok: true, words: "Converted: HEVC · 8-bit · MKV." }, { step: "verified", at: at(11), ok: true, words: "Every check passed: the new file is complete, plays cleanly and looks like the original." }] : []),
      ...(j.id === "48165" || j.id === "48160" ? [{ step: "media_refresh", at: at(10), ok: false, words: j.id === "48160" ? NOT_TOLD.replace("Sonarr", "Radarr") : NOT_TOLD }] : []),
      // contract "Plex / Jellyfin": one media_refresh step per app; a told one is ok (the words are the server's)
      ...(j.id === "48165" ? [{ step: "media_refresh", at: at(10), ok: true, words: "Told Plex (Home) to scan the folder" }] : []),
      ...(j.state === "failed" && j.id === "48190" ? [{ step: "converted", at: at(12), ok: false, words: "The new file is 4 s shorter than the original. The original is kept." }] : []),
      ...(j.state === "failed" && j.id === "48191" ? [{ step: "converted", at: at(12), ok: false, words: "There isn't enough free space to write the new file. About 571 MB is needed, 101 MB is free." }] : []),
      ...(j.state === "failed" && j.id === "failed-replace" ? [{ step: "found", at: at(300), ok: true, words: "Found in /media/home/Failed Saves." }, { step: "checked", at: at(100), ok: true, words: "To be converted: H.264 at 8000 kbps." }, { step: "converted", at: at(15), ok: true, words: "Converted: HEVC · 8-bit · MKV." }, { step: "verified", at: at(14), ok: true, words: "Every check passed." }, { step: "saved", at: at(13), ok: false, words: "Saving in place failed: permission denied." }] : [])],
    // contract 088aa569: every file converted so far is on its library's current workflow, so Convert again is refused
    ...(j.state === "completed" ? { convertAgain: { possible: false, reason: { code: "current_workflow", message: "Already converted with this workflow.", deletedAt: null } } } : {}),
    // the one done job with saved .srt files (the drawer's "Subtitles saved as files" line)
    ...(j.id === "48165" ? { subtitleFiles: ["Severance.S02E01.1080p.WEB-DL.eng.srt", "Severance.S02E01.Goodbye.Mrs.Selvig.1080p.ATVP.WEB-DL.DDP5.1.Atmos.H.264-FLUX.eng.sdh.srt"] } : {}),
    warning: j.id === "48160" ? FELL : j.id === "48165" ? NOT_TOLD : null,
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
