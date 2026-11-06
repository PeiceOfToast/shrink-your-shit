/* First-run wizard (FIRST-RUN.md). Five steps; each Continue writes through the normal
   settings and library calls, so leaving and coming back resumes at the last finished step. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, ed = S.editors;
  function $(id) { return document.getElementById(id); }
  var steps = [].slice.call(document.querySelectorAll("#wizard > section"));
  var st = { level: "basic", path: "", lib: null, passed: false, editor: null, prev: null, maxStep: 1 };

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
  var COPY = {
    unreadable: "We can't open this folder. Check the path, and that shrink-your-shit is allowed to read it.",
    read_only: "This folder is read-only. Make it writable, then press Check again.",
    no_converter: "The converter is not installed correctly.",
    no_videos: "We found no videos here. Pick the folder that holds them."
  };
  function line(cls, word, text) {
    var li = ui.el("li"); li.appendChild(ui.el("span", "badge " + cls, word)); li.appendChild(document.createTextNode(" " + text)); return li;
  }
  function setPassed(ok) {
    st.passed = ok;
    var b = $("go3"); b.setAttribute("aria-disabled", ok ? "false" : "true");
    $("why3").hidden = ok;
  }
  function check() {
    var path = $("path").value.trim(), list = $("checks"); st.path = path; setPassed(false);
    if (!path) { list.textContent = ""; return; }
    list.textContent = ""; list.appendChild(ui.el("li", "muted", "Checking this folder..."));
    api.checkLibrary({}, { path: path }).then(function (r) {
      if ($("path").value.trim() !== path) return;
      list.textContent = "";
      (r.blockers || []).forEach(function (b) { list.appendChild(line("bad", "Fix this", COPY[b.code] || b.message)); });
      if (!(r.blockers || []).length) {
        list.appendChild(line("ok", "OK", "We can read and change files in this folder."));
        list.appendChild(line("ok", "OK", ui.num(r.videoCount) + " videos found (" + ui.bytes(r.totalBytes) + ")."));
      }
      (r.notes || []).forEach(function (n) {
        list.appendChild(n.code === "no_hw_encoder" ? line("idle", "Not found", "No graphics card help found. That is fine; the processor will be used.")
          : line("warn", "Heads up", n.message));
      });
      setPassed(!!r.ok);
    }, function (e) { list.textContent = ""; list.appendChild(line("bad", "Fix this", e.message)); });
  }
  function nameOf(p) { var b = p.replace(/\/+$/, "").split(/[\\/]/).pop() || "Library"; return b.charAt(0).toUpperCase() + b.slice(1); }

  // Folder browser: lists folders on the machine running the program.
  function browse(path) {
    api.browseDirs({ path: path }).then(function (r) {
      var box = $("browser"), ul = ui.el("ul", "stack"); box.hidden = false; box.textContent = "";
      ul.style.cssText = "list-style:none;padding:0;margin:0";
      box.appendChild(ui.el("p", "mono", r.path));
      if (r.parent && r.parent !== r.path) { var up = ui.el("button", "btn small", "Up one folder"); up.type = "button"; up.onclick = function () { browse(r.parent); }; box.appendChild(up); }
      r.dirs.forEach(function (d) {
        var li = ui.el("li"), b = ui.el("button", "btn small", d); b.type = "button";
        b.onclick = function () { browse(r.path.replace(/\/$/, "") + "/" + d); }; li.appendChild(b); ul.appendChild(li);
      });
      box.appendChild(ul);
      var use = ui.el("button", "btn", "Use this folder"); use.type = "button";
      use.onclick = function () { $("path").value = r.path; box.hidden = true; check(); $("path").focus(); };
      box.appendChild(use);
    }, fail);
  }

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
    $("path").addEventListener("change", check);
    $("check").onclick = check;
    $("browse").onclick = function () { browse($("path").value.trim() || "/"); };
    $("go3").onclick = function () {
      if (!st.passed) { $("path").focus(); return; }
      next(3, function () {
        if (st.lib && st.lib.path === st.path) return;
        return api.addLibrary({}, { name: nameOf(st.path), path: st.path, startFrom: "balanced" }).then(function (l) { st.lib = l; });
      });
    };
    $("go4").onclick = function () { next(4, function () { return api.putSetting({ id: st.lib.id }, st.editor.get()); }); };
    $("start").onclick = function () { finish("/activity/", true); };
    $("notyet").onclick = function () { finish("/libraries/", false); };
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
        if (l.items.length) { st.lib = l.items[l.items.length - 1]; st.path = st.lib.path; $("path").value = st.path; }
        if (st.maxStep >= 4 && !st.lib) st.maxStep = 3;
        wire(); $("wizard").hidden = false;
        go(st.maxStep);
        if (st.lib) check();
      });
    }).catch(fail);
  }
  S.ready.then(init);
})();
