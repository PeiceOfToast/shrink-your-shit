/* Libraries page: the list, and Add a library (the folder picker is the same one first-run uses). */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  function $(id) { return document.getElementById(id); }
  function nameOf(p) { var b = p.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || "Library"; return b.charAt(0).toUpperCase() + b.slice(1); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }

  function at(t) { return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }); }
  // Status in words from slice A's status (A2); sizes and other numbers live in Activity. Returns text or a fragment (for the link).
  function status(l) {
    var s = l.status;
    if (s) {
      if (s.kind === "not_scanned") return "Not scanned yet" + (s.nextScan ? " \u00b7 next scan " + at(s.nextScan) : "");
      if (s.kind === "scanning") return "Looking for videos...";
      if (s.kind === "converting") return "Converting " + ui.count(s.converting, "file", "files");
      if (s.kind === "waiting_for_worker") { // "a worker" links to Workers
        var p = document.createDocumentFragment(), a = el("a", null, "a worker"); a.href = "/workers/";
        p.appendChild(document.createTextNode("Waiting for ")); p.appendChild(a); p.appendChild(document.createTextNode(": " + ui.count(s.toDo, "video", "videos") + " to do")); return p;
      }
      if (s.kind === "scanned") return [s.videos != null ? ui.count(s.videos, "video", "videos") : "", s.lastScan ? "last scan " + at(s.lastScan) : "", s.nextScan ? "next " + at(s.nextScan) : ""].filter(Boolean).join(" \u00b7 ");
    }
    // An older answer without status: what the library item said before slice A.
    if (l.state === "not_started") return "Not scanned yet" + (l.nextScan ? " \u00b7 next scan " + at(l.nextScan) : "");
    if (l.state === "working") return "Converting " + ui.count(l.workingFiles, "file", "files");
    if (l.state === "paused") return "Paused";
    return l.nextScan ? "Next scan " + at(l.nextScan) : "";
  }

  // The flow tag is the server's text (B4), the same on the Flows page and the flow's page.
  function card(l) {
    var c = el("div", "card"), top = el("div", "row between"), left = el("div"), h = el("h2"), a = el("a", null, l.name), href = "/library/?id=" + encodeURIComponent(l.id);
    h.style.margin = "0"; a.href = href; h.appendChild(a); left.appendChild(h); left.appendChild(el("span", "mono muted", l.path));
    top.appendChild(left); if (l.tag) top.appendChild(el("span", "badge info", l.tag)); c.appendChild(top);
    var words = status(l);
    if (words) { var st = el("p", "lib-status"); if (typeof words === "string") st.textContent = words; else st.appendChild(words); st.style.margin = "12px 0 0"; c.appendChild(st); }
    // Failed files (A2, 19:58Z): a second line in the warn style, only when there are any; "see why" opens the Files
    // view's Failed list for this library.
    if (l.failedFiles > 0) {
      var fl = el("p", "small"), why = el("a", null, "see why"); fl.style.cssText = "margin:4px 0 0;color:var(--warn)";
      fl.appendChild(document.createTextNode(ui.count(l.failedFiles, "file", "files") + " failed: ")); why.href = "/activity/failed/?library=" + encodeURIComponent(l.id); fl.appendChild(why); c.appendChild(fl);
    }
    // No buttons on the card (design, 19:59Z): Scan lives on the library page, which the name opens.
    return c;
  }

  function load() {
    return Promise.all([api.listLibraries(), api.listFlows()]).then(function (r) {
      var box = $("lib-list"); box.textContent = ""; $("empty").hidden = r[0].total > 0;
      r[0].items.forEach(function (l) { box.appendChild(card(l)); });
      // Add a library: a Flow dropdown, the most-used flow first chosen (B5). With no flow at all (only right after an
      // upgrade with none) it stays hidden and the server makes a Basic flow at Balanced, named after the library.
      var sel = $("addflow"), was = sel.value; sel.textContent = ""; $("addflow-f").hidden = !r[1].items.length;
      var top = r[1].items.slice().sort(function (a, b) { return b.usedBy.length - a.usedBy.length; })[0];
      r[1].items.forEach(function (f) { var o = el("option", null, f.name + (f.tag ? " \u2014 " + f.tag : "")); o.value = f.id; sel.appendChild(o); });
      if (top) sel.value = was && r[1].items.some(function (f) { return f.id === was; }) ? was : top.id;
    }, fail);
  }

  function init() {
    var fp = S.folder({
      onChange: function (ok) { $("addbtn").setAttribute("aria-disabled", ok ? "false" : "true"); },
      onError: fail
    });
    $("addbtn").onclick = function () {
      Promise.resolve(fp.pending()).then(function () {
        if (!fp.passed()) { $("path").focus(); return; }
        var path = fp.path();
        var body = { name: nameOf(path), path: path };
        if (!$("addflow-f").hidden) body.flowId = $("addflow").value; else body.startFrom = "balanced";
        return api.addLibrary({}, body).then(function (l) {
          fp.clear(); $("err").hidden = true;
          $("added").textContent = "Added " + l.name + ". Nothing is converted until you start it.";
          return load();
        });
      }).catch(fail);
    };
    load();
  }
  S.ready.then(init);
})();
