/* Libraries page: the list, and Add a library (the folder picker is the same one first-run uses). */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  function $(id) { return document.getElementById(id); }
  function nameOf(p) { var b = p.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || "Library"; return b.charAt(0).toUpperCase() + b.slice(1); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }

  // What the card says when the setting does not fit the control level (the kind comes from the engine):
  // custom needs Advanced to be edited; a Standard setting is more than the Basic slider can show.
  function mismatch(kind, level) {
    if (kind === "custom" && level === "basic") return "This setting is too custom to show as a slider";
    if (kind === "custom" && level === "standard") return "This setting is too custom to show as choices";
    if (kind === "selectors" && level === "basic") return "This setting has more choices than a slider shows";
    return null;
  }
  // The flow tag (redesign A2; flows themselves are piece B): the tier and what the setting is called today.
  function tag(l) { return ({ slider: "Basic \u00b7 ", selectors: "Standard \u00b7 " }[l.settingKind] || "") + l.settingLabel; }
  // Status in words, only from what the server gives today. Sizes and other numbers live in Activity (A2).
  function status(l) {
    if (l.state === "not_started") return "Not scanned yet";
    if (l.state === "working") return "Converting " + ui.count(l.workingFiles, "file", "files");
    if (l.state === "needs_attention") return "Needs attention";
    if (l.state === "paused") return "Paused";
    return l.nextScan ? "Next scan " + new Date(l.nextScan).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
  }

  function card(l, level) {
    var c = el("div", "card"), top = el("div", "row between"), left = el("div"), h = el("h2"), a = el("a", null, l.name), href = "/library/?id=" + encodeURIComponent(l.id);
    h.style.margin = "0"; a.href = href; h.appendChild(a); left.appendChild(h); left.appendChild(el("span", "mono muted", l.path));
    top.appendChild(left); top.appendChild(el("span", "badge info", tag(l))); c.appendChild(top);
    var warn = mismatch(l.settingKind, level);
    if (warn) {
      var n = el("div", "notice warn"), d = el("div"); n.style.margin = "12px 0 0";
      d.appendChild(el("strong", null, warn)); d.appendChild(document.createTextNode("It keeps working exactly as before."));
      n.appendChild(d); c.appendChild(n);
    }
    var st = el("p", null, status(l)); st.style.margin = "12px 0 0"; st.setAttribute("role", "status"); c.appendChild(st);
    if (l.state === "needs_attention") { st.appendChild(document.createTextNode(": ")); var f = el("a", null, "see Activity > Failed"); f.href = "/activity/failed/"; st.appendChild(f); }
    // One button, Scan. Until workers (workshop S2) a scan also starts converting, so a library that is not started
    // gets no button here: nothing converts until the person presses Start on the library page.
    if (l.started) {
      var b = el("button", "btn small", "Scan"); b.type = "button"; b.style.marginTop = "12px";
      b.onclick = function () {
        if (b.getAttribute("aria-disabled") === "true") return;
        b.setAttribute("aria-disabled", "true");
        api.scanLibrary({ id: l.id }).then(function () { st.textContent = "Scan started. Progress shows in Activity."; }, fail)
          .then(function () { b.removeAttribute("aria-disabled"); });
      };
      c.appendChild(b);
    }
    return c;
  }

  function load() {
    return Promise.all([api.listLibraries(), api.getSettings()]).then(function (r) {
      var box = $("lib-list"); box.textContent = ""; $("empty").hidden = r[0].total > 0;
      r[0].items.forEach(function (l) { box.appendChild(card(l, r[1].controlLevel)); });
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
        return api.addLibrary({}, { name: nameOf(path), path: path, startFrom: "balanced" }).then(function (l) {
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
