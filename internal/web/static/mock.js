/* Fake in-memory server for UI-API-CONTRACT.md, used until the real endpoints land.
   One function per name in api.js: mock[name](params, body) -> Promise. Same shapes,
   same error objects. Settings survive page changes within the tab (sessionStorage). */
(function (g) {
  "use strict";
  var GB = 1e9, now = Date.now();
  function iso(msAgo) { return new Date(now - msAgo).toISOString(); }
  function copy(x) { return JSON.parse(JSON.stringify(x)); }
  function ok(x) { return Promise.resolve(copy(x === undefined ? { ok: true } : x)); }
  function fail(status, code, message, extra) {
    var e = { status: status, code: code, message: message };
    for (var k in extra) e[k] = extra[k];
    return Promise.reject(e);
  }
  function list(items) { return { items: items, total: items.length }; }
  function find(arr, key, id) { for (var i = 0; i < arr.length; i++) if (arr[i][key] === id) return i; return -1; }

  var settings = {
    controlLevel: "basic", perLibraryFlow: false, firstRunDone: true, firstRunStep: 1, authRequired: false,
    keepOriginalsDays: 7, trashPath: "Kept next to each file, in a folder called .syp-trash",
    queueOrder: "newest", languages: [], parallelJobs: 1, quietHours: { from: "", to: "" }, lowPriority: true, scanSchedule: "02:00",
    integrations: { sonarr: { url: "", hasKey: false }, radarr: { url: "", hasKey: false }, plex: { url: "", hasKey: false }, jellyfin: { url: "", hasKey: false },
      notifications: { webhookUrl: "", onDone: false, onFailed: true, onNeedsAttention: true } },
    updateAvailable: /[?&]update=1/.test(location.search) ? { version: "1.2.0" } : null // ?update=1 shows the update notice
  };
  try { var saved = sessionStorage.getItem("syp-mock-settings"); if (saved) Object.assign(settings, JSON.parse(saved)); } catch (e) {}
  function persist() { try { sessionStorage.setItem("syp-mock-settings", JSON.stringify(settings)); } catch (e) {} }

  var libs = [
    { id: "1", name: "Movies", path: "/media/movies", state: "working", workingFiles: 2, waitingFiles: 590, nextScan: iso(-6 * 3600e3), freedBytes: 410 * GB, flowId: "f1", settingLabel: "Balanced", settingKind: "slider" },
    { id: "2", name: "TV Shows", path: "/media/tv", state: "idle", workingFiles: 0, waitingFiles: 0, nextScan: iso(-6 * 3600e3), freedBytes: 230 * GB, flowId: "f2", settingLabel: "Smaller", settingKind: "slider" },
    { id: "4", name: "Anime", path: "/media/anime", state: "idle", workingFiles: 0, waitingFiles: 40, nextScan: iso(-6 * 3600e3), freedBytes: 12 * GB, flowId: "f2", settingLabel: "Smaller", settingKind: "slider" },
    { id: "3", name: "Home videos", path: "/media/home", state: "needs_attention", workingFiles: 0, waitingFiles: 12, nextScan: iso(-6 * 3600e3), freedBytes: 0, flowId: "f3", settingLabel: "Custom", settingKind: "custom" }
  ];
  try { var sl = sessionStorage.getItem("syp-mock-libs"); if (sl) libs = JSON.parse(sl); } catch (e) {}
  // ?fresh=1 on the wizard starts over: no libraries, first-run not done.
  if (/[?&]fresh=1/.test(location.search)) { libs = []; settings.firstRunDone = false; settings.firstRunStep = 1; persist(); saveLibs(); }
  // ?installed=1 (any page): a new install whose library came from install flags (Docker's /media, --library):
  // one library not started yet, first-run not done. Every page should open first-run.
  if (/[?&]installed=1/.test(location.search)) {
    libs = [{ id: "1", name: "media", path: "/media", state: "not_started", started: false, workingFiles: 0, waitingFiles: 0, nextScan: iso(-6 * 3600e3), freedBytes: 0, flowId: "f1", settingLabel: "Balanced", settingKind: "slider" }];
    settings.firstRunDone = false; settings.firstRunStep = 1; persist(); saveLibs();
  }
  function saveLibs() { try { sessionStorage.setItem("syp-mock-libs", JSON.stringify(libs)); sessionStorage.setItem("syp-mock-flows", JSON.stringify(flows)); } catch (e) {} }
  var flows = [
    { id: "f1", name: "Movies", usedBy: ["Movies"], madeWith: "slider", changedAt: iso(9 * 864e5) },
    { id: "f2", name: "TV Shows", usedBy: ["TV Shows", "Anime"], madeWith: "slider", changedAt: iso(20 * 864e5) },
    { id: "f3", name: "Home videos 2", usedBy: ["Home videos"], madeWith: "builder", changedAt: iso(3 * 864e5) },
    { id: "f4", name: "Home videos", usedBy: [], madeWith: "slider", changedAt: iso(60 * 864e5) },
    { id: "f5", name: "Old settings", usedBy: [], madeWith: "legacy", changedAt: iso(120 * 864e5) } // carried over by an upgrade (addendum 11)
  ];
  try { var sf = sessionStorage.getItem("syp-mock-flows"); if (sf) flows = JSON.parse(sf); } catch (e) {}
  // The slider table is the engine's (untuned starting values): crf is x265's, lower = better quality.
  var SLIDER = [{ position: 1, name: "Smallest", crf: 27 }, { position: 2, name: "Smaller", crf: 25 },
    { position: 3, name: "Balanced", crf: 23 }, { position: 4, name: "Better quality", crf: 21 },
    { position: 5, name: "Best quality", crf: 19 }]; // {position, name, crf} since workshop S1
  // Read-only steps for a flow. The raw CRF number appears only here, in a flow step's detail.
  function stepsFor(f) {
    var crf = { f1: 23, f2: 25, f3: 22, f4: 21 }[f.id] || 23, n = 3, chain = [];
    chain.push({ n: n++, kind: "video", label: "Convert to HEVC", detail: "CRF " + crf + " (lower = better quality)" });
    var cap = libs.filter(function (l) { return l.flowId === f.id && l.sel && l.sel.maxBitrate && l.sel.maxBitrate !== "none"; }).map(function (l) { return +l.sel.maxBitrate; })[0];
    if (cap) chain.push({ n: n++, kind: "video", label: "Limit to " + cap + " Mbps", detail: "maxrate " + cap + "M, bufsize " + 2 * cap + "M" });
    if (f.madeWith === "builder") chain.push({ n: n++, kind: "resolution", label: "At most 1080p" });
    chain.push({ n: n++, kind: "audio", label: "Keep all tracks" }, { n: n++, kind: "subtitles", label: "Keep text subtitles" }, { n: n++, kind: "filetype", label: "Keep as is" },
      { n: n++, kind: "finish", label: "Replace the original if the new file passes the checks" });
    return [{ n: 1, kind: "start", label: "A video in the library folder" },
      { n: 2, kind: "check", label: "Is it worth converting?", detail: "H.264 or older, not HDR or Dolby Vision",
        branches: [{ label: "Yes", steps: chain }, { label: "No", steps: [{ n: n, kind: "skip", label: "Show the reason in Activity" }] }] }];
  }
  var nextFlow = 20; function fid() { return "f" + nextFlow++; } // ids never repeat, also across page loads
  flows.forEach(function (f) { nextFlow = Math.max(nextFlow, (parseInt(f.id.slice(1), 10) || 0) + 1); });
  // Import/export wait for the engine's own flow file format: until then the server answers 501 not_available.
  // ?flowio=1 in the address turns them on in this mock (remembered for the tab).
  var flowIO = false;
  try { if (/[?&]flowio=1/.test(location.search)) sessionStorage.setItem("syp-mock-flowio", "1"); if (/[?&]flowio=0/.test(location.search)) sessionStorage.removeItem("syp-mock-flowio"); flowIO = sessionStorage.getItem("syp-mock-flowio") === "1"; } catch (e) {}
  // An editor names a new flow once, after its library ("Movies"); it is never renamed. A clash adds " 2", " 3", ...
  function flowName(base) { var name = base, n = 1; while (flows.some(function (f) { return f.name === name; })) name = base + " " + (++n); return name; }
  function notAvailable() { return fail(501, "not_available", "This is not available yet. Nothing was changed."); }
  var paused = false, pv = false, slowAt = {};
  // ?preview=slow (remembered for the tab, ?preview=0 turns it off): the estimate takes 2.5 minutes, with progress,
  // so the waiting states can be seen (the two-minute note, the progress bar).
  try { if (/[?&]preview=slow/.test(location.search)) sessionStorage.setItem("syp-mock-slowpv", "1"); if (/[?&]preview=0/.test(location.search)) sessionStorage.removeItem("syp-mock-slowpv"); } catch (e) {}
  function slowPreview() { try { return sessionStorage.getItem("syp-mock-slowpv") === "1"; } catch (e) { return false; } }
  var working = [{ jobId: "j1", library: "Movies", path: "/media/movies/The Long Road (2014).mkv", phase: "converting", percent: 62, etaSeconds: 840, fromBytes: 8.2 * GB, expectedBytes: 4.5 * GB },
                 { jobId: "j2", library: "Movies", path: "/media/movies/Harbour Lights (2019).mkv", phase: "checking", percent: 100, etaSeconds: 0, fromBytes: 7 * GB, expectedBytes: 3 * GB },
                 // The engine doesn't always know the new file's size or the time left yet: null (as on a real server).
                 { jobId: "j3", library: "TV Shows", path: "/media/tv/Season 3/E07.mkv", phase: "converting", percent: 0, etaSeconds: null, fromBytes: 2.4 * GB, expectedBytes: null }]; // just started: no time left known either
  var skippedList = [
    { id: "s1", library: "TV Shows", path: "/media/tv/Show S01E01.mkv", reason: "already_efficient", detail: "Already small for its quality. Converting would save about 3%, less than the 10% minimum.", libraryId: "2" },
    { id: "s2", library: "Movies", path: "/media/movies/Dune Sands (2021).mkv", reason: "hdr_dv", detail: "Skipped to protect the picture.", libraryId: "1" },
    { id: "s3", library: "Movies", path: "/media/movies/Old Town (2008).mkv", reason: "already_hevc", detail: "Nothing to gain.", libraryId: null },
    { id: "s5", library: "Movies", path: "/media/movies/Home Reel.avi", reason: "cannot_keep_track", detail: "The MP4 file type cannot hold the subtitle track, so the file was skipped rather than lose it.", libraryId: "1" },
    { id: "s6", library: "Movies", path: "/media/movies/Cut Loose (2015).mkv", reason: "user_stopped", detail: "You pressed Stop this file while it was converting. Your original is untouched.", libraryId: "1" },
    { id: "s7", library: "Movies", path: "/media/movies/Ocean Floor (2017).mkv", reason: "restored", detail: "Left alone so it is not converted again by surprise.", libraryId: "1" },
    { id: "s8", library: "Movies", path: "/media/movies/Tenet (2020).mkv", reason: "some_future_code", detail: "A reason this version does not know by name.", libraryId: null },
    // Workshop slice 19's four new reasons, one each (the Preview counts one of each too).
    { id: "s10", library: "Movies", path: "/media/movies/Old Reel.rm", reason: "format_not_converted", detail: "RealVideo is not a format this version converts.", libraryId: null },
    { id: "s11", library: "Movies", path: "/media/movies/Soundtrack.mkv", reason: "no_video", detail: "The file holds only audio.", libraryId: null },
    // Stopped, then converted later with "MKV": Convert again on the old name answers 409 renamed (25a-2).
    { id: "s17", library: "Movies", path: "/media/movies/Night Train (2009).mp4", reason: "user_stopped", detail: "You pressed Stop this file while it was converting. Your original is untouched.", libraryId: "1" },
    // S3/S4's new reasons (design's words in ui.js): one each.
    { id: "s18", library: "Movies", path: "/media/movies/Night Sky (2019) DV.mkv", reason: "dv", detail: "This flow leaves Dolby Vision files as they are.", libraryId: "1" },
    { id: "s19", library: "Movies", path: "/media/movies/Streamed (2021) P5.mp4", reason: "dv_profile5", detail: "Dolby Vision profile 5 has no HDR10 layer to keep.", libraryId: "1" },
    { id: "s20", library: "TV Shows", path: "/media/tv/Season 4/E02.mkv", reason: "already_in_format", detail: "It is already HEVC in MKV with the audio you chose.", libraryId: "2" },
    { id: "s13", library: "Movies", path: "/media/movies/Short (2003).avi", reason: "setting_cannot_run", detail: "MP4 is the chosen file type, and it cannot hold this file's audio.", libraryId: "1" },
    // Workshop slice 23's temporary guard: the next scan looks again, so no Convert again and no Change rule.
    { id: "s14", library: "TV Shows", path: "/media/tv/Season 4/E01.mkv", reason: "still_changing", detail: "The file was still being written when we looked.", libraryId: "2" },
    { id: "s15", library: "Movies", path: "/media/movies/Extras/Trailer.mkv", reason: "symlink", detail: "This is a link to /mnt/archive/Trailer.mkv, which is outside this library.", libraryId: null },
    // After workshop slice 23 (file guards) shared files are skipped, not converted (sharedNoSaving is 0).
    { id: "s16", library: "Movies", path: "/media/movies/Night Train (2016).mp4", reason: "shared_file", detail: "Another folder uses this same file (for example Radarr). Changing it would break that.", libraryId: null }
  ];
  for (var gi = 1; gi <= 93; gi++) skippedList.push({ id: "g" + gi, library: "TV Shows", path: "/media/tv/Season 2/E" + (gi < 10 ? "0" : "") + gi + ".mkv", reason: "already_efficient", detail: "Already small for its quality.", libraryId: "2" });
  var failedList = [
    { jobId: "j9", library: "Movies", path: "/media/movies/Cut Short (2012).mkv", title: "The new file did not pass the check", detail: "The new file is 3 minutes shorter than the original, so it was thrown away. Try again; if it fails again, the original file may be damaged.", originalUntouched: true },
    { jobId: "j10", attempts: 3, library: "TV Shows", path: "/media/tv/Season 1/E03.mkv", title: "Not enough space", detail: "Needs about 14 GB free, only 6 GB is free. Free some space, then press Retry.", originalUntouched: true }
  ];
  var doneList = [
    { jobId: "j5", library: "Movies", path: "/media/movies/Ocean Floor (2017).mkv", beforeBytes: 6.4 * GB, afterBytes: 3.3 * GB, originalState: "kept", keptDaysLeft: 5, keptId: "k1" },
    { jobId: "j6", library: "Movies", path: "/media/movies/Paper Moon (2010).mp4", newPath: "/media/movies/Paper Moon (2010).mkv", beforeBytes: 4 * GB, afterBytes: 2.2 * GB, originalState: "kept", keptDaysLeft: 6, keptId: "k2" },
    { jobId: "j7", library: "TV Shows", path: "/media/tv/Season 1/E01.mkv", beforeBytes: 3 * GB, afterBytes: 1.4 * GB, originalState: "deleted", keptDaysLeft: 0, keptId: null },
    // Converted with "Put the new file next to the original": kept, but not in the trash, so no keptId (contract, slice 18).
    { jobId: "j8", library: "Movies", path: "/media/movies/Long Walk (2012).mkv", beforeBytes: 5.1 * GB, afterBytes: 2.6 * GB, originalState: "kept", keptDaysLeft: 0, keptId: null },
    { jobId: "j9", library: "Movies", path: "/media/movies/Cut Loose (2015).mkv", beforeBytes: 4.4 * GB, afterBytes: 2.1 * GB, originalState: "restored", keptDaysLeft: 0, keptId: null },
    // Always standardize (S1): a file kept even though it got bigger (Activity shows it as plain text).
    { jobId: "j12", library: "TV Shows", path: "/media/tv/Season 1/E02.mkv", beforeBytes: 1.2 * GB, afterBytes: 1.3 * GB, originalState: "deleted", keptDaysLeft: 0, keptId: null }
  ];
  var keptList = [
    { keptId: "k1", library: "Movies", path: "/media/movies/Ocean Floor (2017).mkv", originalBytes: 6.4 * GB, nowBytes: 3.3 * GB, deleteOn: iso(-5 * 864e5) },
    { keptId: "k2", library: "Movies", path: "/media/movies/Paper Moon (2010).mp4", newPath: "/media/movies/Paper Moon (2010).mkv", originalBytes: 4 * GB, nowBytes: 2.2 * GB, deleteOn: iso(-6 * 864e5) }
  ];

  // Workers (redesign D; workshop S2, UI-API-CONTRACT.md 20:25Z): one, "This computer" (id "this"). It starts stopped,
  // after an update that stopped it (the one-time notice). ?worker=running or ?worker=paused (quiet hours) starts it that
  // way instead. gpu is {found, vendor}, or null when detection didn't run.
  var worker = { id: "this", name: "This computer", host: "nas01", on: false, stopping: false, quiet: false, filesAtOnce: 1, gpu: { found: true, vendor: "NVIDIA" } }, stoppedByUpdate = true, stopAt = 0;
  try { var sw = JSON.parse(sessionStorage.getItem("syp-mock-workers")); if (sw && sw.w.id === "this") { worker = sw.w; stoppedByUpdate = sw.u; stopAt = sw.t; } } catch (e) {}
  var wm = /[?&]worker=(running|paused)/.exec(location.search);
  if (wm) { worker.on = true; worker.quiet = wm[1] === "paused"; stoppedByUpdate = false; }
  function saveWorkers() { try { sessionStorage.setItem("syp-mock-workers", JSON.stringify({ w: worker, u: stoppedByUpdate, t: stopAt })); } catch (e) {} }
  function workerOn() { return worker.on || worker.stopping; }
  function workerView() {
    if (worker.stopping && Date.now() >= stopAt) { worker.stopping = false; saveWorkers(); } // the current files finished
    var conv = (worker.on && !worker.quiet) || worker.stopping ? working.slice(0, worker.filesAtOnce).map(function (j) { return { jobId: j.jobId, path: j.path, percent: j.percent, etaSeconds: j.etaSeconds }; }) : [];
    var state = worker.stopping ? "stopping" : !worker.on ? "stopped" : worker.quiet ? "quiet_hours" : conv.length ? "converting" : "idle";
    return { id: worker.id, name: worker.name, host: worker.host, state: state, filesAtOnce: worker.filesAtOnce, converting: conv,
      quietUntil: worker.quiet ? new Date(new Date().setHours(31, 0, 0, 0)).toISOString() : null, gpu: worker.gpu };
  }

  // A library's status (workshop slice A, UI-API-CONTRACT.md 20:25Z): first that applies, converting > waiting > scanned.
  // Slice A's library status, failed count and madeWith (UI-API-CONTRACT.md 20:25Z). The sample libraries keep ui-40's
  // variety for the cards; a library added here (first-run, Add library) follows what was done to it, so first-run's
  // "Not yet" shows it waiting for a worker.
  var KIND = { "1": { kind: "converting", converting: 2 }, "2": { kind: "scanned" }, "4": { kind: "waiting_for_worker", toDo: 40 }, "3": { kind: "scanned" } }, FAIL = { "2": 1, "3": 3 };
  function withStatus(l) {
    var x = copy(l), on = workerOn(), f = flows[find(flows, "id", l.flowId)];
    var k = KIND[l.id] || (!l.started ? { kind: "not_scanned" } : on && l.workingFiles ? { kind: "converting", converting: l.workingFiles }
      : !on && l.waitingFiles ? { kind: "waiting_for_worker", toDo: l.waitingFiles } : { kind: "scanned" });
    var scanned = k.kind !== "not_scanned" && k.kind !== "scanning";
    x.status = Object.assign({ videos: scanned ? 1284 : null, foundSoFar: null, lastScan: scanned ? iso(-3 * 3600e3) : null, nextScan: l.nextScan, converting: 0, toDo: 0 }, k);
    x.failedFiles = FAIL[l.id] || 0; x.madeWith = f ? f.madeWith : "slider"; if (f) x.tag = tagOf(f); return x;
  }
  // Flows per level (piece B, UI-API-CONTRACT.md 20:40Z). A flow's setting lives on the libraries that use it (the first
  // one is read; a save is copied to all), or on the flow itself while no library uses it. kind and tag (B4) are built here
  // the way the server builds them.
  var QN = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];
  var AUDIO_SHORT = { stereo_aac: "Stereo AAC", stereo_aac_surround: "Stereo AAC, plus surround as it is", eac3_surround: "Surround E-AC-3", ac3_surround: "Surround AC-3", stereo_opus: "Stereo Opus", as_is: "As it is" }; // the server's words (S6 rules.AudioWords)
  function recOf(f) { var i = find(libs, "flowId", f.id); return i >= 0 ? libs[i] : (f.rec = f.rec || { quality: 3, audio: "stereo_aac", settingKind: "slider", settingLabel: "Balanced" }); }
  function kindOf(f) { var r = recOf(f); return f.madeWith === "builder" ? "advanced" : f.madeWith === "legacy" ? "earlier" : r.sel ? "standard" : "basic"; }
  function tagOf(f) {
    var k = kindOf(f), r = recOf(f), q = (r.sel ? r.sel.quality : r.quality) || 3;
    if (k === "advanced") return "Advanced \u00b7 Custom"; if (k === "earlier") return "Earlier install";
    if (k === "basic") return "Basic \u00b7 " + QN[q - 1];
    var x = r.sel, parts = ["Standard"]; if (x.format && x.format !== "hevc") parts.push(x.format.toUpperCase());
    parts.push(QN[q - 1], x.resolution === "keep" || !x.resolution ? "same resolution" : "at most " + x.resolution + "p",
      !x.maxBitrate || x.maxBitrate === "none" ? "no bitrate limit" : ({ high: "High", medium: "Medium", low: "Low" }[x.maxBitrate] || x.maxBitrate) + " bitrate", AUDIO_SHORT[x.audio] || x.audio);
    return parts.join(" \u00b7 ");
  }
  function flowView(f) { var v = copy(f); delete v.rec; v.kind = kindOf(f); v.tag = tagOf(f); return v; }
  // Standard choices that the Basic slider can show (B3 "Switch to Basic").
  // S6: stereo_aac + only my languages, or as_is (with or without), are the three Basic audio choices.
  function fitsBasic(x) { return x.format === "hevc" && (x.resolution || "keep") === "keep" && x.fileType === "mkv" && (x.maxBitrate || "none") === "none" &&
    ((x.audio === "stereo_aac" && x.onlyMyLanguages !== false) || x.audio === "as_is") && x.subtitles === "keep_all" && x.afterConverting === "replace"; }
  function settingOf(r) { // the setting answer, as GET .../setting gives it, for a library or a flow's record
    var av = avail();
    if (r.sel) return Object.assign({ kind: "selectors", warnings: warningsFor(r.sel), fitsBasic: fitsBasic(r.sel) }, av, r.sel);
    if (r.settingKind === "custom") return Object.assign({ kind: "custom", warnings: [] }, av);
    return Object.assign({ kind: "slider", quality: r.quality || (r.settingLabel === "Smaller" ? 2 : 3), positions: SLIDER, warnings: [],
      audio: r.audio || "stereo_aac", becomes: { video: "HEVC", resolution: "same", fileType: "MKV" } }, av);
  }
  function applySetting(r, b) { // a PUT body onto a record: {quality, audio} = Basic, selector fields = Standard
    if (b.speed) { r.sel = copy(b); r.settingKind = "selectors"; r.settingLabel = QN[(b.quality || 3) - 1]; return; }
    delete r.sel; r.quality = b.quality || 3; r.audio = b.audio || "stereo_aac"; r.settingKind = "slider"; r.settingLabel = QN[r.quality - 1];
  }

  // Activity's Files view (redesign C; workshop S5, UI-API-CONTRACT.md 21:00Z), built from the lists above. Codec,
  // resolution and length are made up from the name; Unprocessed has two retrying files first, then 590 waiting.
  try { if (/[?&]finish=1/.test(location.search)) sessionStorage.setItem("syp-mock-finish", "1"); if (/[?&]finish=0/.test(location.search)) sessionStorage.removeItem("syp-mock-finish"); } catch (e) {}
  function finishing() { try { return sessionStorage.getItem("syp-mock-finish") === "1"; } catch (e) { return false; } }
  function nameOf2(p) { return p.split("/").pop(); }
  function probe(path) { var h = 0; for (var i = 0; i < path.length; i++) h = (h * 31 + path.charCodeAt(i)) >>> 0;
    return { codec: ["H.264", "H.264", "MPEG-4", "HEVC", "VC-1"][h % 5], resolution: ["1080p", "4K", "720p", "1080p", "480p"][(h >>> 3) % 5], durationSec: 1800 + h % 6000 }; }
  function fileOf(o, extra) {
    var lid = o.libraryId || (libs[find(libs, "name", o.library)] || {}).id || null, li = find(libs, "id", lid), root = li >= 0 ? libs[li].path : "", d = o.path.slice(0, o.path.length - nameOf2(o.path).length).replace(/\/+$/, "");
    return Object.assign({ libraryId: lid, library: o.library, path: o.path, folder: root && d.indexOf(root) === 0 && d.length > root.length ? d.slice(root.length + 1) : ".", name: nameOf2(o.path),
      bytes: o.bytes || o.beforeBytes || o.fromBytes || 3 * GB, newPath: o.newPath || undefined }, probe(o.path), extra);
  }
  var RETRYING = [{ jobId: "j31", library: "Movies", path: "/media/movies/Busy Night (2011).mkv", retryAt: iso(-12 * 60e3), attempts: 1, detail: "The file was in use by another program." },
    { jobId: "j32", library: "TV Shows", path: "/media/tv/Season 3/E07.mkv", retryAt: iso(-31 * 60e3), attempts: 2, detail: "There was not enough free space." }];
  function filesFor(show) {
    if (show === "processing") return workerOn() ? working.map(function (j, k) { return fileOf(j, { id: "job-" + j.jobId, jobId: j.jobId, percent: j.percent, etaSeconds: j.etaSeconds,
      convertingTo: "HEVC · MKV", encoder: "x265 (processor)", decoder: "H.264 (processor)", bitrateKbps: 4800 - k * 700, fps: 62 - k * 9, worker: "This computer" }); }) : [];
    if (show === "unprocessed") {
      var r = RETRYING.map(function (j) { return fileOf(j, { id: "job-" + j.jobId, status: "retrying", retryAt: j.retryAt, attempts: j.attempts, detail: j.detail }); });
      for (var n = 1; n <= 590; n++) r.push(fileOf({ library: "Movies", path: "/media/movies/Queued " + (n < 10 ? "00" : n < 100 ? "0" : "") + n + ".mkv" }, { id: "q" + n, status: "waiting" }));
      return r;
    }
    if (show === "processed") return doneList.map(function (d, k) { var f = flows[find(flows, "id", (libs[find(libs, "name", d.library)] || {}).flowId)];
      return fileOf(d, { id: "done-" + d.jobId, jobId: d.jobId, beforeBytes: d.beforeBytes, afterBytes: d.afterBytes, flow: f ? { id: f.id, name: f.name } : null, finishedAt: iso(-(k + 1) * 3600e3),
        originalState: d.originalState === "kept" ? (d.keptId ? "kept" : "kept_beside") : d.originalState, keptId: d.keptId, keptDaysLeft: d.keptDaysLeft }); });
    if (show === "skipped") return skippedList.map(function (x) { return fileOf(x, { id: "skip-" + x.id, skippedId: x.id, reason: x.reason, detail: x.detail }); });
    return failedList.map(function (x) { return fileOf(x, { id: "fail-" + x.jobId, jobId: x.jobId, title: x.title, detail: x.detail, attempts: x.attempts || 1 }); });
  }
  function filesFiltered(show, p) {
    var q = (p.q || "").toLowerCase();
    return filesFor(show).filter(function (f) {
      return (!p.library || f.libraryId === p.library) && (!q || f.name.toLowerCase().indexOf(q) >= 0 || (f.newPath && nameOf2(f.newPath).toLowerCase().indexOf(q) >= 0)) && (!(+p.kept) || show !== "processed" || !!f.keptId);
    });
  }

  function summary() {
    // Waiting to be freed = the space the kept originals will give back (original - new); the whole
    // originals' size is only the Kept originals figure (keptBytes). Contract, workshop response to F1.
    var kept = 0; keptList.forEach(function (k) { kept += k.originalBytes - k.nowBytes; });
    var freed = 0; libs.forEach(function (l) { freed += l.freedBytes; }); // the libraries' own numbers, added up
    return { freedBytes: freed, waitingBytes: kept, workingFiles: paused || !workerOn() ? 0 : working.length, waitingFiles: 590, paused: paused, workersRunning: workerOn(),
             counts: { skipped: skippedList.length, failed: failedList.length } };
  }
  function previewResult() {
    // About 10-20% of 1.1 TB, as first-run step 1 promises. files = canShrink + skipped (1284 = 1174 + 110; the Skipped list has 110 rows too, F4).
    // Since workshop slice 23 (file guards) shared files are skipped (skipped.shared_file) and sharedNoSaving is 0 (addendum 12).
    return { stale: false, status: "ready", estimateBytesLow: 110 * GB, estimateBytesHigh: 220 * GB, files: 1284, totalBytes: 1100 * GB, canShrink: 1174, sharedNoSaving: 0,
             skipped: { already_efficient: 71, hdr_dv: 5, format_not_converted: 1, no_video: 1, setting_cannot_run: 1, still_changing: 1, symlink: 1, shared_file: 29 } };
  }

  var ORDER = ["newest", "oldest", "largest", "smallest"], CAPS = ["none", "high", "medium", "low"]; // S6 levels
  // Which choices this version can save, on every setting answer (addendum 13 and its correction). ?selectors= picks
  // the state (remembered for the tab): now (default: the server on dev since slice 25b, every audio and subtitle
  // choice, the file type kept when replacing), keep (slice 25a, keep_all only), some (wider, not complete), full
  // (everything, file types too), old (an older server: no lists at all).
  try { var sm = /[?&]selectors=(now|keep|some|full|old)/.exec(location.search); if (sm) sessionStorage.setItem("syp-mock-avail", sm[1]); } catch (e) {}
  // What this version can save (workshop S6, UI-API-CONTRACT.md 21:10Z): VP9 isn't made on this computer; the
  // bitrate ladder (Mbps by format, level and resolution) is research's. ?selectors=old: an older server (no lists).
  var LADDER = { hevc: { high: [20, 12, 8, 4, 2], medium: [12, 8, 5, 2.5, 1.2], low: [8, 5, 3, 1.5, 0.8] }, h264: { high: [35, 20, 12, 6, 3], medium: [20, 12, 8, 4, 2], low: [12, 8, 5, 2.5, 1.2] },
    av1: { high: [14, 8, 5.5, 2.8, 1.4], medium: [8, 5.5, 3.5, 1.8, 0.9], low: [5.5, 3.5, 2, 1, 0.6] }, vp9: { high: [16, 10, 6.5, 3.2, 1.6], medium: [10, 6.5, 4, 2, 1], low: [6.5, 4, 2.5, 1.2, 0.7] } };
  function ladder() { var o = {}; Object.keys(LADDER).forEach(function (c) { o[c] = {}; Object.keys(LADDER[c]).forEach(function (l) { var r = LADDER[c][l]; o[c][l] = { "2160": r[0], "1440": r[1], "1080": r[2], "720": r[3], sd: r[4] }; }); }); return o; }
  function avail() {
    var mode = "now"; try { mode = sessionStorage.getItem("syp-mock-avail") || "now"; } catch (e) {}
    if (mode === "old") return {};
    var types = ["mkv", "mp4", "webm", "keep"];
    return { availableFormats: ["hevc", "h264", "av1"], availableAudio: ["stereo_aac", "stereo_aac_surround", "eac3_surround", "ac3_surround", "stereo_opus", "as_is"],
      availableSubtitles: ["keep_all", "my_languages", "text_only", "srt"], availableFileTypes: { replace: types, beside: types, folder: types }, bitrateLadder: ladder() };
  }
  // The setting's warnings (design's wording; S6 retires the HDR one): the 25a-2 rename when Replace changes the file
  // type, and the rough-picture warning for a Low limit. Only the mock holds a copy of them.
  function warningsFor(s) {
    var w = []; if (!s) return w;
    if (s.maxBitrate === "low") w.push("Low may look blocky in dark or fast scenes for 4K HDR files (" + (LADDER[s.format || "hevc"] || LADDER.hevc).low[0] + " Mbps). Choose Medium or No limit to avoid that.");
    if (s.afterConverting === "replace" && s.fileType && s.fileType !== "keep")
      w.push("Changing the file type renames each file (" + (s.fileType === "mp4" ? "movie.mkv becomes movie.mp4" : "movie.mp4 becomes movie." + s.fileType) + "). Radarr, Sonarr, Plex or Jellyfin may lose track of the renamed files: after converting, rescan this library in those apps.");
    return w;
  }
  // "Is this the right folder?" (redesign A3; workshop slice A, UI-API-CONTRACT.md 20:25Z): the first 5 videos A to Z.
  // A folder's first answer is still looking (120 found, two being checked); the next is finished. Folder names: "few"
  // (3 videos), "unreadable" (one can't be read).
  var fiveAsked = {};
  function firstFive(root) {
    root = root.replace(/\/+$/, ""); var looking = (fiveAsked[root] = (fiveAsked[root] || 0) + 1) === 1, few = /few/.test(root);
    var all = [["12 Angry Men (1957)", "12 Angry Men (1957).mkv", 8.1, "H.264", "1080p", 5760], ["2001 A Space Odyssey (1968)", "2001 A Space Odyssey (1968).mkv", 54, "H.264", "4K", 8940],
      ["Alien (1979)", "Alien (1979).mp4", 6.4, "HEVC", "1080p", 7020], [".", "Amelie (2001).avi", 1.4, "MPEG-4", "480p", 7320], ["Arrival (2016)", "Arrival (2016).mkv", 11, "H.264", "4K", 6960]].slice(0, few ? 3 : 5);
    var items = all.map(function (f, i) { var ready = !looking || i < 3;
      return { path: root + "/" + (f[0] === "." ? "" : f[0] + "/") + f[1], folder: f[0], name: f[1], bytes: f[2] * GB, state: ready ? "ready" : "checking",
        codec: ready ? f[3] : null, resolution: ready ? f[4] : null, durationSec: ready ? f[5] : null }; });
    if (/unreadable/.test(root)) { items[1].state = "unreadable"; items[1].codec = items[1].resolution = items[1].durationSec = null; }
    var total = few ? 3 : 1284;
    return { items: items, total: looking ? Math.min(120, total) : total, stillLooking: looking };
  }
  function gone() { return fail(404, "not_found", "That item is gone. Nothing was changed. Reload the page."); }
  function removeFrom(arr, key, id) { var i = find(arr, key, id); return i < 0 ? null : arr.splice(i, 1)[0]; }

  // Activity state survives page changes within the tab, like settings and libraries do.
  try { var sa = JSON.parse(sessionStorage.getItem("syp-mock-act")); if (sa) { skippedList = sa.s; failedList = sa.f; doneList = sa.d; keptList = sa.k; working = sa.w; paused = sa.p; } } catch (e) {}
  function saveAct() { try { sessionStorage.setItem("syp-mock-act", JSON.stringify({ s: skippedList, f: failedList, d: doneList, k: keptList, w: working, p: paused })); } catch (e) {} }

  var m = {
    login: function (p, b) {
      if (auth && b.token !== token()) return fail(401, "bad_token", "That token was not accepted. Check it and try again.");
      store("syp-mock-session", "1"); return ok();
    },
    logout: function () { store("syp-mock-session", null); return ok(); },
    getSettings: function () { return ok(settings); },
    patchSettings: function (p, b) {
      var level = b.controlLevel;
      if (level && ["basic", "standard", "advanced"].indexOf(level) < 0) return fail(400, "bad_value", "Control level must be Basic, Standard or Advanced. Nothing was changed.");
      if (b.quietHours) { var qf = "from" in b.quietHours ? b.quietHours.from : settings.quietHours.from, qt = "to" in b.quietHours ? b.quietHours.to : settings.quietHours.to;
        if (!qf !== !qt) return fail(400, "bad_value", "Set both a start and an end, or leave both empty. Nothing was changed."); }
      if (b.queueOrder != null && ORDER.indexOf(b.queueOrder) < 0) return fail(400, "invalid", "Do first must be newest, oldest, largest or smallest files. Nothing was changed.");
      if (b.keepOriginalsDays != null && !(b.keepOriginalsDays >= 0 && b.keepOriginalsDays <= 365)) return fail(400, "bad_value", "Keep originals for 0 to 365 days. Nothing was changed.");
      // Deep merge. A secret (apiKey, token) is never stored or returned: only that one is saved shows as hasKey.
      (function merge(dst, src) {
        Object.keys(src).forEach(function (k) {
          if (k === "authRequired" || k === "trashPath" || k === "updateAvailable") return;
          if (k === "apiKey" || k === "token") { if (src[k]) dst.hasKey = true; return; }
          if (Array.isArray(src[k])) dst[k] = src[k].slice(); else if (src[k] && typeof src[k] === "object") merge(dst[k] = dst[k] || {}, src[k]); else dst[k] = src[k];
        });
      })(settings, b);
      persist(); return ok(settings);
    },
    getToken: function () { return ok({ token: token() }); },
    rotateToken: function () { store("syp-mock-token-n", String(+(read("syp-mock-token-n") || 0) + 1)); return ok({ token: token() }); },
    getHealth: function () {
      return ok({ version: "1.1.0 (sample)", logsTail: ["20:01:12 started", "20:01:13 watching /media/movies", "20:02:40 converted Ocean Floor (2017).mkv", "20:03:02 skipped Dune Sands (2021).mkv: HDR"],
        checks: [{ id: "converter", label: "Video converter found", status: "ok", detail: "" }, { id: "encoder", label: "Hardware encoder", status: "warn", detail: "Not found. That is fine; the processor will be used" },
                 { id: "disk", label: "Free space", status: "ok", detail: "1.4 TB free" }, { id: "db", label: "Library database", status: "ok", detail: "" }] });
    },
    backup: function () { return Promise.resolve({ blob: function () { return Promise.resolve(new Blob(["Sample backup from the stand-in server. A real backup holds your settings and the list of converted files."], { type: "text/plain" })); } }); },
    listLibraries: function () { return ok(list(libs.map(withStatus))); }, // withStatus adds the flow's tag too
    addLibrary: function (p, b) {
      var l = { id: String(libs.length + 10), name: b.name || "New library", path: b.path || "", state: "not_started", started: false, workingFiles: 0, waitingFiles: 0, nextScan: iso(-6 * 3600e3), freedBytes: 0, settingLabel: "Balanced", settingKind: "slider" };
      var fi = b.flowId ? find(flows, "id", b.flowId) : -1;
      if (b.flowId && fi < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      if (fi >= 0) { var r0 = copy(recOf(flows[fi])); ["quality", "audio", "sel", "settingKind", "settingLabel"].forEach(function (k) { if (r0[k] !== undefined) l[k] = r0[k]; }); flows[fi].usedBy.push(l.name); l.flowId = b.flowId; delete flows[fi].rec; libs.push(l); saveLibs(); return ok(withStatus(l)); }
      var f = { id: fid(), name: flowName(l.name), usedBy: [l.name], madeWith: "slider", changedAt: iso(0) }; flows.push(f); l.flowId = f.id;
      libs.push(l); saveLibs(); return ok(l);
    },
    // Try these folder names to see each state: "missing", "readonly", "full", "empty", "nocodec"; and the two
    // notes that pass with a warning: "space_unknown", "unreadable_folders" (contract, workshop slice 20).
    checkLibrary: function (p, b) {
      var path = b.path || "", codes = { missing: "unreadable", readonly: "read_only", full: "low_space", empty: "no_videos", nocodec: "no_converter" }, bl = [], k;
      // Messages as the server words them (read_only carries fix steps for the platform); the others send none here,
      // so the page's own sentence for that code shows (its fallback).
      var said = { full: "Only 6 GB free on this disk. Free up space (about 15 GB is enough to start), then press Check again.",
        readonly: "This folder is read-only. Make it writable, then press Check again. shrink-your-shit runs as the user syp. On this server: sudo chown -R syp:syp " + path + " (or add syp to the folder's group and give the group write access)." };
      for (k in codes) if (path.indexOf(k) >= 0) bl.push({ code: codes[k], message: said[k] || "" });
      var notes = bl.length ? [] : [{ code: "hardlinks", message: "212 files are shared with another folder (for example by Radarr). We will leave them alone, because changing one would break the link." }, { code: "no_hw_encoder", message: "" }];
      if (!bl.length && path.indexOf("space_unknown") >= 0) notes.unshift({ code: "space_unknown", message: "We couldn't check how much space is free on this disk. Files here are only converted when there is room, so check that the disk is working." });
      if (!bl.length && path.indexOf("unreadable_folders") >= 0) notes.unshift({ code: "unreadable_folders", message: "3 folders inside could not be read. Files in them will not be converted." });
      return ok({ ok: !bl.length, blockers: bl, notes: notes, videoCount: 1284, totalBytes: 1100 * GB });
    },
    browseDirs: function (p) { return ok({ path: p.path || "/media", parent: "/", dirs: ["movies", "tv", "home"] }); },
    getLibrary: function (p) { var i = find(libs, "id", p.id); return i < 0 ? fail(404, "not_found", "That library is gone. Nothing was changed.") : ok(withStatus(libs[i])); },
    patchLibrary: function (p, b) { var i = find(libs, "id", p.id); if (i < 0) return fail(404, "not_found", "That library is gone. Nothing was changed."); Object.assign(libs[i], { name: b.name || libs[i].name, path: b.path || libs[i].path }); saveLibs(); return ok(libs[i]); },
    // Record only: its queued and running jobs stop (originals untouched); kept originals and Activity rows stay.
    deleteLibrary: function (p) {
      var r = removeFrom(libs, "id", p.id); saveLibs();
      if (r) working = working.filter(function (j) { return j.library !== r.name; });
      return r ? ok() : gone();
    },
    scanLibrary: function (p) { var i = find(libs, "id", p.id); if (i >= 0) { libs[i].started = true; libs[i].state = "working"; libs[i].waitingFiles = libs[i].waitingFiles || 590; saveLibs(); } return ok(); },
    getSliderTable: function () { return ok({ positions: SLIDER }); },
    getSetting: function (p) {
      var i = find(libs, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That library is gone. Nothing was changed.");
      var av = avail();
      if (libs[i].sel) return ok(Object.assign({ kind: "selectors", warnings: warningsFor(libs[i].sel) }, av, libs[i].sel));
      if (libs[i].settingKind === "custom") return ok(Object.assign({ kind: "custom", warnings: [] }, av));
      // A Basic flow (redesign A1, contract A5 as proposed): its audio choice and its fixed facts.
      return ok(Object.assign({ kind: "slider", quality: libs[i].quality || (libs[i].settingLabel === "Smaller" ? 2 : 3), positions: SLIDER, warnings: [],
        audio: libs[i].audio || "stereo_aac", becomes: { video: "HEVC", resolution: "same", fileType: "MKV" } }, av));
    },
    putSetting: function (p, b) {
      var i = find(libs, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That library is gone. Nothing was changed.");
      // "Keep only my languages" before any language is chosen (slice 25b): the server's words.
      if (!(settings.languages || []).length && (b.speed ? b.audio === "my_languages" || b.subtitles === "my_languages" : b.audio && b.audio !== "keep_all")) return fail(400, "languages_needed", "Choose your languages first, in Settings > General.");
      var f = flows[find(flows, "id", libs[i].flowId)];
      if (f && f.usedBy.length > 1 && !b.sharedFlowChoice) return fail(409, "flow_shared", "Another library uses this setting. Nothing was changed.", { usedBy: f.usedBy.slice() }); // every library on the flow, this one too, as the server sends it
      if (f && f.usedBy.length > 1 && b.sharedFlowChoice === "make_copy") {
        f.usedBy = f.usedBy.filter(function (n) { return n !== libs[i].name; });
        var nf = { id: fid(), name: flowName(libs[i].name + " (own copy)"), usedBy: [libs[i].name], madeWith: b.speed ? "selectors" : "slider", changedAt: iso(0) };
        flows.push(nf); libs[i].flowId = nf.id; saveLibs();
      }
      if (b.speed && b.maxBitrate != null && CAPS.indexOf(b.maxBitrate) < 0) return fail(400, "invalid", "Max bitrate must be one of the listed choices. Nothing was changed.");
      if (b.quality && !b.speed) delete libs[i].sel; // a slider save replaces Standard choices
      if (b.quality && !b.speed) libs[i].audio = b.audio || "stereo_aac"; // Basic's audio choice
      if (b.quality) { libs[i].quality = b.quality; libs[i].settingLabel = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"][b.quality - 1]; libs[i].settingKind = "slider"; saveLibs(); }
      if (b.speed) { libs[i].sel = copy(b); libs[i].settingKind = "selectors"; libs[i].settingLabel = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"][(b.quality || 3) - 1]; saveLibs(); }
      return ok();
    },
    replaceCustom: function (p) { var i = find(libs, "id", p.id); if (i >= 0) { libs[i].settingKind = "slider"; libs[i].settingLabel = "Balanced"; libs[i].state = "idle"; libs[i].quality = 3; saveLibs(); } return ok(); },
    putLibraryFlow: function (p, b) {
      var i = find(libs, "id", p.id), nf = find(flows, "id", b.flowId);
      if (i < 0 || nf < 0) return gone();
      // The library now follows the new flow's setting (piece B): copy it before the library joins.
      var r = copy(recOf(flows[nf])); delete libs[i].sel; ["quality", "audio", "sel", "settingKind", "settingLabel"].forEach(function (k) { if (r[k] !== undefined) libs[i][k] = r[k]; });
      flows.forEach(function (f) { if (f.id !== b.flowId && f.usedBy.indexOf(libs[i].name) >= 0 && f.usedBy.length === 1) f.rec = copy(recOf(f)); f.usedBy = f.usedBy.filter(function (n) { return n !== libs[i].name; }); });
      flows[nf].usedBy.push(libs[i].name); libs[i].flowId = b.flowId; delete flows[nf].rec; saveLibs(); return ok();
    },
    // Answers "computing" first, then "ready", so the waiting state gets exercised.
    preview: function (p, b) {
      var li = find(libs, "id", p.id);
      var bs = b.setting || {};
      if (bs.speed && !(settings.languages || []).length && (bs.audio === "my_languages" || bs.subtitles === "my_languages")) return fail(400, "languages_needed", "Choose your languages first, in Settings > General.");
      if (b.sampleFile && li >= 0 && b.sampleFile.indexOf(libs[li].path + "/") !== 0) return fail(400, "outside_library", "That file is not inside this library. Nothing was changed.");
      if (slowPreview()) {
        var t = Date.now() - (slowAt[p.id] = slowAt[p.id] || Date.now());
        if (t < 150000) return ok({ status: "computing", stale: false, progress: { done: Math.floor(1284 * t / 150000), total: 1284 } });
        delete slowAt[p.id];
      } else if ((pv = !pv)) return ok({ status: "computing", stale: false });
      var r = previewResult(), q = b.setting && b.setting.quality || 3;
      r.warnings = warningsFor(b.setting); // for the setting as given, saved or not (contract, 25a update)
      if (b.sampleFile) r.sample = { lines: ["Video: converted to the smaller format (" + SLIDER[q - 1].name.toLowerCase() + " setting)", "Audio: 2 tracks kept", "Subtitles: 3 kept (all text)", "File type: MKV, unchanged", "Replaces the original; original kept for 7 days"],
        expectedBytesLow: ((b.sampleFile.length % 5) + 2.2 - 0.2 * q) * GB, expectedBytesHigh: ((b.sampleFile.length % 5) + 3.1 - 0.2 * q) * GB };
      return ok(r);
    },
    listFiles: function (p) {
      var i = find(libs, "id", p.id), root = i < 0 ? "/media" : libs[i].path;
      return ok(list([{ path: root + "/Alien (1979).mkv", bytes: 9 * GB }, { path: root + "/Heat (1995).mkv", bytes: 7 * GB }, { path: root + "/Show S02E04.mkv", bytes: 4.1 * GB }]));
    },
    firstFiles: function (p) { var i = find(libs, "id", p.id); return i < 0 ? fail(404, "not_found", "There is no such library.") : ok(firstFive(libs[i].path)); },
    firstFilesFor: function (p, b) { return ok(firstFive(b.path || "/media")); },
    activitySummary: function () { return ok(summary()); },
    activityFiles: function (p) {
      // ?finish=1 (remembered for the tab): the first converting file advances quickly and, at 100%, moves to Processed,
      // so the "finished" message and a live log can be seen. Otherwise it only creeps.
      if (workerOn() && working[0]) {
        if (finishing()) {
          working[0].percent = Math.min(100, working[0].percent + 9);
          if (working[0].percent >= 100) { var j = working.shift(); doneList.unshift({ jobId: j.jobId, library: j.library, path: j.path, beforeBytes: j.fromBytes, afterBytes: Math.round(j.fromBytes * 0.55), originalState: "deleted", keptDaysLeft: 0, keptId: null }); saveAct(); }
        } else if (working[0].percent < 99) working[0].percent++; // moves a little on every look
      }
      var all = filesFiltered(p.show, p), counts = {};
      ["unprocessed", "processing", "processed", "skipped", "failed"].forEach(function (k) { counts[k] = filesFiltered(k, { library: p.library, q: p.q }).length; });
      counts.keptOriginals = filesFiltered("processed", { library: p.library, q: p.q, kept: 1 }).length;
      var off = +p.offset || 0, lim = +p.limit || 50;
      return ok({ items: all.slice(off, off + lim), total: all.length, counts: counts });
    },
    fileLog: function (p) {
      var j = working.filter(function (w) { return "job-" + w.jobId === p.id; })[0], live = !!j && workerOn(), from = +p.from || 0;
      var lines = ["ffprobe: Input #0, matroska,webm", "  Stream #0:0: Video: h264 (High), yuv420p, 1920x1080, 23.98 fps", "  Stream #0:1(eng): Audio: ac3, 48000 Hz, 5.1(side)",
        "ffmpeg -i input.mkv -map 0 -c:v libx265 -crf 23 -c:a copy -c:s copy output.mkv", "frame= 1200 fps= 62 q=28.0 size= 102400kB time=00:00:50.05 bitrate=4800.0kbits/s speed=2.58x"];
      var text = from === 0 ? lines.join("\n") + "\n" : live ? "frame= " + (1200 + from * 3) + " fps= 61 q=28.0 time=00:0" + Math.min(9, 1 + (from % 9)) + ":00.00 bitrate=4790.0kbits/s\n" : "";
      // S5's limits: Ocean Floor's finished log comes in three pages, already cut with the server's line; E01 is from
      // before S5 (a summary, no text).
      if (p.id === "done-j5") text = ["page 1: " + lines.join(" | ") + "\n", "… 512 KiB left out …\n", "page 3: the end of the log\n"][from] || "";
      if (p.id === "done-j7") text = "";
      var steps = [{ step: "found", at: iso(-5 * 3600e3), ok: true, words: "Found in the library." }, { step: "checked", at: iso(-5 * 3600e3 + 60e3), ok: true, words: "H.264, 1080p: will be converted." }];
      if (!live && /^done-/.test(p.id)) steps.push({ step: "converted", at: iso(-4 * 3600e3), ok: true, words: "Converted to HEVC." }, { step: "verified", at: iso(-4 * 3600e3 + 30e3), ok: true, words: "Plays all the way through and looks like the original." }, { step: "replaced", at: iso(-4 * 3600e3 + 40e3), ok: true, words: "Replaced the original, which is kept for now." });
      if (/^fail-/.test(p.id)) steps.push({ step: "converted", at: iso(-3 * 3600e3), ok: false, words: "Converting did not finish." });
      return ok({ summary: steps, text: text, next: from + 1, live: live });
    },
    listWorkers: function () { return ok({ workers: [workerView()], stoppedByUpdate: stoppedByUpdate, basicFlowsChanged: stoppedByUpdate && /[?&]basicchanged=1/.test(location.search) }); }, // ?basicchanged=1: S4's flag
    startWorker: function (p) { if (p.id !== "this") return fail(404, "not_found", "There is no such worker. Nothing was changed."); worker.on = true; worker.stopping = false; stoppedByUpdate = false; saveWorkers(); return ok(workerView()); },
    stopWorker: function (p, b) {
      if (p.id !== "this") return fail(404, "not_found", "There is no such worker. Nothing was changed.");
      var when = b.when || "after_current"; if (when !== "after_current" && when !== "now") return fail(400, "invalid", "Stop after the current files or now. Nothing was changed.");
      var busy = workerView().converting.length; worker.on = false;
      if (when === "after_current" && busy) { worker.stopping = true; stopAt = Date.now() + 6000; } else worker.stopping = false; // nothing converting: stopped at once
      saveWorkers(); return ok(workerView());
    },
    setWorker: function (p, b) {
      if (!(b.filesAtOnce >= 1 && b.filesAtOnce <= 8 && b.filesAtOnce % 1 === 0)) return fail(400, "invalid_setting", "Enter a whole number from 1 to 8.");
      worker.filesAtOnce = b.filesAtOnce; saveWorkers(); return ok(workerView());
    },
    dismissWorkerNotice: function () { stoppedByUpdate = false; saveWorkers(); return ok({ stoppedByUpdate: false }); },
    pause: function () { paused = true; saveAct(); return ok(); },
    resume: function () { paused = false; saveAct(); return ok(); },
    queue: function () {
      if (!paused && working[0] && working[0].percent < 99) working[0].percent++; // moves a little on every look
      return ok({ working: paused ? [] : working, upNext: [{ library: "Movies", count: 590, order: settings.queueOrder }, { library: "TV Shows", count: 0, order: "" }] });
    },
    // A running file answers 202 "stopping" and leaves the queue a few seconds later, as the worker stops it
    // cleanly (contract, slice 24); a file no longer working answers 409 not_stoppable.
    stopJob: function (p) {
      var i = find(working, "jobId", p.id);
      if (i < 0) return fail(409, "not_stoppable", "This file has already finished, so there is nothing to stop. Nothing was changed.");
      setTimeout(function () {
        var j = removeFrom(working, "jobId", p.id); if (!j) return;
        skippedList.unshift({ id: "s" + p.id, library: j.library, path: j.path, reason: "user_stopped", detail: "You stopped it.", libraryId: "1" }); saveAct(); // newest first, as a restored file
      }, 4000);
      return ok({ status: "stopping" });
    },
    retryJob: function (p) { var r = removeFrom(failedList, "jobId", p.id); saveAct(); return r ? ok() : gone(); },
    skipped: function (p) {
      var want = p.reason ? String(p.reason).split(",") : null; // reason=a,b: any of them (contract 22:30Z)
      var r = skippedList.filter(function (x) { return !want || want.indexOf(x.reason) >= 0; }), off = +p.offset || 0, lim = +p.limit || 50;
      return ok({ items: r.slice(off, off + lim), total: r.length });
    },
    convertAgain: function (p) {
      if (p.id === "s17") return fail(409, "renamed", "Night Train (2009).mp4 no longer exists: it was converted and is now Night Train (2009).mkv. Nothing was changed.");
      var r = removeFrom(skippedList, "id", p.id); saveAct(); return r ? ok() : gone();
    },
    failed: function () { return ok(list(failedList)); },
    done: function () { return ok(list(doneList)); },
    kept: function () {
      var b = 0; keptList.forEach(function (k) { b += k.originalBytes; });
      return ok({ keptBytes: b, count: keptList.length, days: settings.keepOriginalsDays, items: keptList });
    },
    restoreKept: function (p) {
      var k = removeFrom(keptList, "keptId", p.id); if (!k) return gone();
      // The original goes back and the smaller file is deleted: the Done row stays and says so (contract, slice 18).
      doneList.forEach(function (d) { if (d.keptId === p.id) { d.originalState = "restored"; d.keptId = null; d.keptDaysLeft = 0; } });
      skippedList.unshift({ id: "r" + p.id, library: k.library, path: k.path, reason: "restored", detail: "Left alone so it is not converted again by surprise.", libraryId: "1" });
      saveAct(); return ok();
    },
    listFlows: function () { return ok(list(flows.map(flowView))); },
    createFlow: function (p, b) {
      var std = b.kind === "standard", f = { id: fid(), name: flowName(b.name || (std ? "Standard flow" : "Basic flow")), usedBy: [], madeWith: std ? "selectors" : "slider", changedAt: iso(0), rec: {} };
      applySetting(f.rec, std ? { speed: "normal", format: "hevc", quality: 3, resolution: "keep", maxBitrate: "none", audio: "stereo_aac", onlyMyLanguages: true, subtitles: "keep_all", fileType: "mkv", afterConverting: "replace", ignore: [], includeDolbyVision: false } : { quality: 3, audio: "stereo_aac" }); // Standard: S6 defaults
      flows.push(f); saveLibs(); return ok(flowView(f));
    },
    flowWarnings: function (p, b) { return ok({ warnings: warningsFor(b) }); }, // no side effects
    checkOutput: function (p, b) { // "Save to another folder": checked like a library folder (S6)
      var path = (b.path || "").replace(/\/+$/, ""), mine = libs.filter(function (l) { return l.flowId === p.id; }), hit = mine.filter(function (l) { return path === l.path || path.indexOf(l.path + "/") === 0; })[0];
      if (hit) return ok({ blockers: [{ code: "inside_library", message: "This folder is inside the library, so new files would be converted again. Pick a folder outside " + hit.path + "." }], notes: [] });
      if (/readonly/.test(path)) return ok({ blockers: [{ code: "not_writable", message: "We can't save files in this folder. Pick a folder shrink-your-shit can write to." }], notes: [] });
      return ok({ blockers: [], notes: [] });
    },
    getFlowSetting: function (p) { var i = find(flows, "id", p.id); if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      var f = flows[i]; return ok(f.madeWith === "builder" || f.madeWith === "legacy" ? Object.assign({ kind: "custom", warnings: [] }, avail()) : settingOf(recOf(f))); },
    putFlowSetting: function (p, b) {
      var i = find(flows, "id", p.id); if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      var f = flows[i]; if (f.madeWith === "builder" || f.madeWith === "legacy") return fail(409, "flow_read_only", "This flow can't be changed here yet. Nothing was changed.");
      if (!(settings.languages || []).length && (b.speed ? b.audio === "my_languages" || b.subtitles === "my_languages" : b.audio && b.audio !== "keep_all")) return fail(400, "languages_needed", "Choose your languages first, in Settings > General.");
      if (b.speed && ((b.audio === "stereo_opus" && b.fileType === "mp4") || (b.fileType === "webm" && !((b.format === "av1" || b.format === "vp9") && b.audio === "stereo_opus")))) return fail(400, "invalid", "That file type can't hold those choices. Nothing was changed.");
      if (b.speed && b.afterConverting === "folder" && !b.outputDir) return fail(400, "invalid", "Choose the folder for new files. Nothing was changed.");
      var r = recOf(f); applySetting(r, b); f.madeWith = r.sel ? "selectors" : "slider"; f.changedAt = iso(0);
      libs.forEach(function (l) { if (l.flowId === f.id && l !== r) ["quality", "audio", "settingKind", "settingLabel"].forEach(function (k) { l[k] = r[k]; }), r.sel ? (l.sel = copy(r.sel)) : delete l.sel; });
      saveLibs(); return ok(settingOf(r));
    },
    renameFlow: function (p, b) { var i = find(flows, "id", p.id); if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed."); flows[i].name = b.name || flows[i].name; saveLibs(); return ok(flowView(flows[i])); },
    duplicateFlow: function (p) { var i = find(flows, "id", p.id); if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed."); var f = copy(flows[i]); f.rec = copy(recOf(flows[i])); f.id = fid(); f.name = flowName(flows[i].name); f.usedBy = []; flows.push(f); saveLibs(); return ok(flowView(f)); },
    deleteFlow: function (p) {
      var i = find(flows, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      if (flows[i].usedBy.length) return fail(409, "flow_in_use", "In use by " + flows[i].usedBy.join(", ") + ". Nothing was deleted.", { usedBy: flows[i].usedBy });
      flows.splice(i, 1); saveLibs(); return ok();
    },
    importFlow: function (p, b) {
      if (!flowIO) return notAvailable();
      if (!b || !b.name) return fail(400, "bad_flow", "That file is not a flow. Nothing was imported.");
      var f = { id: fid(), name: b.name, usedBy: [], madeWith: "builder", changedAt: iso(0) }; flows.push(f); saveLibs(); return ok(f);
    },
    exportFlow: function (p) { if (!flowIO) return notAvailable(); var i = find(flows, "id", p.id); return i < 0 ? fail(404, "not_found", "That flow is gone.") : ok({ name: flows[i].name, steps: stepsFor(flows[i]) }); },
    getFlow: function (p) {
      var i = find(flows, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      var v = Object.assign(flowView(flows[i]), { steps: stepsFor(flows[i]) }); delete v.usedBy; delete v.changedAt; return ok(v); // the contract's fields only
    }
  };
  // ?auth=1: the mock requires a session, as the server does beyond loopback (?auth=0 turns it off; remembered
  // for the tab). Every call but login answers 401 until you sign in with the token Settings > General shows.
  // Only "signed in" and a counter are kept, never the token itself.
  function read(k) { try { return sessionStorage.getItem(k); } catch (e) { return null; } }
  function store(k, v) { try { if (v == null) sessionStorage.removeItem(k); else sessionStorage.setItem(k, v); } catch (e) {} }
  function token() { var n = read("syp-mock-token-n"); return n ? "sample-token-new-" + n : "sample-token-not-real"; }
  var am = /[?&]auth=([01])/.exec(location.search); if (am) store("syp-mock-auth", am[1] === "1" ? "1" : null);
  var auth = read("syp-mock-auth") === "1"; settings.authRequired = auth;
  Object.keys(m).forEach(function (name) {
    var f = m[name];
    if (name !== "login") m[name] = function (p, b) { return auth && !read("syp-mock-session") ? fail(401, "unauthorized", "Sign in to continue.") : f(p, b); };
  });
  g.SYP = g.SYP || {};
  g.SYP.mock = m;
})(window);
