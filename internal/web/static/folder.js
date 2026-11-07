/* Folder picker with its checks: the one "enter a folder" component, used by first-run and
   by Add a library. Needs the folderpicker markup (ids path, browse, check, browser, checks).
   opt.onChange(passed, path) tells the page when Continue / Add may turn on. */
(function (g) {
  "use strict";
  var ui = g.SYP.ui, api = g.SYP.api;
  var COPY = {
    unreadable: "We can't open this folder. Check the path, and that shrink-your-shit is allowed to read it.",
    read_only: "This folder is read-only. Make it writable, then press Check again.",
    no_converter: "The converter is not installed correctly.",
    no_videos: "We found no videos here. Pick the folder that holds them."
  };
  function $(id) { return document.getElementById(id); }
  function line(cls, word, text) {
    var li = ui.el("li"); li.appendChild(ui.el("span", "badge " + cls, word)); li.appendChild(document.createTextNode(" " + text)); return li;
  }

  g.SYP.folder = function (opt) {
    var passed = false, pending = null, five = null;
    function set(ok) { passed = ok; if (opt.onChange) opt.onChange(ok, $("path").value.trim()); }
    function check() {
      var path = $("path").value.trim(), list = $("checks"); set(false);
      if (five) five.stop(); five = null; $("first5").textContent = "";
      list.textContent = "";
      if (!path) return Promise.resolve();
      $("checks-h").hidden = $("check").hidden = false; // shown once the first check has started
      list.appendChild(ui.el("li", "muted", "Checking this folder..."));
      return api.checkLibrary({}, { path: path }).then(function (r) {
        if ($("path").value.trim() !== path) return;
        list.textContent = "";
        (r.blockers || []).forEach(function (b) {
          var li = line("bad", "Fix this", b.message || COPY[b.code] || "We can't use this folder."); // the server's words (they carry fix steps); ours only if it sends none
          if (b.code === "no_converter") { // the health report says what is missing; it stays open during first-run
            var a = ui.el("a", null, "See the health report"); a.href = "/settings/system/"; li.appendChild(document.createTextNode(" ")); li.appendChild(a);
          }
          list.appendChild(li);
        });
        if (!(r.blockers || []).length) { // OK lines only when every check passed: beside a blocker some never ran
          var spaceUnknown = (r.notes || []).some(function (n) { return n.code === "space_unknown"; });
          if (!spaceUnknown) list.appendChild(line("ok", "OK", "Enough free space.")); // not an OK when it couldn't be read: the note says so
          list.appendChild(line("ok", "OK", "Video converter works."));
          list.appendChild(line("ok", "OK", "We can read and change files in this folder."));
          list.appendChild(line("ok", "OK", ui.count(r.videoCount, "video", "videos") + " found (" + ui.bytes(r.totalBytes) + ")."));
          // Once the checks pass: "Is this the right folder?" (redesign A3). It never holds up Continue / Add.
          five = ui.firstFive($("first5"), path, function () { return api.firstFilesFor({}, { path: path }); });
        }
        (r.notes || []).forEach(function (n) {
          list.appendChild(n.code === "no_hw_encoder" ? line("idle", "Not found", "No graphics card help found. That is fine; the processor will be used.")
            : line("warn", "Heads up", n.message));
        });
        set(!!r.ok);
      }, function (e) { list.textContent = ""; list.appendChild(line("bad", "Fix this", e.message)); });
    }
    // Browse: lists folders on the machine running the program.
    function browse(path) {
      api.browseDirs({ path: path }).then(function (r) {
        var box = $("browser"), ul = ui.el("ul"); box.hidden = false; box.textContent = "";
        ul.style.cssText = "list-style:none;padding:0;margin:0 0 12px";
        box.appendChild(ui.el("p", "mono", r.path));
        if (r.parent && r.parent !== r.path) { var up = ui.el("button", "btn small", "Up one folder"); up.type = "button"; up.onclick = function () { browse(r.parent); }; box.appendChild(up); }
        r.dirs.forEach(function (d) {
          var li = ui.el("li"), b = ui.el("button", "btn small", d); b.type = "button";
          b.onclick = function () { browse(r.path.replace(/[\\/]$/, "") + (r.path.indexOf("\\") >= 0 ? "\\" : "/") + d); }; li.appendChild(b); ul.appendChild(li);
        });
        box.appendChild(ul);
        var use = ui.el("button", "btn", "Use this folder"); use.type = "button";
        use.onclick = function () { $("path").value = r.path; box.hidden = true; check(); $("path").focus(); };
        box.appendChild(use);
      }, function (e) { if (opt.onError) opt.onError(e); });
    }
    $("path").addEventListener("change", function () { pending = check(); });
    $("check").onclick = function () { pending = check(); };
    $("browse").onclick = function () { browse($("path").value.trim() || "/"); };
    return {
      check: function () { pending = check(); return pending; }, pending: function () { return pending; }, passed: function () { return passed; }, path: function () { return $("path").value.trim(); },
      set: function (p) { $("path").value = p; },
      clear: function () { if (five) five.stop(); five = null; $("first5").textContent = ""; $("path").value = ""; $("checks").textContent = ""; $("checks-h").hidden = $("check").hidden = true; $("browser").hidden = true; set(false); }
    };
  };
})(window);
