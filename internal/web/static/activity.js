/* Activity: the header (savings, Pause/Resume), the tab counts and the five tabs. Polls every
   few seconds; a refresh only redraws what changed and puts focus back where it was. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  var pane = document.getElementById("pane"), tab = pane.getAttribute("data-tab"), seen = {}, T = {}, confirmId = null;
  function $(id) { return document.getElementById(id); }
  function cap(s) { return s.charAt(0).toUpperCase() + s.slice(1); }
  function base(p) { return p.split(/[\\/]/).pop(); }
  function nameOf(i) { return i.library + " / " + base(i.path); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function clear() { $("err").hidden = true; }

  // Draw only when the data changed, and give focus back to the same control (data-key) afterwards.
  function draw(key, data, fn) {
    var j = JSON.stringify(data); if (seen[key] === j) return; seen[key] = j;
    var a = document.activeElement, k = a && a.getAttribute && a.getAttribute("data-key");
    fn();
    if (k) { var n = pane.querySelector('[data-key="' + k + '"]'); if (n) n.focus(); }
  }
  function heading(t, id) { var h = el("h2", null, t); h.id = id; h.tabIndex = -1; h.style.marginTop = "0"; return h; }
  function after() { var h = pane.querySelector("h2"); if (h) h.focus(); } // a row went away: land on the heading
  function table(cols) {
    var w = el("div", "table-wrap"), t = el("table"), tr = el("tr"), tb = el("tbody");
    cols.forEach(function (c) { var th = el("th"); th.scope = "col"; if (c === "") th.appendChild(el("span", "sr", "Actions")); else th.textContent = c; tr.appendChild(th); });
    var hd = el("thead"); hd.appendChild(tr); t.appendChild(hd); t.appendChild(tb); w.appendChild(t);
    return { wrap: w, body: tb, row: function (cells) {
      var r = el("tr"); cells.forEach(function (c) { var td = el("td"); if (typeof c === "string") td.textContent = c; else if (c) td.appendChild(c); r.appendChild(td); }); tb.appendChild(r); } };
  }
  function btn(txt, key, fn, cls) { var b = el("button", cls || "btn small", txt); b.type = "button"; b.setAttribute("data-key", key); b.onclick = fn; return b; }
  function empty(t) { var c = el("div", "card empty"); c.appendChild(el("p", null, t)); return c; }
  // Run an action, refresh, and (when a row went away) land on the heading AFTER the redraw, so focus is not lost.
  function act(p, land) { return p.then(refresh).then(land ? after : null).catch(fail); }

  // ---- header
  var paused = false, FIRST = { newest: "newest files", oldest: "oldest files", largest: "largest files", smallest: "smallest files" }; // upNext order = queueOrder
  function header(s) {
    $("stats").hidden = false; paused = s.paused;
    $("st-freed").textContent = ui.bytes(s.freedBytes); $("st-wait").textContent = ui.bytes(s.waitingBytes);
    $("st-work").textContent = s.paused ? "Paused" : s.workingFiles + (s.workingFiles === 1 ? " file" : " files");
    $("st-work-l").textContent = (s.paused ? "Nothing new starts" : "Working now") + " · " + ui.num(s.waitingFiles) + " waiting";
    var p = $("pause"); p.hidden = false; p.textContent = s.paused ? "Resume" : "Pause all";
    [["/activity/skipped/", "Skipped", s.counts.skipped], ["/activity/failed/", "Failed", s.counts.failed]].forEach(function (t) {
      var a = document.querySelector('.tabs a[href="' + t[0] + '"]'); if (a) a.textContent = t[1] + " (" + ui.num(t[2]) + ")";
    });
  }

  // ---- Queue
  function eta(sec) { return sec < 60 ? "less than a minute left" : "about " + Math.round(sec / 60) + " min left"; }
  T.queue = function () {
    return api.queue().then(function (q) {
      draw("queue", q, function () {
        pane.textContent = "";
        pane.appendChild(heading("Working now", "qh"));
        if (paused) { var n = el("div", "notice info"), d = el("div"); d.appendChild(el("strong", null, "Paused")); d.appendChild(document.createTextNode("Nothing new starts until you press Resume.")); n.appendChild(d); pane.appendChild(n); }
        else if (!q.working.length) pane.appendChild(empty("Nothing is being converted right now. Files waiting for their turn will be listed here."));
        q.working.forEach(function (j) {
          var c = el("div", "card"), top = el("div", "row between"), nm = base(j.path), conv = j.phase === "converting";
          top.appendChild(el("strong", null, nameOf(j))); top.appendChild(el("span", "badge info", conv ? "Converting" : "Checking the new file")); c.appendChild(top);
          var pr = el("div", "progress"), bar = el("span"); pr.setAttribute("role", "progressbar"); pr.setAttribute("aria-valuemin", 0); pr.setAttribute("aria-valuemax", 100);
          pr.setAttribute("aria-valuenow", j.percent); pr.setAttribute("aria-label", (conv ? "Converting " : "Checking ") + nm); bar.style.width = j.percent + "%"; pr.appendChild(bar); c.appendChild(pr);
          var line = el("p", "small muted", conv ? j.percent + "% · " + eta(j.etaSeconds) + " · " + ui.bytes(j.fromBytes) + " → expected about " + ui.bytes(j.expectedBytes)
            : "Playing the new file start to end before it replaces the original"); line.style.margin = "8px 0";
          c.appendChild(line);
          if (conv) { c.appendChild(btn("Stop this file", "stop-" + j.jobId, function () { act(api.stopJob({ id: j.jobId }), true); })); c.appendChild(el("span", "hint small", " It moves to Skipped, where you can convert it again.")); }
          pane.appendChild(c);
        });
        pane.appendChild(el("h2", null, "Up next"));
        var next = q.upNext.map(function (u) { return u.library + ": " + ui.count(u.count, "file", "files") + (u.count && u.order ? ", " + (FIRST[u.order] || u.order) + " first" : ""); });
        pane.appendChild(el("p", "muted", next.length ? next.join(". ") + "." : "Nothing is waiting."));
      });
    });
  };

  // ---- Skipped
  var KIND = { user_stopped: "info", restored: "info", hdr_dv: "warn", shared_file: "warn", cannot_keep_track: "warn", bitmap_subs: "warn", symlink: "warn", unreadable: "warn" };
  // Reasons a library rule causes, with the lowest control level whose library page can change that rule.
  var RULE = { already_efficient: 0, would_not_shrink: 0, hdr_dv: 1, cannot_keep_track: 1, setting_cannot_run: 1 }, RANK = { basic: 0, standard: 1, advanced: 2 }, level = null;
  var reason = "", shown = 0, tb, more, count;
  T.skipped = function () {
    if (!$("reason")) {
      pane.textContent = "";
      var h = heading("Skipped", "sh"); pane.appendChild(h); h.id = "sh";
      pane.appendChild(el("p", "muted", "Nothing was changed on these files. To change a rule, open the library."));
      var f = el("div", "field"), lb = el("label", null, "Show"), sel = el("select"); lb.htmlFor = "reason"; sel.id = "reason";
      sel.appendChild(el("option", null, "All reasons")).value = "";
      Object.keys(ui.REASONS).forEach(function (k) { sel.appendChild(el("option", null, cap(ui.REASONS[k]))).value = k; });
      sel.onchange = function () { reason = sel.value; shown = 0; seen.skipped = null; refresh(); };
      f.appendChild(lb); f.appendChild(sel); pane.appendChild(f);
      var t = table(["File", "Why it was skipped", ""]); tb = t; pane.appendChild(t.wrap);
      count = el("p", "hint"); pane.appendChild(count);
      more = btn("Show more", "more", function () { shown += 50; seen.skipped = null; refresh(); }, "btn"); more.hidden = true; pane.appendChild(more);
      pane.appendChild(el("div", null)).id = "empty-slot";
    }
    var want = Math.max(shown, 50);
    if (level === null) return api.getSettings().then(function (s) { level = RANK[s.controlLevel] || 0; }, function () { level = 0; }).then(T.skipped); // unknown level: Basic's links only
    return api.skipped({ reason: reason || null, limit: want, offset: 0 }).then(function (r) {
      shown = want;
      draw("skipped", r, function () {
        tb.body.textContent = "";
        r.items.forEach(function (i) {
          var why = el("div"), known = ui.REASONS[i.reason];
          why.appendChild(el("span", "badge " + (KIND[i.reason] || "idle"), cap(known || ui.OTHER_REASON))); why.appendChild(document.createElement("br")); why.appendChild(document.createTextNode(i.detail || "")); // the engine's detail; never the raw code
          var a = null;
          if (i.reason === "user_stopped" || i.reason === "restored") a = btn("Convert again", "again-" + i.id, function () { act(api.convertAgain({ id: i.id }), true); });
          else if (i.libraryId && RULE[i.reason] != null && level >= RULE[i.reason]) { a = el("a", "btn small", "Change rule"); a.href = "/library/?id=" + encodeURIComponent(i.libraryId); }
          tb.row([nameOf(i), why, a]);
        });
        tb.wrap.hidden = !r.items.length; more.hidden = r.items.length >= r.total;
        count.textContent = r.items.length ? "Showing " + ui.num(r.items.length) + " of " + ui.num(r.total) + "." : "";
        var slot = $("empty-slot"); slot.textContent = ""; if (!r.items.length) slot.appendChild(empty("Nothing has been skipped" + (reason ? " for this reason" : "") + "."));
        $("sh").textContent = "Skipped: " + ui.num(r.total) + (r.total === 1 ? " file" : " files") + (reason ? "" : " left alone on purpose");
      });
    });
  };

  // ---- Failed
  T.failed = function () {
    return api.failed().then(function (r) {
      draw("failed", r, function () {
        pane.textContent = "";
        pane.appendChild(heading("Failed: " + ui.num(r.total) + (r.total === 1 ? " file" : " files"), "fh"));
        if (!r.items.length) { pane.appendChild(empty("Nothing has failed.")); return; }
        var bad = r.items.filter(function (i) { return i.originalUntouched === false; }).length;
        var n = el("div", "notice " + (bad ? "warn" : "ok")), d = el("div");
        if (bad) { d.appendChild(el("strong", null, bad + (bad === 1 ? " original may have changed" : " originals may have changed"))); d.appendChild(document.createTextNode("Those rows say so. Check them before trying again. For the others, the original was not touched.")); }
        else { d.appendChild(el("strong", null, "Your original files were not touched")); d.appendChild(document.createTextNode("A file only replaces the original after it passes every check.")); }
        n.appendChild(d); pane.appendChild(n);
        var t = table(["File", "What went wrong", ""]);
        r.items.forEach(function (i) {
          var why = el("div"); why.appendChild(el("span", "badge bad", "Failed")); why.appendChild(document.createTextNode(" ")); why.appendChild(el("strong", null, i.title)); why.appendChild(document.createElement("br")); why.appendChild(document.createTextNode(i.detail));
          if (i.originalUntouched === false) { why.appendChild(document.createElement("br")); why.appendChild(el("strong", null, "Check this file before trying again: the original may have changed.")); }
          t.row([nameOf(i), why, btn("Retry", "retry-" + i.jobId, function () { act(api.retryJob({ id: i.jobId }), true); })]);
        });
        pane.appendChild(t.wrap);
      });
    });
  };

  // ---- Done
  T.done = function () {
    return api.done().then(function (r) {
      draw("done", r, function () {
        pane.textContent = ""; pane.appendChild(heading("Done", "dh"));
        if (!r.items.length) { pane.appendChild(empty("Converted files will be listed here.")); return; }
        var t = table(["File", "Before", "After", "Original"]);
        r.items.forEach(function (i) {
          var o = el("span");
          // What happened to the original, from originalState (contract, slice 18); the column header says "Original".
          if (i.originalState === "kept" && i.keptId) { o.appendChild(document.createTextNode("Kept " + i.keptDaysLeft + (i.keptDaysLeft === 1 ? " more day" : " more days") + " · ")); var a = el("a", null, "Get original back"); a.href = "/activity/kept/?confirm=" + encodeURIComponent(i.keptId); o.appendChild(a); }
          else o.textContent = { kept: "Kept beside the new file", restored: "Back in place", deleted: "Deleted" }[i.originalState] || ""; // an unknown state: say nothing rather than guess
          t.row([nameOf(i), ui.bytes(i.beforeBytes), ui.bytes(i.afterBytes), o]);
        });
        pane.appendChild(t.wrap);
      });
    });
  };

  // ---- Kept originals, with the one confirm panel
  var panel, openFor = null, restoredNote = "";
  function closePanel(back) { panel.hidden = true; openFor = null; if (back) { var b = pane.querySelector('[data-key="restore-' + back + '"]'); if (b) b.focus(); } }
  function openPanel(i) {
    openFor = i.keptId; panel.textContent = "";
    var h = el("h3", null, "Get the original of “" + base(i.path) + "” back?"); h.tabIndex = -1; h.setAttribute("data-key", "panel-h"); panel.appendChild(h);
    // Restore deletes the smaller file: there is no switching back (design, mockups/restore.html).
    var say = el("p", null, "The original (" + ui.bytes(i.originalBytes) + ") goes back in place and the smaller version is deleted. This file will not be converted again unless you press Convert again, under "), sk = el("a", null, "Activity > Skipped");
    sk.href = "/activity/skipped/"; say.appendChild(sk); say.appendChild(document.createTextNode(".")); panel.appendChild(say);
    var row = el("div", "row");
    row.appendChild(btn("Get original back", "confirm", function () { act(api.restoreKept({ id: i.keptId }).then(function () { closePanel(); restoredNote = "Original of " + base(i.path) + " is back in place."; seen.kept = null; }), true); }, "btn primary"));
    row.appendChild(btn("Cancel", "cancel", function () { closePanel(i.keptId); }, "btn")); panel.appendChild(row);
    panel.hidden = false; panel.scrollIntoView({ block: "nearest" }); h.focus();
  }
  T.kept = function () {
    if (!panel) { panel = el("div", "card"); panel.id = "confirm"; panel.hidden = true; panel.setAttribute("role", "group"); panel.setAttribute("aria-label", "Confirm getting the original back"); }
    return api.kept().then(function (r) {
      draw("kept", r, function () {
        pane.textContent = ""; pane.appendChild(heading("Kept originals", "kh"));
        var p = el("p", "muted"); p.appendChild(document.createTextNode("After a file is converted, the original is kept for " + ui.count(r.days, "day", "days") + " in a folder called .syp-trash next to it, so you can get it back. Change this in "));
        var a = el("a", null, "Settings → Safety"); a.href = "/settings/safety/"; p.appendChild(a); p.appendChild(document.createTextNode(".")); pane.appendChild(p);
        pane.appendChild(el("p", "muted", restoredNote)).id = "restored"; $("restored").setAttribute("role", "status");
        if (!r.items.length) { pane.appendChild(empty("No kept originals yet. Originals appear here after the first file is converted.")); pane.appendChild(panel); return; }
        var s = el("p"); s.appendChild(el("strong", null, ui.bytes(r.keptBytes))); s.appendChild(document.createTextNode(" of originals kept for " + ui.num(r.count) + (r.count === 1 ? " file." : " files."))); pane.appendChild(s);
        var t = table(["File", "Original", "Now", "Deleted on", ""]);
        r.items.forEach(function (i) {
          t.row([nameOf(i), ui.bytes(i.originalBytes), ui.bytes(i.nowBytes), new Date(i.deleteOn).toLocaleDateString([], { month: "short", day: "numeric" }), btn("Get original back", "restore-" + i.keptId, function () { openPanel(i); })]);
        });
        pane.appendChild(t.wrap); pane.appendChild(panel);
        if (confirmId) { var hit = r.items.filter(function (x) { return x.keptId === confirmId; })[0]; confirmId = null; if (hit) openPanel(hit); }
      });
    });
  };

  function refresh() {
    return Promise.all([api.activitySummary(), T[tab.toLowerCase().split(" ")[0]]()]).then(function (r) { clear(); header(r[0]); }, fail);
  }

  function init() {
    confirmId = new URLSearchParams(location.search).get("confirm");
    $("pause").onclick = function () { act(paused ? api.resume() : api.pause()); };
    refresh().then(function () {
      var busy = false;
      setInterval(function () { // one refresh at a time; a failed poll leaves the last good data on screen
        if (document.hidden || busy) return;
        busy = true; refresh().then(function () { busy = false; }, function () { busy = false; });
      }, 3000);
    });
  }
  S.ready.then(init);
})();
