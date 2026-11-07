/* One flow (redesign B3): the one place its settings are edited. Basic flows get the A1 editor, Standard flows the
   selectors; Save stores the flow's own setting (PUT /api/flows/{id}/setting), which every library using it follows.
   Advanced and earlier-install flows stay read-only: the diagram and its numbered text version. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  function $(id) { return document.getElementById(id); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  var HEAD = { start: "Start", check: "Check", video: "Video", audio: "Audio", subtitles: "Subtitles", filetype: "File type", finish: "Finish", skip: "Skip", resolution: "Resolution" };

  // Steps run left to right; a step with branches is followed by a column of labelled rows.
  function drawSteps(steps, parent) {
    steps.forEach(function (s, i) {
      if (i || parent.children.length) { var ar = el("span", "garrow", "→"); ar.setAttribute("aria-hidden", "true"); parent.appendChild(ar); }
      var n = el("div", "gnode" + (s.kind === "check" ? " cond" : s.kind === "skip" ? " skip" : s.kind === "finish" ? " out" : ""));
      n.appendChild(el("b", null, HEAD[s.kind] || s.kind)); n.appendChild(document.createTextNode(s.label));
      if (s.detail) { n.appendChild(document.createElement("br")); n.appendChild(el("span", "small muted", s.detail)); }
      parent.appendChild(n);
      if (s.branches && s.branches.length) {
        var ar2 = el("span", "garrow", "→"); ar2.setAttribute("aria-hidden", "true"); parent.appendChild(ar2);
        var col = el("div", "branch");
        s.branches.forEach(function (b) {
          var row = el("div", "branch-row"); row.appendChild(el("span", "branch-label", b.label)); drawSteps(b.steps, row);
          col.appendChild(row);
        });
        parent.appendChild(col);
      }
    });
  }
  function text(steps, ol) {
    steps.forEach(function (s) {
      ol.appendChild(el("li", null, (HEAD[s.kind] ? HEAD[s.kind] + ": " : "") + s.label + (s.detail ? " - " + s.detail : "") + "."));
      (s.branches || []).forEach(function (b) { // every branch is said under its own label
        var li = el("li", null, (b.label === "Yes" ? "If yes" : b.label === "No" ? "If no" : b.label) + ":"), sub = el("ol"); text(b.steps, sub); li.appendChild(sub); ol.appendChild(li);
      });
    });
  }
  function and(list) { return list.length < 2 ? list.join("") : list.slice(0, -1).join(", ") + " and " + list[list.length - 1]; }
  function say(t) { $("said").textContent = t; }

  function init() {
    var id = new URLSearchParams(location.search).get("id"), f, editor = null, kind, warn, names = {}, langs = [];
    if (!id) { fail({ message: "No flow was chosen. Open one from the Flows page." }); return; }
    function head() {
      $("f-title").textContent = f.name; document.title = f.name + " - shrink-your-shit";
      $("f-tag").textContent = f.tag || ""; $("f-tag").hidden = !f.tag;
    }
    // Switching kind takes effect on Save (B3): a Basic flow opens as Standard with the same values; a Standard flow can
    // go back to Basic only when every choice fits the slider (fitsBasic from the server).
    function build(k, s) {
      kind = k; var host = $("f-editor"), sw = $("f-switch"); host.textContent = ""; sw.textContent = "";
      $("f-qh").hidden = k !== "basic";
      if (k === "basic") {
        // A Basic choice Standard can't hold (Stereo AAC, until piece E adds it to the server's available audio) would
        // change quietly on Save, so the switch is off (still focusable) with design's line under it (REDESIGN B3).
        var a = el("a", null, "Switch to Standard"), why = el("span", "hint", "Switching to Standard comes in a later update for flows with Stereo AAC."); a.href = "#"; why.id = "f-switch-why"; why.style.display = "block";
        var syncSwitch = function () { var off = !!editor && (s.availableAudio || []).indexOf(editor.get().audio) < 0; a.setAttribute("aria-disabled", off ? "true" : "false"); why.hidden = !off; if (off) a.setAttribute("aria-describedby", why.id); else a.removeAttribute("aria-describedby"); };
        editor = S.editors.basic(host, s, langs, names, function () { say(""); syncSwitch(); }, f.name + ": smallest file to best quality");
        sw.appendChild(a); sw.appendChild(why); syncSwitch();
        a.onclick = function (e) { e.preventDefault(); if (a.getAttribute("aria-disabled") === "true") return; var v = editor.get();
          build("standard", { quality: v.quality, speed: "normal", format: "hevc", resolution: "keep", maxBitrate: "none", audio: v.audio, subtitles: "keep_all", fileType: "mkv", afterConverting: "replace", skipHdr: true, fitsBasic: true, becomes: s.becomes, availableFileTypes: s.availableFileTypes, availableAudio: s.availableAudio, availableSubtitles: s.availableSubtitles });
          say("Standard choices. They take effect when you press Save."); };
      } else {
        editor = S.editors.selectors(host, function () { say(""); }, s);
        if (s.fitsBasic) { var b = el("a", null, "Switch to Basic"); b.href = "#"; sw.appendChild(b);
          b.onclick = function (e) { e.preventDefault(); var v = editor.get(); build("basic", { quality: v.quality, audio: v.audio, becomes: s.becomes || { video: "HEVC", resolution: "same", fileType: "MKV" } }); say("Basic slider. It takes effect when you press Save."); }; }
        else sw.textContent = "This flow uses choices the slider can't show.";
      }
    }
    function rename() {
      var box = $("f-renamebox"), form = el("form", "row"), lb = el("label", "sr", "New name for " + f.name), inp = el("input"), ok = el("button", "btn small primary", "Save"), no = el("button", "btn small", "Cancel");
      inp.id = "f-newname"; lb.htmlFor = inp.id; inp.value = f.name; inp.size = 26; no.type = "button";
      form.appendChild(lb); form.appendChild(inp); form.appendChild(ok); form.appendChild(no); box.textContent = ""; box.appendChild(form); inp.focus(); inp.select();
      var close = function () { box.textContent = ""; $("f-rename").focus(); };
      no.onclick = close; inp.onkeydown = function (e) { if (e.key === "Escape") { e.preventDefault(); close(); } };
      form.onsubmit = function (e) { e.preventDefault(); var n = inp.value.trim(); if (!n) return;
        api.renameFlow({ id: id }, { name: n }).then(function (nf) { f.name = nf.name; head(); close(); say("Renamed."); }, fail); };
    }
    Promise.all([api.getFlow({ id: id }), api.getFlowSetting({ id: id }), api.getSettings(), fetch("/assets/languages.json").then(function (x) { return x.json(); }).then(null, function () { return []; })]).then(function (r) {
      f = r[0]; var s = r[1]; langs = r[2].languages || []; r[3].forEach(function (x) { names[x[0]] = x[1]; });
      head(); $("f-rename").hidden = false; $("f-rename").onclick = function (e) { e.preventDefault(); rename(); };
      $("f-body").hidden = false;
      $("export").hidden = !api.flowIO();
      $("export").onclick = function () {
        api.exportFlow({ id: id }).then(function (j) {
          var u = URL.createObjectURL(new Blob([JSON.stringify(j, null, 2)], { type: "application/json" })), a = el("a"); a.href = u; a.download = f.name + ".json"; a.click(); URL.revokeObjectURL(u);
        }, fail);
      };
      if (f.kind === "advanced" || f.kind === "earlier") { $("f-readonly").hidden = false; drawSteps(f.steps || [], $("graph")); text(f.steps || [], $("f-text")); return; }
      $("f-edit").hidden = false; warn = ui.warnings($("save")); warn.show(s.warnings);
      if (f.usedBy.length) { $("f-used").textContent = "Saving changes this for " + and(f.usedBy) + "."; $("f-used").hidden = false; }
      build(f.kind === "standard" ? "standard" : "basic", s);
      $("save").onclick = function () {
        if (!editor) return;
        api.putFlowSetting({ id: id }, editor.get()).then(function (ns) {
          $("err").hidden = true; warn.show(ns && ns.warnings); say("Saved.");
          return api.getFlow({ id: id }).then(function (nf) { f = nf; head(); if (ns) build(nf.kind === "standard" ? "standard" : "basic", ns); });
        }, function (e) { say(""); fail(e); });
      };
    }).catch(fail);
  }
  S.ready.then(init);
})();
