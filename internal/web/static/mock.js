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
    parallelJobs: 1, quietHours: { from: "", to: "" }, lowPriority: true, scanSchedule: "02:00",
    integrations: {}, updateAvailable: null
  };
  try { var saved = sessionStorage.getItem("syp-mock-settings"); if (saved) Object.assign(settings, JSON.parse(saved)); } catch (e) {}
  function persist() { try { sessionStorage.setItem("syp-mock-settings", JSON.stringify(settings)); } catch (e) {} }

  var libs = [
    { id: "1", name: "Movies", path: "/media/movies", state: "working", workingFiles: 2, waitingFiles: 590, nextScan: iso(-6 * 3600e3), freedBytes: 410 * GB, flowId: "f1", settingLabel: "Balanced", settingKind: "slider" },
    { id: "2", name: "TV Shows", path: "/media/tv", state: "idle", workingFiles: 0, waitingFiles: 0, nextScan: iso(-6 * 3600e3), freedBytes: 230 * GB, flowId: "f2", settingLabel: "Smaller", settingKind: "slider" },
    { id: "3", name: "Home videos", path: "/media/home", state: "needs_attention", workingFiles: 0, waitingFiles: 12, nextScan: iso(-6 * 3600e3), freedBytes: 0, flowId: "f3", settingLabel: "Custom", settingKind: "custom" }
  ];
  try { var sl = sessionStorage.getItem("syp-mock-libs"); if (sl) libs = JSON.parse(sl); } catch (e) {}
  // ?fresh=1 on the wizard starts over: no libraries, first-run not done.
  if (/[?&]fresh=1/.test(location.search)) { libs = []; settings.firstRunDone = false; settings.firstRunStep = 1; persist(); saveLibs(); }
  function saveLibs() { try { sessionStorage.setItem("syp-mock-libs", JSON.stringify(libs)); } catch (e) {} }
  var flows = [
    { id: "f1", name: "Movies - Balanced", usedBy: ["Movies"], madeWith: "slider", changedAt: iso(9 * 864e5) },
    { id: "f2", name: "TV Shows - Smaller", usedBy: ["TV Shows"], madeWith: "slider", changedAt: iso(20 * 864e5) },
    { id: "f3", name: "Home videos - hand made", usedBy: ["Home videos"], madeWith: "builder", changedAt: iso(3 * 864e5) },
    { id: "f4", name: "Old setting", usedBy: [], madeWith: "slider", changedAt: iso(60 * 864e5) }
  ];
  var paused = false, pv = false;
  var working = [{ jobId: "j1", library: "Movies", path: "/media/movies/Alien (1979).mkv", phase: "converting", percent: 42, etaSeconds: 1500, fromBytes: 9 * GB, expectedBytes: 4 * GB },
                 { jobId: "j2", library: "Movies", path: "/media/movies/Heat (1995).mkv", phase: "checking", percent: 96, etaSeconds: 60, fromBytes: 7 * GB, expectedBytes: 3 * GB }];
  var skippedList = [
    { id: "s1", library: "TV Shows", path: "/media/tv/Show S01E01.mkv", reason: "already_efficient", detail: "Already small for its length.", libraryId: "2" },
    { id: "s2", library: "Movies", path: "/media/movies/Dune.mkv", reason: "hdr_dv", detail: "HDR picture would lose its colours.", libraryId: "1" },
    { id: "s3", library: "Movies", path: "/media/movies/Up.mkv", reason: "user_stopped", detail: "You stopped it.", libraryId: "1" },
    { id: "s4", library: "Movies", path: "/media/movies/Tenet.mkv", reason: "some_future_code", detail: "A reason this version does not know by name.", libraryId: null }
  ];
  var failedList = [{ jobId: "j9", library: "Movies", path: "/media/movies/Broken.mkv", title: "The new file did not pass the check", detail: "Your original was not touched. Try again, or leave it.", originalUntouched: true }];
  var doneList = [{ jobId: "j5", library: "TV Shows", path: "/media/tv/Show S01E02.mkv", beforeBytes: 3 * GB, afterBytes: 1.2 * GB, originalState: "kept", keptDaysLeft: 5, keptId: "k1" }];
  var keptList = [{ keptId: "k1", library: "TV Shows", path: "/media/tv/Show S01E02.mkv", originalBytes: 3 * GB, nowBytes: 1.2 * GB, deleteOn: iso(-5 * 864e5) }];

  function summary() {
    var kept = 0; keptList.forEach(function (k) { kept += k.originalBytes - k.nowBytes; });
    return { freedBytes: 640 * GB, waitingBytes: kept, workingFiles: paused ? 0 : working.length, waitingFiles: 602, paused: paused,
             counts: { skipped: skippedList.length, failed: failedList.length } };
  }
  function previewResult() {
    return { stale: false, status: "ready", estimateBytesLow: 300 * GB, estimateBytesHigh: 420 * GB, files: 1284, totalBytes: 1100 * GB, canShrink: 912,
             skipped: { already_efficient: 281, hdr_dv: 62, shared_file: 29 } };
  }
  function gone() { return fail(404, "not_found", "That item is gone. Nothing was changed. Reload the page."); }
  function removeFrom(arr, key, id) { var i = find(arr, key, id); return i < 0 ? null : arr.splice(i, 1)[0]; }

  var m = {
    login: function (p, b) { return b.token ? ok() : fail(401, "bad_token", "That token was not accepted. Nothing was changed. Check the address you were given."); },
    logout: function () { return ok(); },
    getSettings: function () { return ok(settings); },
    patchSettings: function (p, b) {
      var k, level = b.controlLevel;
      if (level && ["basic", "standard", "advanced"].indexOf(level) < 0) return fail(400, "bad_value", "Control level must be Basic, Standard or Advanced. Nothing was changed.");
      for (k in b) if (k in settings && k !== "authRequired" && k !== "trashPath") settings[k] = b[k];
      persist(); return ok(settings);
    },
    getToken: function () { return ok({ token: "sample-token-not-real" }); },
    rotateToken: function () { return ok({ token: "sample-token-new" }); },
    getHealth: function () { return ok({ version: "sample", logsTail: [], checks: [{ id: "converter", label: "Video converter found", status: "ok", detail: "" }] }); },
    backup: function () { return ok(); },
    listLibraries: function () { return ok(list(libs)); },
    addLibrary: function (p, b) {
      var l = { id: String(libs.length + 10), name: b.name || "New library", path: b.path || "", state: "idle", workingFiles: 0, waitingFiles: 0, nextScan: iso(-6 * 3600e3), freedBytes: 0, flowId: "fn", settingLabel: "Balanced", settingKind: "slider" };
      libs.push(l); saveLibs(); return ok(l);
    },
    // Try these folder names to see each state: "missing", "readonly", "full", "empty", "nocodec".
    checkLibrary: function (p, b) {
      var path = b.path || "", codes = { missing: "unreadable", readonly: "read_only", full: "low_space", empty: "no_videos", nocodec: "no_converter" }, bl = [], k;
      for (k in codes) if (path.indexOf(k) >= 0) bl.push({ code: codes[k], message: k === "full" ? "Only 6 GB free on this disk. Free up space (about 15 GB is enough to start), then press Check again." : "Cannot use this folder." });
      return ok({ ok: !bl.length, blockers: bl, notes: [{ code: "hardlinks", message: "212 files are shared with another folder (for example by Radarr). We will leave them alone, because changing one would break the link." }, { code: "no_hw_encoder", message: "" }],
                  videoCount: 1284, totalBytes: 1100 * GB });
    },
    browseDirs: function (p) { return ok({ path: p.path || "/media", parent: "/", dirs: ["movies", "tv", "home"] }); },
    getLibrary: function (p) { var i = find(libs, "id", p.id); return i < 0 ? fail(404, "not_found", "That library is gone. Nothing was changed.") : ok(libs[i]); },
    patchLibrary: function (p, b) { var i = find(libs, "id", p.id); if (i < 0) return fail(404, "not_found", "That library is gone. Nothing was changed."); Object.assign(libs[i], { name: b.name || libs[i].name, path: b.path || libs[i].path }); saveLibs(); return ok(libs[i]); },
    deleteLibrary: function (p) { var r = removeFrom(libs, "id", p.id); saveLibs(); return r ? ok() : gone(); },
    scanLibrary: function () { return ok(); },
    getSetting: function (p) {
      var i = find(libs, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That library is gone. Nothing was changed.");
      if (libs[i].settingKind === "custom") return ok({ kind: "custom" });
      return ok({ quality: libs[i].settingLabel === "Smaller" ? 2 : 3 });
    },
    putSetting: function (p, b) {
      var i = find(libs, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That library is gone. Nothing was changed.");
      var f = flows[find(flows, "id", libs[i].flowId)];
      if (f && f.usedBy.length > 1 && !b.sharedFlowChoice) return fail(409, "flow_shared", "Another library uses this setting. Nothing was changed.", { usedBy: f.usedBy });
      return ok();
    },
    replaceCustom: function () { return ok(); },
    putLibraryFlow: function (p, b) { var i = find(libs, "id", p.id); if (i >= 0) libs[i].flowId = b.flowId; saveLibs(); return ok(); },
    // Answers "computing" first, then "ready", so the waiting state gets exercised.
    preview: function () { pv = !pv; return ok(pv ? { status: "computing", stale: false } : previewResult()); },
    previewSample: function () { return ok({ lines: ["Video: HEVC, about half the size"], expectedLow: 2 * GB, expectedHigh: 3 * GB }); },
    listFiles: function () { return ok(list([{ path: "/media/movies/Alien (1979).mkv", bytes: 9 * GB }])); },
    activitySummary: function () { return ok(summary()); },
    pause: function () { paused = true; return ok(); },
    resume: function () { paused = false; return ok(); },
    queue: function () { return ok({ working: paused ? [] : working, upNext: [{ library: "Movies", count: 590, order: "Smallest first" }] }); },
    stopJob: function (p) {
      var j = removeFrom(working, "jobId", p.id); if (!j) return gone();
      skippedList.push({ id: "s" + p.id, library: j.library, path: j.path, reason: "user_stopped", detail: "You stopped it.", libraryId: "1" }); return ok();
    },
    retryJob: function (p) { return removeFrom(failedList, "jobId", p.id) ? ok() : gone(); },
    skipped: function (p) {
      var r = skippedList.filter(function (s) { return !p.reason || s.reason === p.reason; });
      return ok(list(r));
    },
    convertAgain: function (p) { return removeFrom(skippedList, "id", p.id) ? ok() : gone(); },
    failed: function () { return ok(list(failedList)); },
    done: function () { return ok(list(doneList)); },
    kept: function () {
      var b = 0; keptList.forEach(function (k) { b += k.originalBytes; });
      return ok({ keptBytes: b, count: keptList.length, days: settings.keepOriginalsDays, items: keptList });
    },
    restoreKept: function (p) { return removeFrom(keptList, "keptId", p.id) ? ok() : gone(); },
    listFlows: function () { return ok(list(flows)); },
    createFlow: function (p, b) { var f = { id: "f" + (flows.length + 10), name: b.name || "New flow", usedBy: [], madeWith: "builder", changedAt: iso(0) }; flows.push(f); return ok(f); },
    renameFlow: function (p, b) { var i = find(flows, "id", p.id); if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed."); flows[i].name = b.name || flows[i].name; return ok(flows[i]); },
    duplicateFlow: function (p) { var i = find(flows, "id", p.id); if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed."); var f = copy(flows[i]); f.id = "f" + (flows.length + 10); f.name += " (copy)"; f.usedBy = []; flows.push(f); return ok(f); },
    deleteFlow: function (p) {
      var i = find(flows, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      if (flows[i].usedBy.length) return fail(409, "flow_in_use", "In use by " + flows[i].usedBy.join(", ") + ". Nothing was deleted.", { usedBy: flows[i].usedBy });
      flows.splice(i, 1); return ok();
    },
    importFlow: function () { return ok(); },
    exportFlow: function (p) { var i = find(flows, "id", p.id); return i < 0 ? fail(404, "not_found", "That flow is gone.") : ok(flows[i]); },
    getFlow: function (p) {
      var i = find(flows, "id", p.id);
      if (i < 0) return fail(404, "not_found", "That flow is gone. Nothing was changed.");
      return ok({ id: flows[i].id, name: flows[i].name, steps: [{ n: 1, kind: "check", label: "Check the file can be read", branches: [] }, { n: 2, kind: "convert", label: "Make it smaller", branches: [] }] });
    }
  };
  g.SYP = g.SYP || {};
  g.SYP.mock = m;
})(window);
