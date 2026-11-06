/* Settings: every tab is drawn from settings.json, one control per setting. System is the health page. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  var host = document.getElementById("host"), tab = host.getAttribute("data-tab").toLowerCase(), RANK = { basic: 0, standard: 1, advanced: 2 };
  function $(id) { return document.getElementById(id); }
  function get(o, path) { return path.split(".").reduce(function (a, k) { return a == null ? a : a[k]; }, o); }
  function put(o, path, v) { var p = path.split("."), last = p.pop(); p.forEach(function (k) { o = o[k] = o[k] || {}; }); o[last] = v; }
  function field(e, input, hint) {
    var box = el("div", "field"), lb = el("label", null, e.label), id = "f-" + e.key.replace(/\./g, "-");
    input.id = id; lb.htmlFor = id; box.appendChild(lb); box.appendChild(input);
    if (hint) { var h = el("span", "hint", hint); h.id = id + "-h"; input.setAttribute("aria-describedby", h.id); box.appendChild(h); }
    return box;
  }
  function check(e, id, checked) {
    var row = el("div", "check"), cb = el("input"), lb = el("label", null, e.label); cb.type = "checkbox"; cb.id = id; cb.checked = !!checked; lb.htmlFor = id;
    if (e.hint) { lb.appendChild(document.createElement("br")); lb.appendChild(el("span", "hint", e.hint)); }
    row.appendChild(cb); row.appendChild(lb); return [row, cb];
  }

  function keyHint(e, s) { return get(s, e.hasKey) ? "A key is saved. Type a new one to replace it." : "Not set."; }

  // ---- the registry-driven tabs
  function form(reg, s) {
    var f = el("form", "stack"), ctl = {}, fs = null, last = null, libs = [], say = el("span", "muted"), noteFor = {};
    f.id = tab + "-form"; f.noValidate = true; say.id = "saved"; say.setAttribute("role", "status");
    reg.forEach(function (e) {
      if (e.type === "info") return;
      if (e.group !== last) { fs = el("fieldset"); fs.appendChild(el("legend", null, e.group)); f.appendChild(fs); last = e.group; }
      if (e.intro) fs.appendChild(el("p", "muted", e.intro));
      var v = get(s, e.key);
      if (e.type === "radio") {
        e.options.forEach(function (o) {
          var row = el("div", "check"), r = el("input"), id = e.idPrefix + "-" + o.value, lb = el("label", null, o.label);
          r.type = "radio"; r.name = e.key; r.value = o.value; r.id = id; r.checked = v === o.value; lb.htmlFor = id; lb.firstChild.replaceWith(el("strong", null, o.label));
          lb.appendChild(document.createElement("br")); lb.appendChild(el("span", "muted", o.hint)); row.appendChild(r); row.appendChild(lb); fs.appendChild(row);
          r.onchange = downgrade;
        });
        var warn = el("div", "notice warn"); warn.id = "downgrade"; warn.setAttribute("role", "status"); warn.hidden = true; fs.appendChild(warn);
        ctl[e.key] = { get: function () { var c = f.querySelector('input[name="' + e.key + '"]:checked'); return c && c.value; } };
      } else if (e.type === "checkbox") {
        var c = check(e, e.id || "f-" + e.key.replace(/\./g, "-"), v); fs.appendChild(c[0]); ctl[e.key] = { get: function () { return c[1].checked; } };
        if (e.key === "perLibraryFlow") { var pv = el("div"); pv.id = "pl-preview"; fs.appendChild(pv); c[1].onchange = function () { choosePerLibrary(c[1].checked); }; choosePerLibrary(c[1].checked); }
      } else if (e.type === "readout") {
        var p = el("p"); p.appendChild(el("strong", null, e.label + ": ")); p.appendChild(document.createTextNode(v || "")); fs.appendChild(p);
      } else if (e.type === "token") {
        fs.appendChild(token(e, s));
      } else {
        var inp = el("input"); inp.type = e.type === "secret" ? "password" : e.type === "time" ? "time" : e.type === "number" ? "number" : "text";
        if (e.type === "number") { inp.min = e.min; inp.max = e.max; inp.step = 1; inp.style.width = "7rem"; }
        if (e.type === "text") inp.size = 34;
        if (e.type !== "secret") inp.value = v == null ? "" : v; else inp.autocomplete = "new-password";
        var hint = e.type === "secret" ? keyHint(e, s) : e.hint;
        fs.appendChild(field(e, inp, hint)); ctl[e.key] = { get: function () { return e.type === "number" ? (inp.value === "" ? null : +inp.value) : inp.value; }, el: inp, e: e };
      }
    });
    var row = el("div", "row"), save = el("button", "btn primary", "Save"); save.type = "submit"; row.appendChild(save); row.appendChild(say); f.appendChild(row);

    f.onsubmit = function (ev) {
      ev.preventDefault();
      var patch = {}, n = 0;
      for (var k in ctl) {
        var c = ctl[k], v = c.get();
        if (c.e && c.e.type === "number" && (v == null || !c.el.checkValidity() || v !== Math.floor(v))) { say.textContent = c.e.error; c.el.focus(); return; }
        var old = get(s, k);
        if (c.e && c.e.type === "secret") { if (v) { put(patch, k, v); n++; } continue; }
        if (v !== (old == null ? (typeof v === "string" ? "" : old) : old)) { put(patch, k, v); n++; }
      }
      var qf = ctl["quietHours.from"], qt = ctl["quietHours.to"];
      if (qf && qt && !qf.get() !== !qt.get()) { say.textContent = "Set both a start and an end, or leave both empty."; (qf.get() ? qt : qf).el.focus(); return; }
      if (!n) { say.textContent = "Nothing was changed."; return; }
      api.patchSettings({}, patch).then(function (ns) {
        s = ns; say.textContent = "Saved."; downgrade();
        for (var k in ctl) if (ctl[k].e && ctl[k].e.type === "secret") { ctl[k].el.value = ""; $(ctl[k].el.id + "-h").textContent = keyHint(ctl[k].e, ns); }
        return S.menu(ns);
      },
        function (er) { say.textContent = er.message; });
    };
    host.appendChild(f);

    // Moving to a lower level: say what happens to libraries it cannot show. No action here.
    function downgrade() {
      var c = f.querySelector('input[name="controlLevel"]:checked'), w = $("downgrade"); if (!c || !w) return;
      var lvl = c.value, hit = libs.filter(function (l) { return lvl === "basic" ? l.settingKind !== "slider" : lvl === "standard" && l.settingKind === "custom"; });
      if (RANK[lvl] >= RANK[s.controlLevel] || !hit.length) { w.hidden = true; return; }
      w.textContent = "";
      var d = el("div"); d.appendChild(el("strong", null, "If you choose " + lvl.charAt(0).toUpperCase() + lvl.slice(1) + " now"));
      d.appendChild(document.createTextNode(hit.length + " of your " + libs.length + (libs.length === 1 ? " library" : " libraries") + " (" + hit.map(function (l) { return "“" + l.name + "”"; }).join(", ") + ") " +
        (hit.length === 1 ? "uses a setting" : "use settings") + (lvl === "basic" ? " too custom to show as a slider" : " too custom to show as choices") + ". " + (hit.length === 1 ? "It keeps" : "They keep") + " running exactly as before, and you can edit " + (hit.length === 1 ? "it" : "them") + " by switching back. Nothing is converted."));
      w.appendChild(d); w.hidden = false;
    }
    if (ctl.controlLevel) api.listLibraries().then(function (r) { libs = r.items; downgrade(); }, function () {});

    var loaded = false;
    function choosePerLibrary(on) {
      var run = function () { S.settingsFlow($("pl-preview"), on); };
      if (!on) { if (loaded) run(); return; }
      if (loaded) return run();
      var sc = document.createElement("script"); sc.src = "/assets/settings-flow.js"; sc.onload = function () { loaded = true; run(); }; document.head.appendChild(sc);
    }
  }

  // ---- access token: shown only when asked for, never stored by the page
  function token(e, s) {
    var box = el("div", "field"), lb = el("label", null, e.label), row = el("div", "row"), inp = el("input"), show = el("button", "btn", "Show"), mk = el("button", "btn", "Make a new one"), msg = el("p", "muted");
    inp.id = "f-token"; lb.htmlFor = inp.id; inp.readOnly = true; inp.size = 26; inp.value = "•".repeat(12); msg.setAttribute("role", "status");
    show.type = mk.type = "button"; show.setAttribute("aria-controls", inp.id);
    var hint = el("span", "hint", s.authRequired ? e.hint : "Not needed on this computer. " + e.hint); hint.id = "f-token-h"; inp.setAttribute("aria-describedby", hint.id);
    function hide() { inp.value = "•".repeat(12); show.textContent = "Show"; }
    show.onclick = function () {
      if (show.textContent === "Hide") return hide();
      api.getToken().then(function (t) { inp.value = t.token; show.textContent = "Hide"; }, function (er) { msg.textContent = er.message; });
    };
    mk.onclick = function () {
      var open = box.querySelector(".notice.warn"); if (open) { open.querySelector(".btn.danger").focus(); return; } // one confirm at a time
      msg.textContent = ""; var n = el("div", "notice warn"), d = el("div"), r2 = el("div", "row"), yes = el("button", "btn danger", "Make a new one"), no = el("button", "btn", "Cancel");
      yes.type = no.type = "button"; d.appendChild(el("strong", null, "Make a new token?")); d.appendChild(document.createTextNode("The old one stops working, so other devices must use the new one."));
      r2.style.marginTop = "8px"; r2.appendChild(yes); r2.appendChild(no); d.appendChild(r2); n.appendChild(d); n.setAttribute("role", "group"); box.appendChild(n); yes.focus();
      no.onclick = function () { n.remove(); mk.focus(); };
      yes.onclick = function () { api.rotateToken().then(function (t) { n.remove(); inp.value = t.token; show.textContent = "Hide"; msg.textContent = "New token made. Use it to sign in on other devices."; mk.focus(); }, function (er) { n.remove(); msg.textContent = er.message; }); };
    };
    row.appendChild(inp); row.appendChild(show); row.appendChild(mk);
    box.appendChild(lb); box.appendChild(row); box.appendChild(hint); box.appendChild(msg);
    return box;
  }

  // ---- System: health, version, logs, backup, update notice
  var BADGE = { ok: ["ok", "OK"], warn: ["warn", "Heads up"], bad: ["bad", "Fix this"] };
  function system(s) {
    var list = el("ul", "stack"), ver = el("p", "muted"), logs = el("pre", "mono"), note = el("p", "muted"), say = el("p", "muted");
    list.style.cssText = "list-style:none;padding:0"; list.setAttribute("aria-live", "polite"); list.setAttribute("aria-label", "Health checks");
    logs.tabIndex = 0; logs.setAttribute("role", "region"); logs.setAttribute("aria-label", "Recent log lines"); logs.style.cssText = "overflow:auto;max-height:16rem;background:var(--surface-2);padding:12px;border-radius:8px;margin:0";
    say.setAttribute("role", "status");
    if (s.updateAvailable) { var n = el("div", "notice info"), d = el("div"); d.appendChild(el("strong", null, "Version " + s.updateAvailable.version + " is available")); d.appendChild(document.createTextNode("Update the same way you installed shrink-your-shit.")); n.appendChild(d); host.appendChild(n); }
    var h = el("div", "row between"), btn = el("button", "btn", "Check again"); btn.type = "button"; h.appendChild(el("h2", null, "Health checks")); h.style.alignItems = "baseline"; h.appendChild(btn);
    host.appendChild(h); host.appendChild(list); host.appendChild(ver);
    host.appendChild(el("h2", null, "Recent log lines")); host.appendChild(logs);
    host.appendChild(el("h2", null, "Backup")); host.appendChild(el("p", null, "Download a copy of your settings and the list of what has been converted. Your videos are not included."));
    var bk = el("button", "btn", "Download a backup"); bk.type = "button"; host.appendChild(bk); host.appendChild(say);
    function load() {
      return api.getHealth().then(function (r) {
        list.textContent = ""; r.checks.forEach(function (c) { var li = el("li"), b = BADGE[c.status] || BADGE.warn; li.appendChild(el("span", "badge " + b[0], b[1])); li.appendChild(document.createTextNode(" " + c.label + (c.detail ? ". " + c.detail : ""))); list.appendChild(li); });
        ver.textContent = "shrink-your-shit " + r.version; logs.textContent = r.logsTail.length ? r.logsTail.join("\n") : "Nothing has been logged yet.";
      }, function (er) { say.textContent = er.message; });
    }
    btn.onclick = load;
    bk.onclick = function () {
      api.backup().then(function (r) {
        if (r && r.blob) return r.blob(); // a file the server sent
        if (r && typeof r === "object") return new Blob([JSON.stringify(r, null, 2)], { type: "application/json" }); // the server sent the backup as JSON
        throw { message: "The server sent no backup. Nothing was downloaded." };
      }).then(function (b) { var u = URL.createObjectURL(b), a = el("a"); a.href = u; a.download = "shrink-your-shit-backup"; a.click(); URL.revokeObjectURL(u); say.textContent = "Backup downloaded."; }, function (er) { say.textContent = er.message; });
    };
    load();
  }

  S.ready.then(function () { return Promise.all([fetch("/assets/settings.json").then(function (r) { return r.json(); }), api.getSettings()]); }).then(function (r) {
    if (tab === "system") system(r[1]); else form(r[0].filter(function (e) { return e.tab === tab; }), r[1]);
  }).catch(function () {});
})();
