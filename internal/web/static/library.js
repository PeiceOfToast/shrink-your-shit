/* Library page: the Basic slider with Save and Scan now, and the Preview card. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, ed = S.editors;
  function $(id) { return document.getElementById(id); }
  var id = new URLSearchParams(location.search).get("id"), lib, slider, prev, custom = false, ask = {};
  var DESC = ["Shrinks the most. Some files may lose a little detail. Best for space-starved drives.", "Smaller files, small quality trade-off.",
    "Shrinks most files with little visible change. A good start for most people.", "Keeps more detail. Only touches files with lots of room to shrink.",
    "Keeps nearly all detail. Only touches files with lots of room to shrink, so savings are small."];
  var table = []; // the engine's slider table (crf, minSavingPct per position), read from the server

  function say(t) { $("said").textContent = t; }
  function fail(e) { var b = $("err"); b.firstChild.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function facts() {
    var v = slider.get().quality - 1, li = $("facts").children, row = table[v];
    $("desc").textContent = DESC[v];
    li[0].textContent = "Video: converted to a smaller format";
    li[2].textContent = "Skipped: HDR and Dolby Vision files, files that are already small" + (row ? ", files that would shrink by less than " + row.minSavingPct + "%" : "");
  }
  function head() {
    $("lib-title").textContent = lib.name; $("lib-path").textContent = lib.path; document.title = lib.name + " - shrink-your-shit";
    $("lib-state").textContent = ""; $("lib-state").appendChild(ui.stateBadge(lib));
    $("scan").textContent = lib.state === "not_started" ? "Start" : "Scan now";
  }

  // 409 flow_shared: ask once whether to change it for everyone or make a copy.
  function askShared(used) {
    var n = used.length, box = $("share");
    $("share-q").textContent = "This setting is also used by " + used.join(", ");
    $("sh1l").textContent = n === 1 ? "Change it for both libraries" : "Change it for all " + (n + 1) + " libraries";
    $("sh2l").textContent = "Make a separate copy for " + lib.name + " only";
    box.hidden = false; $("sh1").focus();
  }
  function save() {
    var body = slider.get(), choice = !$("share").hidden && document.querySelector('input[name="sh"]:checked').value;
    if (choice) body.sharedFlowChoice = choice;
    var first = custom ? api.replaceCustom({ id: id }).then(function () { custom = false; $("custom").hidden = true; }) : Promise.resolve();
    first.then(function () { return api.putSetting({ id: id }, body); }).then(function () {
      $("share").hidden = true; say("Saved."); $("err").hidden = true;
    }, function (e) { if (e.code === "flow_shared") askShared(e.usedBy || []); else fail(e); });
  }

  function init() {
    if (!id) { fail({ message: "No library was chosen. Open one from the Libraries page." }); return; }
    Promise.all([api.getSettings(), api.getLibrary({ id: id }), api.getSetting({ id: id })]).then(function (r) {
      var level = r[0].controlLevel, setting = r[2]; lib = r[1];
      head(); $("lib-body").hidden = false;
      var get = function () { return slider ? slider.get() : setting; }; // the loaded setting until a level has its own editor
      prev = ui.preview($("pv"), id, get);
      if (level !== "basic") {
        $("later").hidden = false; $("save").hidden = true; $("desc").hidden = true; $("facts").hidden = true;
      } else {
        custom = setting.quality == null;
        $("custom").hidden = !custom;
        slider = ed.slider($("slider"), setting.quality || 3, function () { facts(); prev.mark(); }, lib.name + ": smallest file to best quality");
        if (custom) $("save").textContent = "Use this slider setting";
        $("save").onclick = save;
        // The slider's table comes from the server (echoed in the setting, or on its own for a custom setting).
        Promise.resolve(setting.positions ? setting : api.getSliderTable()).then(function (t) { table = t.positions; facts(); }, facts);
      }
      $("scan").onclick = function () {
        api.scanLibrary({ id: id }).then(function () { say("Scan started. Progress shows in Activity."); return api.getLibrary({ id: id }); })
          .then(function (l) { lib = l; head(); }, fail);
      };
      $("update").onclick = function () { prev.start(); };
      // Remove: the one place; a confirm first (native dialog).
      $("remove").onclick = function () { $("rm-q").textContent = "Remove " + lib.name + "?"; $("rm-dialog").returnValue = ""; $("rm-dialog").showModal(); };
      $("rm-dialog").addEventListener("close", function () {
        if ($("rm-dialog").returnValue !== "remove") { $("remove").focus(); return; }
        api.deleteLibrary({ id: id }).then(function () { location.href = "/libraries/"; }, fail);
      });
      prev.start();
      if (location.hash === "#preview") $("preview").scrollIntoView();
    }).catch(fail);
  }
  S.ready.then(init);
})();
