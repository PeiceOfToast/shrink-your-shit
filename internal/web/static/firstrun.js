/* First-run wizard (FIRST-RUN.md). Five steps; each Continue writes through the normal
   settings and library calls, so leaving and coming back resumes at the last finished step. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, ed = S.editors;
  function $(id) { return document.getElementById(id); }
  var steps = [].slice.call(document.querySelectorAll("#wizard > section"));
  var st = { level: "basic", path: "", lib: null, editor: null, prev: null, maxStep: 1 }, fp;

  function fail(e) {
    var box = $("err"); box.textContent = e && e.message || "Something went wrong. Nothing was changed."; box.hidden = false;
    box.scrollIntoView && box.scrollIntoView({ block: "nearest" });
  }
  function go(n) {
    $("err").hidden = true;
    n = Math.max(1, Math.min(n, st.maxStep));
    steps.forEach(function (s, i) { s.hidden = i !== n - 1; });
    Array.prototype.forEach.call($("stepper").children, function (li, i) {
      if (i === n - 1) li.setAttribute("aria-current", "step"); else li.removeAttribute("aria-current");
    });
    var h = steps[n - 1].querySelector("h1"); h.focus();
    if (n === 4) buildStep4();
    if (n !== 4 && st.prev) st.prev.stop();
    if (location.hash !== "#s" + n) history.pushState(null, "", "#s" + n);
  }
  // Save what this step chose, then move on. The write happens first so a failure stays on the step.
  function next(from, write) {
    return Promise.resolve(write && write()).then(function () {
      return api.patchSettings({}, { firstRunStep: from + 1 });
    }).then(function () { st.maxStep = Math.max(st.maxStep, from + 1); go(from + 1); }, fail);
  }

  // Step 2
  function levelNow() { return document.querySelector('input[name="lvl"]:checked').value; }

  // Step 3: checks run as soon as a folder is chosen; Continue stays off until it passes.
  function setPassed(ok) {
    $("go3").setAttribute("aria-disabled", ok ? "false" : "true");
    $("why3").textContent = ok ? "This folder passes the checks. Press Continue." : "Continue turns on when the folder passes the checks."; // same height either way, so the button never moves
  }
  function nameOf(p) { var b = p.replace(/\/+$/, "").split(/[\\/]/).pop() || "Library"; return b.charAt(0).toUpperCase() + b.slice(1); }

  // Step 4: the editor for the chosen level plus the Preview card.
  function buildStep4() {
    var host = $("editor"); host.textContent = ""; $("lib-name").textContent = st.lib ? st.lib.name : "";
    if (st.prev) st.prev.stop();
    var get = function () { return st.editor.get(); };
    st.prev = ui.preview($("preview"), st.lib.id, get);
    var change = function () { st.prev.mark(); };
    if (st.level === "standard") st.editor = ed.selectors(host, change);
    else if (st.level === "advanced") st.editor = ed.startFrom(host, change);
    else {
      var c = ui.el("div", "card"); c.appendChild(ui.el("h2", null, "Smaller file or better quality?")).style.marginTop = "0";
      host.appendChild(c);
      st.editor = ed.slider(c, 3, change, st.lib.name + ": smallest file to best quality");
      c.appendChild(ui.el("p", "muted small", "Balanced is a good start for most people. Your choice is saved for this library and can be changed any time."));
    }
    st.prev.start();
  }

  function finish(dest, scan) {
    Promise.resolve(scan && api.scanLibrary({ id: st.lib.id }))
      .then(function () { return api.patchSettings({}, { firstRunDone: true, firstRunStep: 5 }); })
      .then(function () { location.href = dest; }, fail);
  }

  function wire() {
    $("go1").onclick = function () { next(1); };
    $("go2").onclick = function () { st.level = levelNow(); next(2, function () { return api.patchSettings({}, { controlLevel: st.level }); }); };
    fp = S.folder({ onChange: function (ok, p) { st.path = p; setPassed(ok); }, onError: fail });
    $("go3").onclick = function () {
      // Wait for a check the field's change event (on blur) just started.
      Promise.resolve(fp.pending()).then(function () {
        if (!fp.passed()) { $("path").focus(); return; }
        next(3, function () {
          var body = { name: nameOf(st.path), path: st.path };
          if (!st.lib) return api.addLibrary({}, { name: body.name, path: body.path, startFrom: "balanced" }).then(function (l) { st.lib = l; });
          // Coming back and choosing another folder changes this library; it never adds a second one.
          if (st.lib.path !== st.path) return api.patchLibrary({ id: st.lib.id }, body).then(function (l) { st.lib = l; });
        });
      });
    };
    $("go4").onclick = function () { next(4, function () { return api.putSetting({ id: st.lib.id }, st.editor.get()); }); };
    $("start").onclick = function () { finish("/activity/", true); };
    $("notyet").onclick = function () { finish("/library/?id=" + encodeURIComponent(st.lib.id), false); };
    steps.forEach(function (s, i) {
      if (i === 0) return;
      var b = s.querySelector("[data-back]"); if (b) b.onclick = function () { go(i); };
    });
    window.addEventListener("popstate", function () { go(+(/#s(\d)/.exec(location.hash) || [0, 1])[1]); });
  }

  function init() {
    var fresh = /[?&]fresh=1/.test(location.search);
    api.getSettings().then(function (s) {
      if (s.firstRunDone && !fresh) { location.replace("/libraries/"); return; }
      if (fresh) history.replaceState(null, "", location.pathname);
      st.level = s.controlLevel; st.maxStep = Math.min(Math.max(s.firstRunStep || 1, 1), 5);
      var radio = document.querySelector('input[name="lvl"][value="' + s.controlLevel + '"]'); if (radio) radio.checked = true;
      return api.listLibraries().then(function (l) {
        if (l.items.length) { st.lib = l.items[l.items.length - 1]; st.path = st.lib.path; }
        if (st.maxStep >= 4 && !st.lib) st.maxStep = 3;
        wire(); $("wizard").hidden = false;
        if (st.lib) fp.set(st.path);
        go(st.maxStep);
        if (st.lib) fp.check();
      });
    }).catch(fail);
  }
  S.ready.then(init);
})();
