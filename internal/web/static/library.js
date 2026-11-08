/* Library page: the Basic editor or the Standard choices, with Save and Scan now; the Preview card (not at Basic). */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, ed = S.editors;
  function $(id) { return document.getElementById(id); }
  var id = new URLSearchParams(location.search).get("id"), lib, editor, prev, custom = false;
  var warn = null; // the warnings notice above Save

  function say(t) { $("said").textContent = t; }
  // An earlier "Saved." must not stand beside the error.
  function fail(e) { say(""); var b = $("err"); b.firstChild.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function head() {
    $("lib-title").textContent = lib.name; $("lib-path").textContent = lib.path; document.title = lib.name + " - shrink-your-shit";
    $("lib-state").textContent = ""; $("lib-state").appendChild(ui.stateBadge(lib));
    $("scan").textContent = lib.state === "not_started" ? "Start" : "Scan now";
  }

  // 409 flow_shared: ask once whether to change it for everyone or make a copy.
  function askShared(used) {
    used = used.filter(function (n) { return n !== lib.name; }); // the server lists every library on the flow, this one too
    var n = used.length, box = $("share");
    $("share-q").textContent = "This setting is also used by " + used.join(", ");
    $("sh1l").textContent = n === 1 ? "Change it for both libraries" : "Change it for all " + (n + 1) + " libraries";
    $("sh2l").textContent = "Make a separate copy for " + lib.name + " only";
    box.hidden = false; $("applies").hidden = true; $("sh1").focus();
  }
  function save() {
    if (!editor) return; // the Basic editor draws once the language names are in
    var body = editor.get(), choice = !$("share").hidden && document.querySelector('input[name="sh"]:checked').value;
    if (choice) body.sharedFlowChoice = choice;
    var first = custom ? api.replaceCustom({ id: id }).then(function () { custom = false; $("custom").hidden = true; }) : Promise.resolve();
    first.then(function () { return api.putSetting({ id: id }, body); }).then(function (r) {
      // After saving, the saved setting's warnings: from the answer if it carries them, else asked for.
      if (r && r.warnings) warn.show(r.warnings); else api.getSetting({ id: id }).then(function (s) { warn.show(s.warnings); }, function () {});
      $("share").hidden = true; say("Saved."); $("err").hidden = true;
    }, function (e) { if (e.code === "flow_shared") askShared(e.usedBy || []); else fail(e); });
  }

  // One-file breakdown beside the whole-library estimate.
  function showSample(sm) {
    var ul = $("sample-lines"); ul.textContent = "";
    if (!sm) { $("sample-size").textContent = ""; return; }
    sm.lines.forEach(function (l) { ul.appendChild(ui.el("li", null, l)); });
    $("sample-size").textContent = "Expected size: about " + ui.range(sm.expectedBytesLow, sm.expectedBytesHigh);
  }

  function init() {
    if (!id) { fail({ message: "No library was chosen. Open one from the Libraries page." }); return; }
    Promise.all([api.getSettings(), api.getLibrary({ id: id }), api.getSetting({ id: id })]).then(function (r) {
      var level = r[0].controlLevel, setting = r[2]; lib = r[1];
      head(); $("lib-body").hidden = false;
      if (r[0].perLibraryFlow) { // the Flow choice comes first; its code loads only when this is on
        var sc = document.createElement("script"); sc.src = "/assets/flowpick.js";
        sc.onload = function () { S.flowpick($("flowpick"), lib, say); }; document.head.appendChild(sc);
      }
      var get = function () { return editor ? editor.get() : setting; }; // the loaded setting until a level has its own editor
      warn = ui.warnings($("save")); warn.show(setting.warnings); // the saved setting's warnings, at every level
      // At Standard the editor's Preview gives the warnings of the setting as edited, before it is saved.
      var extra = level === "standard" ? { sample: function () { return $("sf").value || null; }, onSample: showSample, onWarnings: function (w) { warn.show(w); } } : null;
      if (level === "standard") prev = ui.preview($("pv"), id, get, extra); // only for the warnings now (see below)
      if (level === "standard") {
        custom = setting.kind === "custom";
        $("custom").hidden = !custom;
        $("custom").querySelector("strong").textContent = "This setting is too custom to show as choices";
        $("qh").textContent = "Choices for " + lib.name;
        // A custom setting shows the defaults, but still with the choices this version can save (addendum 13).
        editor = ed.selectors($("selectors"), function () { prev.mark(); }, custom ? { availableAudio: setting.availableAudio, availableSubtitles: setting.availableSubtitles, availableFileTypes: setting.availableFileTypes } : setting);
        if (custom) $("save").textContent = "Use these choices instead";
        $("save").onclick = save; $("applies").textContent = "Applies to " + lib.name + " only."; $("applies").hidden = false;
        $("sample").hidden = false; $("sf").onchange = function () { prev.start(); };
        api.listFiles({ id: id }).then(function (r) {
          r.items.forEach(function (f) { var o = ui.el("option", null, f.path.split(/[\\/]/).pop() + " (" + ui.bytes(f.bytes) + ")"); o.value = f.path; $("sf").appendChild(o); });
          prev.start();
        }, function (e) { fail(e); prev.start(); });
      } else if (level !== "basic") {
        $("later").hidden = false; $("save").hidden = true;
      } else {
        custom = setting.kind === "custom";
        var more = setting.kind === "selectors"; // saved with Standard choices: more than a slider can show
        $("custom").hidden = !(custom || more);
        if (more) $("custom").querySelector("strong").textContent = "This setting has more choices than a slider shows";
        // The Basic editor (redesign A1) replaces the old description and facts list; language names come from the
        // same list Settings uses. A custom or Standard setting opens at Balanced with the default audio.
        if (custom || more) $("save").textContent = "Use this slider setting";
        $("save").onclick = save;
        var langs = r[0].languages || [];
        fetch("/assets/languages.json").then(function (x) { return x.json(); }).then(null, function () { return []; }).then(function (l) {
          var names = {}; l.forEach(function (x) { names[x[0]] = x[1]; }); // the names Settings shows (design: "English and Deutsch")
          editor = ed.basic($("slider"), custom || more ? { becomes: setting.becomes } : setting, langs, names, null, lib.name + ": smallest file to best quality");
        });
      }
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

      // "Is this the right folder?" replaces the Preview card (redesign A3). At Standard the Preview still runs, unseen,
      // only for the warnings above Save while editing, until workshop's warnings-only check exists (REDESIGN E).
      $("preview").hidden = true;
      ui.firstFive($("first5-card"), lib.path, function () { return api.firstFiles({ id: id }); }, "h2", "/activity/");
    }).catch(fail);
  }
  S.ready.then(init);
})();
