/* The one place the UI talks to the server. Every call in UI-API-CONTRACT.md is
   listed once here as name: [method, path]. Call it as SYP.api.name(params, body).
   The real server is the default. The mock (mock.js, sample data) is a development tool: ?mock=1 turns
   it on for the tab, ?mock=0 turns it off again, and the page shows "Showing sample data" while it is on.
   A remembered choice only exists if someone opened ?mock=1 in that tab, so it is honoured as is. */
(function (g) {
  "use strict";
  var DEFAULT_MOCK = false;
  var E = {
    login: ["POST", "/api/login"], logout: ["POST", "/api/logout"],
    getSettings: ["GET", "/api/settings"], patchSettings: ["PATCH", "/api/settings"],
    getToken: ["GET", "/api/settings/token"], rotateToken: ["POST", "/api/settings/token/rotate"],
    getHealth: ["GET", "/api/system/health"], backup: ["POST", "/api/system/backup"],
    listLibraries: ["GET", "/api/libraries"], addLibrary: ["POST", "/api/libraries"],
    checkLibrary: ["POST", "/api/libraries/check"], browseDirs: ["GET", "/api/fs/dirs"],
    getLibrary: ["GET", "/api/libraries/{id}"], patchLibrary: ["PATCH", "/api/libraries/{id}"],
    deleteLibrary: ["DELETE", "/api/libraries/{id}"], scanLibrary: ["POST", "/api/libraries/{id}/scan"],
    getSliderTable: ["GET", "/api/settings/slider"],
    getSetting: ["GET", "/api/libraries/{id}/setting"], putSetting: ["PUT", "/api/libraries/{id}/setting"],
    replaceCustom: ["POST", "/api/libraries/{id}/setting/replace-custom"],
    putLibraryFlow: ["PUT", "/api/libraries/{id}/flow"],
    preview: ["POST", "/api/libraries/{id}/preview"],
    listFiles: ["GET", "/api/libraries/{id}/files"],
    // "Is this the right folder?" (redesign A3; workshop slice A, UI-API-CONTRACT.md 20:25Z): a library, or a folder.
    firstFiles: ["GET", "/api/libraries/{id}/first-files"], firstFilesFor: ["POST", "/api/libraries/first-files"],
    // Activity's Files view (redesign C; workshop S5, UI-API-CONTRACT.md 21:00Z): one list per card, and each file's log.
    activityFiles: ["GET", "/api/activity/files"], fileLog: ["GET", "/api/activity/files/{id}/log"],
    // Workers (redesign D; workshop S2, UI-API-CONTRACT.md 20:25Z): list, start, stop {when}, Files at once, the update notice.
    listWorkers: ["GET", "/api/workers"], startWorker: ["POST", "/api/workers/{id}/start"], stopWorker: ["POST", "/api/workers/{id}/stop"],
    setWorker: ["PATCH", "/api/workers/{id}"], dismissWorkerNotice: ["POST", "/api/workers/notice/dismiss"],
    activitySummary: ["GET", "/api/activity/summary"], pause: ["POST", "/api/queue/pause"],
    resume: ["POST", "/api/queue/resume"], queue: ["GET", "/api/activity/queue"],
    stopJob: ["POST", "/api/jobs/{id}/stop"], retryJob: ["POST", "/api/jobs/{id}/retry"],
    skipped: ["GET", "/api/activity/skipped"], convertAgain: ["POST", "/api/skipped/{id}/convert-again"],
    failed: ["GET", "/api/activity/failed"], done: ["GET", "/api/activity/done"],
    kept: ["GET", "/api/activity/kept"], restoreKept: ["POST", "/api/kept/{id}/restore"],
    listFlows: ["GET", "/api/flows"], createFlow: ["POST", "/api/flows"], renameFlow: ["PATCH", "/api/flows/{id}"],
    duplicateFlow: ["POST", "/api/flows/{id}/duplicate"], deleteFlow: ["DELETE", "/api/flows/{id}"],
    importFlow: ["POST", "/api/flows/import"], exportFlow: ["GET", "/api/flows/{id}/export"],
    getFlow: ["GET", "/api/flows/{id}"],
    // A flow's own setting (piece B, UI-API-CONTRACT.md 20:40Z): the same bodies as a library's setting.
    getFlowSetting: ["GET", "/api/flows/{id}/setting"], putFlowSetting: ["PUT", "/api/flows/{id}/setting"],
    // Standard (piece E; workshop S6, UI-API-CONTRACT.md 21:10Z): warnings while editing, and the "another folder" check.
    flowWarnings: ["POST", "/api/flows/{id}/warnings"], checkOutput: ["POST", "/api/flows/{id}/check-output"]
  };

  function pick() {
    var m = /[?&]mock=([01])/.exec(location.search), on = DEFAULT_MOCK;
    try {
      if (m) sessionStorage.setItem("syp-mock", m[1]);
      var s = sessionStorage.getItem("syp-mock");
      if (s) on = s === "1";
    } catch (e) { if (m) on = m[1] === "1"; }
    return on;
  }
  var mocked = pick();

  // Errors are always {code, message, status, ...}: the contract's error object.
  function real(name, p, body) {
    var e = E[name], path = e[1], q = [], k;
    p = p || {};
    path = path.replace(/\{(\w+)\}/g, function (_, key) { return encodeURIComponent(p[key]); });
    for (k in p) if (e[1].indexOf("{" + k + "}") < 0 && p[k] != null) q.push(encodeURIComponent(k) + "=" + encodeURIComponent(p[k]));
    if (q.length) path += "?" + q.join("&");
    var opt = { method: e[0], credentials: "same-origin", headers: {} };
    if (e[0] !== "GET") { opt.headers["Content-Type"] = "application/json"; opt.body = JSON.stringify(body || {}); }
    return fetch(path, opt).then(function (r) {
      var ct = r.headers.get("Content-Type") || "";
      if (ct.indexOf("json") < 0) return r.ok ? r : Promise.reject({ code: "http_" + r.status, message: "The server answered " + r.status + ". Nothing was changed.", status: r.status });
      return r.json().then(function (j) {
        if (r.ok) return j;
        var err = j && j.error || { code: "http_" + r.status, message: "The server answered " + r.status + "." };
        err.status = r.status;
        return Promise.reject(err);
      });
    }, function () {
      return Promise.reject({ code: "offline", message: "Can't reach the server. Nothing was changed. Check that it is running, then try again." });
    });
  }

  // 401: no session, or it ran out: app.js shows the sign-in screen (login's own 401 is a wrong token).
  // 403 csrf: the server refused a write it did not trust; say so in plain words.
  function guard(name, e) {
    if (e && e.status === 401 && name !== "login" && g.SYP.needSignIn) g.SYP.needSignIn();
    if (e && e.status === 403 && e.code === "csrf") e.message = "This change was refused because it did not come from this page. Nothing was changed. Reload the page and try again.";
    return Promise.reject(e);
  }
  var api = { mocked: mocked, names: Object.keys(E) };
  // Import and Export stay hidden until the server has them (workshop slice 30; contract addendum 11). The page
  // never asks the server to find out. Against the mock, ?flowio=1 shows them for development (mock.js keeps it).
  api.flowIO = function () { try { return mocked && sessionStorage.getItem("syp-mock-flowio") === "1"; } catch (e) { return false; } };
  Object.keys(E).forEach(function (name) {
    api[name] = function (p, body) {
      return (mocked ? g.SYP.mock[name](p || {}, body || {}) : real(name, p, body)).then(null, function (e) { return guard(name, e); });
    };
  });
  g.SYP = g.SYP || {};
  g.SYP.api = api;
  g.SYP.endpoints = E;
})(window);
