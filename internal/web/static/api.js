/* The one place the UI talks to the server. Every call in UI-API-CONTRACT.md is
   listed once here as name: [method, path]. Call it as SYP.api.name(params, body).
   Mock or real is one switch: ?mock=1 / ?mock=0 in the address (remembered for the
   tab), or DEFAULT_MOCK below. Flip DEFAULT_MOCK to false when the endpoints land. */
(function (g) {
  "use strict";
  var DEFAULT_MOCK = true;
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
    activitySummary: ["GET", "/api/activity/summary"], pause: ["POST", "/api/queue/pause"],
    resume: ["POST", "/api/queue/resume"], queue: ["GET", "/api/activity/queue"],
    stopJob: ["POST", "/api/jobs/{id}/stop"], retryJob: ["POST", "/api/jobs/{id}/retry"],
    skipped: ["GET", "/api/activity/skipped"], convertAgain: ["POST", "/api/skipped/{id}/convert-again"],
    failed: ["GET", "/api/activity/failed"], done: ["GET", "/api/activity/done"],
    kept: ["GET", "/api/activity/kept"], restoreKept: ["POST", "/api/kept/{id}/restore"],
    listFlows: ["GET", "/api/flows"], createFlow: ["POST", "/api/flows"], renameFlow: ["PATCH", "/api/flows/{id}"],
    duplicateFlow: ["POST", "/api/flows/{id}/duplicate"], deleteFlow: ["DELETE", "/api/flows/{id}"],
    importFlow: ["POST", "/api/flows/import"], exportFlow: ["GET", "/api/flows/{id}/export"],
    getFlow: ["GET", "/api/flows/{id}"]
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

  var api = { mocked: mocked, names: Object.keys(E) };
  Object.keys(E).forEach(function (name) {
    api[name] = function (p, body) { return mocked ? g.SYP.mock[name](p || {}, body || {}) : real(name, p, body); };
  });
  g.SYP = g.SYP || {};
  g.SYP.api = api;
  g.SYP.endpoints = E;
})(window);
