/* One flow, read-only: the diagram and the numbered text version, always both. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  function $(id) { return document.getElementById(id); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  var HEAD = { start: "Start", check: "Check", video: "Video", audio: "Audio", subtitles: "Subtitles", filetype: "File type", finish: "Finish", skip: "Skip", resolution: "Resolution" };
  var MADE = { slider: "the slider", selectors: "the simple choices", builder: "the builder" };

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
      (s.branches || []).forEach(function (b) { if (b.steps.length > 1 || b.label !== "Yes") { var li = el("li", null, b.label + ":"); var sub = el("ol"); text(b.steps, sub); li.appendChild(sub); ol.appendChild(li); } else text(b.steps, ol); });
    });
  }

  function init() {
    var id = new URLSearchParams(location.search).get("id");
    if (!id) { fail({ message: "No flow was chosen. Open one from the Flows page." }); return; }
    Promise.all([api.getFlow({ id: id }), api.listFlows(), api.listLibraries()]).then(function (r) {
      var f = r[0], meta = r[1].items.filter(function (x) { return x.id === id; })[0] || { usedBy: [], madeWith: "" }, libs = r[2].items;
      $("f-title").textContent = f.name; document.title = f.name + " - shrink-your-shit";
      $("f-meta").textContent = (meta.usedBy.length ? "Used by " + meta.usedBy.length + (meta.usedBy.length === 1 ? " library: " : " libraries: ") + meta.usedBy.join(", ") : "Not used by any library") + " · Made with " + (MADE[meta.madeWith] || "the builder");
      var note = $("f-note");
      note.textContent = " This shows what happens to each video, from left to right. ";
      var lib = libs.filter(function (l) { return meta.usedBy.length === 1 && l.name === meta.usedBy[0]; })[0];
      if (meta.madeWith !== "builder" && lib) { note.appendChild(document.createTextNode("Change the setting on the ")); var a = el("a", null, lib.name + " library"); a.href = "/library/?id=" + encodeURIComponent(lib.id); note.appendChild(a); note.appendChild(document.createTextNode(" and this flow updates.")); }
      drawSteps(f.steps, $("graph")); text(f.steps, $("f-text"));
      $("f-body").hidden = false;
      $("export").hidden = false; $("builder").hidden = false;
      $("export").onclick = function () {
        api.exportFlow({ id: id }).then(function (j) {
          var u = URL.createObjectURL(new Blob([JSON.stringify(j, null, 2)], { type: "application/json" })), a = el("a"); a.href = u; a.download = f.name + ".json"; a.click(); URL.revokeObjectURL(u);
        }, fail);
      };
    }).catch(fail);
  }
  S.ready.then(init);
})();
