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
  // The library answer must carry its id: every later step uses it (a reply the page could not read once sent
  // the Preview to /libraries/undefined). Reloading picks the library up from the list.
  function keep(l) {
    if (!l || !l.id) return Promise.reject({ message: "Your library was saved, but this page could not read the server's answer. Reload the page to carry on." });
    st.lib = l;
  }
  function nameOf(p) { var b = p.replace(/\/+$/, "").split(/[\\/]/).pop() || "Library"; return b.charAt(0).toUpperCase() + b.slice(1); }

  // "Your language" (A4) pre-filled from the browser: its language's English name, matched in our list.
  function browserLang(list) {
    try {
      var dn = new Intl.DisplayNames(["en"], { type: "language" }), langs = navigator.languages || [navigator.language];
      for (var i = 0; i < langs.length; i++) { var n = dn.of(langs[i].split("-")[0]), hit = list.filter(function (x) { return (x[2] || x[1]) === n; })[0]; if (hit) return hit[0]; }
    } catch (e) {}
    return "";
  }
  // Step 4: the editor for the chosen level; no Preview card (redesign A4). At Standard the Preview still runs, unseen,
  // only for the warnings above Continue, until workshop's warnings-only check exists (REDESIGN E).
  function buildStep4() {
    var host = $("editor"); host.textContent = ""; $("lib-name").textContent = st.lib ? st.lib.name : "";
    if (st.prev) st.prev.stop();
    st.editor = null; st.lang = null;
    var get = function () { return st.editor.get(); };
    if (!st.warn) st.warn = ui.warnings($("go4")); st.warn.show([]);
    st.prev = st.level === "standard" ? ui.preview($("preview"), st.lib.id, get, { onWarnings: st.warn.show }) : null;
    var change = function () { if (st.prev) st.prev.mark(); };
    if (st.level === "standard") {
      // The library's setting answer says which Audio and Subtitles choices this version can save (addendum 13).
      st.editor = ed.selectors(host, change);
      api.getSetting({ id: st.lib.id }).then(function (s) {
        if (!s.availableAudio && !s.availableSubtitles && !s.availableFileTypes) return;
        // Rebuild with what the person has chosen so far, so nothing they picked meanwhile is lost.
        var now = st.editor.get(); now.availableAudio = s.availableAudio; now.availableSubtitles = s.availableSubtitles; now.availableFileTypes = s.availableFileTypes;
        host.textContent = ""; st.editor = ed.selectors(host, change, now);
      }, function () {});
    } else if (st.level === "advanced") st.editor = ed.startFrom(host, change);
    else {
      // Basic: the A1 editor (slider, Your files become, Picture, Audio, the fixed lines) and "Your language" (A4).
      var c = ui.el("div", "card"), box = ui.el("div"), f = ui.el("div", "field"), lb = ui.el("label", null, "Your language"), sel = ui.el("select"),
        hint = ui.el("span", "hint", "Audio in this language is kept. You can add more languages later in Settings.");
      c.appendChild(ui.el("h2", null, "Smaller file or better quality?")).style.marginTop = "0";
      sel.id = "f-lang"; lb.htmlFor = sel.id; hint.id = "f-lang-h"; sel.setAttribute("aria-describedby", hint.id);
      f.appendChild(lb); f.appendChild(sel); f.appendChild(hint); c.appendChild(box); c.appendChild(f); host.appendChild(c);
      Promise.all([api.getSetting({ id: st.lib.id }), api.getSettings(), fetch("/assets/languages.json").then(function (r) { return r.json(); })]).then(function (r) {
        var s = r[0], have = r[1].languages || [], names = {};
        r[2].forEach(function (x) { names[x[0]] = x[1]; var o = ui.el("option", null, x[1]); o.value = x[0]; sel.appendChild(o); });
        sel.value = have[0] || browserLang(r[2]) || r[2][0][0];
        var draw = function () {
          var now = st.editor ? st.editor.get() : { quality: s.quality || 3, audio: s.audio };
          box.textContent = ""; st.editor = ed.basic(box, { quality: now.quality, audio: now.audio, becomes: s.becomes }, [sel.value], names, null, st.lib.name + ": smallest file to best quality", true);
        };
        sel.onchange = draw; draw();
        // Saved on Continue: the chosen language first, any others kept (the same list as Settings > General).
        st.lang = function () { return [sel.value].concat(have.filter(function (c) { return c !== sel.value; })); };
      }, fail);
    }
    if (st.prev) st.prev.start();
  }

  // Step 5 (A4): both buttons scan (reading only); Start converting also starts the worker on this computer (S2).
  // Nothing changes on disk before that (SAF-UI-6).
  function finish(dest, start) {
    api.scanLibrary({ id: st.lib.id })
      .then(function () { return start && api.listWorkers().then(function (r) { return api.startWorker({ id: r.workers[0].id }); }); })
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
          if (!st.lib) return api.addLibrary({}, { name: body.name, path: body.path, startFrom: "balanced" }).then(keep);
          // Coming back and choosing another folder changes this library; it never adds a second one.
          if (st.lib.path !== st.path) return api.patchLibrary({ id: st.lib.id }, body).then(keep);
        });
      });
    };
    $("go4").onclick = function () {
      if (!st.editor) return; // the Basic editor draws once the language list is in
      next(4, function () {
        return Promise.resolve(st.lang && api.patchSettings({}, { languages: st.lang() })).then(function () { return api.putSetting({ id: st.lib.id }, st.editor.get()); });
      });
    };
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
