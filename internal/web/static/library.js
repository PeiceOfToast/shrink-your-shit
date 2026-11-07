/* Library page (redesign B5): which flow the library uses (pick one, Save; Edit this flow opens the flow's page, the one
   place settings are edited), "Is this the right folder?", Scan in the header and Remove at the bottom. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui;
  function $(id) { return document.getElementById(id); }
  var id = new URLSearchParams(location.search).get("id"), lib, flows = [];

  function say(t) { $("said").textContent = t; }
  // An earlier "Saved." must not stand beside the error.
  function fail(e) { say(""); var b = $("err"); b.firstChild.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function head() {
    $("lib-title").textContent = lib.name; $("lib-path").textContent = lib.path; document.title = lib.name + " - shrink-your-shit";
    $("lib-state").textContent = ""; $("lib-state").appendChild(ui.stateBadge(lib));
    // Files waiting and no worker running (slice A status; redesign A4/D4): say so, with the way to start one.
    var w = lib.status && lib.status.kind === "waiting_for_worker"; $("waiting").hidden = !w;
    if (w) $("waiting-n").textContent = ui.count(lib.status.toDo, "video", "videos") + (lib.status.toDo === 1 ? " is" : " are") + " waiting";
  }
  // The Flow choice (B5): every flow by name with its tag; Save applies it.
  function pick() {
    var sel = $("flow"); sel.textContent = "";
    flows.forEach(function (f) { var o = ui.el("option", null, f.name + (f.tag ? " \u2014 " + f.tag : "")); o.value = f.id; sel.appendChild(o); });
    sel.value = lib.flowId;
    var link = function () { $("flow-edit").href = "/flow/?id=" + encodeURIComponent(sel.value); };
    sel.onchange = function () { link(); say(""); }; link();
    $("save").onclick = function () {
      api.putLibraryFlow({ id: id }, { flowId: sel.value }).then(function () { lib.flowId = sel.value; $("err").hidden = true; say("Saved."); }, fail);
    };
  }

  function init() {
    if (!id) { fail({ message: "No library was chosen. Open one from the Libraries page." }); return; }
    Promise.all([api.getLibrary({ id: id }), api.listFlows()]).then(function (r) {
      lib = r[0]; flows = r[1].items; head(); pick(); $("lib-body").hidden = false;
      ui.firstFive($("first5-card"), lib.path, function () { return api.firstFiles({ id: id }); }, "h2", "/activity/?show=unprocessed&library=" + encodeURIComponent(id));
      // Scan reads the folder and changes nothing on disk (workshop S2: nothing converts until a worker runs).
      $("scan").onclick = function () {
        api.scanLibrary({ id: id }).then(function () { say("Scan started. Progress shows in Activity."); return api.getLibrary({ id: id }); })
          .then(function (l) { lib = l; head(); }, fail);
      };
      // Remove: the one place; a confirm first (native dialog).
      $("remove").onclick = function () { $("rm-q").textContent = "Remove " + lib.name + "?"; $("rm-dialog").returnValue = ""; $("rm-dialog").showModal(); };
      $("rm-dialog").addEventListener("close", function () {
        if ($("rm-dialog").returnValue !== "remove") { $("remove").focus(); return; }
        api.deleteLibrary({ id: id }).then(function () { location.href = "/libraries/"; }, fail);
      });
    }).catch(fail);
  }
  S.ready.then(init);
})();
