/* Flows page: the one list of every flow, with Rename, Duplicate, Delete, New flow and Import. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  function $(id) { return document.getElementById(id); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  var MADE = { slider: "Slider", selectors: "Selectors", builder: "Flow builder" };
  function ago(iso) {
    var d = Math.floor((Date.now() - new Date(iso)) / 864e5);
    return d < 1 ? "Today" : d < 14 ? d + (d === 1 ? " day ago" : " days ago") : Math.floor(d / 7) + " weeks ago";
  }
  function btn(txt, cls, fn) { var b = el("button", "btn small " + (cls || ""), txt); b.type = "button"; b.onclick = fn; return b; }

  function rename(f, cell) {
    var form = el("form", "row"), id = "rn-" + f.id, lb = el("label", "sr", "New name for " + f.name), inp = el("input");
    lb.htmlFor = id; inp.id = id; inp.type = "text"; inp.value = f.name; inp.size = 26;
    var ok = el("button", "btn small primary", "Save"), no = btn("Cancel", "", function () { load("f-" + f.id); });
    form.appendChild(lb); form.appendChild(inp); form.appendChild(ok); form.appendChild(no);
    form.onsubmit = function (e) { e.preventDefault(); if (inp.value.trim()) api.renameFlow({ id: f.id }, { name: inp.value.trim() }).then(function () { return load("f-" + f.id); }, fail); };
    cell.textContent = ""; cell.appendChild(form); inp.focus(); inp.select();
  }

  function row(f, tb) {
    var tr = el("tr"), th = el("th"), a = el("a", null, f.name);
    a.setAttribute("data-key", "f-" + f.id); th.scope = "row"; th.style.cssText = "background:none;color:inherit"; a.href = "/flow/?id=" + encodeURIComponent(f.id); th.appendChild(a); tr.appendChild(th);
    var used = el("td");
    if (f.usedBy.length) { used.appendChild(document.createTextNode(f.usedBy.join(", "))); if (f.madeWith === "builder") { used.appendChild(document.createTextNode(" ")); used.appendChild(el("span", "badge warn", "too custom for a slider")); } }
    else used.appendChild(el("span", "muted", "No library"));
    tr.appendChild(used); tr.appendChild(el("td", null, MADE[f.madeWith] || f.madeWith)); tr.appendChild(el("td", null, ago(f.changedAt)));
    var act = el("td"), r = el("div", "row"), v = el("a", "btn small", "View"); v.href = a.href;
    r.appendChild(v); r.appendChild(btn("Rename", "", function () { rename(f, th); })); r.appendChild(btn("Duplicate", "", function () { api.duplicateFlow({ id: f.id }).then(function (n) { return load("f-" + n.id); }, fail); }));
    if (f.usedBy.length) { // a real control, disabled, with the reason beside it
      var d = el("button", "btn small danger", "Delete"), why = el("span", "hint small", "In use by " + f.usedBy.join(", ")); d.type = "button"; d.setAttribute("aria-disabled", "true");
      why.id = "why-" + f.id; d.setAttribute("aria-describedby", why.id); r.appendChild(d); r.appendChild(why);
    } else r.appendChild(btn("Delete", "danger", function () { $("del-q").textContent = "Delete " + f.name + "?"; $("del-dialog").returnValue = ""; $("del-dialog").setAttribute("data-id", f.id); $("del-dialog").showModal(); }));
    act.appendChild(r); tr.appendChild(act); tb.appendChild(tr);
  }

  // key: the control to focus once the list is redrawn ("top" = the page heading).
  function load(key) {
    return api.listFlows().then(function (r) {
      var box = $("flow-list"); box.textContent = "";
      if (!r.items.length) { var c = el("div", "card empty"); c.appendChild(el("p", null, "No flows yet. Press New flow to start one.")); box.appendChild(c); return; }
      var w = el("div", "table-wrap"), t = el("table"), cap = el("caption", "small muted"), hd = el("thead"), hr = el("tr"), tb = el("tbody");
      var unused = r.items.filter(function (f) { return !f.usedBy.length; });
      cap.style.cssText = "text-align:left;padding-bottom:8px";
      cap.textContent = r.total + (r.total === 1 ? " flow." : " flows.") + (unused.length === 1 ? " “" + unused[0].name + "” is unused and can be deleted." : unused.length ? " " + unused.length + " are unused and can be deleted." : "");
      t.appendChild(cap);
      ["Name", "Used by", "Made with", "Last changed", ""].forEach(function (c) { var th = el("th"); th.scope = "col"; if (c) th.textContent = c; else th.appendChild(el("span", "sr", "Actions")); hr.appendChild(th); });
      hd.appendChild(hr); t.appendChild(hd); t.appendChild(tb); w.appendChild(t); box.appendChild(w);
      r.items.forEach(function (f) { row(f, tb); });
      var t0 = key === "top" ? document.querySelector("h1") : typeof key === "string" && box.querySelector('[data-key="' + key + '"]');
      if (t0) { t0.tabIndex = -1; t0.focus(); }
    }, fail);
  }

  function init() {
    $("newflow").onclick = function () { // each one gets a name of its own: New flow, New flow 2, ...
      api.listFlows().then(function (r) {
        var taken = r.items.map(function (f) { return f.name; }), n = 1, name = "New flow";
        while (taken.indexOf(name) >= 0) name = "New flow " + (++n);
        return api.createFlow({}, { name: name });
      }).then(function (f) { location.href = "/flow/?id=" + encodeURIComponent(f.id); }, fail); };
    $("import").onclick = function () { $("importfile").click(); };
    $("importfile").onchange = function () {
      var f = this.files[0]; if (!f) return;
      var rd = new FileReader();
      rd.onload = function () {
        var body; try { body = JSON.parse(rd.result); } catch (e) { fail({ message: "That file is not a flow. Nothing was imported." }); return; }
        api.importFlow({}, body).then(load, fail);
      };
      rd.readAsText(f); this.value = "";
    };
    $("del-dialog").addEventListener("close", function () {
      if ($("del-dialog").returnValue !== "delete") return;
      api.deleteFlow({ id: $("del-dialog").getAttribute("data-id") }).then(function () { return load("top"); }, fail);
    });
    load();
  }
  S.ready.then(init);
})();
