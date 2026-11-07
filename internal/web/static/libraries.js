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
    if (kind === "custom" && level === "basic") return ["This setting is too custom to show as a slider", "Use a slider setting instead"];
    if (kind === "custom" && level === "standard") return ["This setting is too custom to show as choices", "Use simple choices instead"];
    if (kind === "selectors" && level === "basic") return ["This setting has more choices than a slider shows", "Use a slider setting instead"];
    return null;
  }

  function card(l, level) {
    var c = el("div", "card"), top = el("div", "row between"), left = el("div"), h = el("h2"), a = el("a", null, l.name), href = "/library/?id=" + encodeURIComponent(l.id);
    h.style.margin = "0"; a.href = href; h.appendChild(a); left.appendChild(h); left.appendChild(el("span", "mono muted", l.path));
    top.appendChild(left); top.appendChild(ui.stateBadge(l)); c.appendChild(top);
    var warn = mismatch(l.settingKind, level);
    if (warn) {
      var n = el("div", "notice warn"), d = el("div"); n.style.margin = "12px 0";
      d.appendChild(el("strong", null, warn[0])); d.appendChild(document.createTextNode("It keeps working exactly as before."));
      n.appendChild(d); c.appendChild(n);
      var p = el("p", "muted small"); p.style.margin = "0 0 12px"; p.appendChild(document.createTextNode("Setting: ")); p.appendChild(el("strong", null, l.settingLabel)); c.appendChild(p);
      var b = el("a", "btn small", warn[1]); b.href = href; c.appendChild(b);
      return c;
    }
    var s = el("p"); s.style.margin = "12px 0 0"; s.appendChild(document.createTextNode("Setting: ")); s.appendChild(el("strong", null, l.settingLabel)); c.appendChild(s);
    var m = el("p", "muted small", l.state === "not_started" ? "Added but not started. Nothing has been changed on disk."
      : "Space freed so far: " + ui.bytes(l.freedBytes) + " \u00b7 " + ui.count(l.waitingFiles, "file", "files") + " waiting");
    m.style.margin = "0"; c.appendChild(m);
    var r = el("div", "row"), x = el("a", "btn small", "Change setting"), y = el("a", "btn small", "Preview");
    r.style.marginTop = "12px"; x.href = href; y.href = href + "#preview"; r.appendChild(x); r.appendChild(y); c.appendChild(r);
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
