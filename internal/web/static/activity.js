/* Activity: the header (savings; Start/Stop is on the Workers page), the tab counts and the five tabs. Polls every
   few seconds; a refresh only redraws what changed and puts focus back where it was. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  var pane = document.getElementById("pane"), tab = pane.getAttribute("data-tab"), seen = {}, T = {}, confirmId = null;
  function $(id) { return document.getElementById(id); }
  function base(p) { return p.split(/[\\/]/).pop(); }
  function nameOf(i) { return i.library + " / " + base(i.path); }
  // A file renamed by a new file type (25a-2, newPath): the row's name, and the other name in a muted line under it
  // ("was movie.mp4" on Done, "now movie.mkv" on Kept originals; design 18:25Z).
  function named(i, name, other, word) {
    var d = el("div", null, i.library + " / " + base(name));
    if (other && base(other) !== base(name)) d.appendChild(el("div", "muted small", word + " " + base(other)));
    return d;
  }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function clear() { $("err").hidden = true; }

  // Draw only when the data changed, and give focus back to the same control (data-key) afterwards,
  // or to the same heading (id), such as "Working now" after the last Stop, so a redraw does not drop it.
  function draw(key, data, fn) {
    var j = JSON.stringify(data); if (seen[key] === j) return; seen[key] = j;
    var a = document.activeElement, k = a && a.getAttribute && a.getAttribute("data-key"), id = !k && a && a.id && pane.contains(a) ? a.id : "";
    fn();
    var n = k ? pane.querySelector('[data-key="' + k + '"]') : id ? $(id) : null;
    if (n) n.focus();
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
    [["/activity/skipped/", "Skipped", s.counts.skipped], ["/activity/failed/", "Failed", s.counts.failed]].forEach(function (t) {
      var a = document.querySelector('.tabs a[href="' + t[0] + '"]'); if (a) a.textContent = t[1] + " (" + ui.num(t[2]) + ")";
    });
  }

  // ---- Queue
  function eta(sec) { return sec < 60 ? "less than a minute left" : "about " + Math.round(sec / 60) + " min left"; }
  // A converting file's line. The engine doesn't always know the time left or the new size (null at the start of
  // a conversion): those parts are left out, never shown as a guess.
  function working(j) {
    var parts = [j.percent + "%"];
    if (j.etaSeconds != null) parts.push(eta(j.etaSeconds));
    parts.push(ui.bytes(j.fromBytes) + (j.expectedBytes != null ? " → expected about " + ui.bytes(j.expectedBytes) : ""));
    return parts.join(" · ");
  }
  // Stop on a running file can answer 202 "stopping": the worker stops it within moments (contract, slice 24).
  // Until it leaves the queue its row says "Stopping…" and its Stop is aria-disabled (a real disabled button would
  // drop focus); "Stopping…" is announced once in a polite live region outside the redrawn pane. When it leaves,
  // focus goes to the next row's Stop, or to the Queue heading (design, 2026-11-07).
  var stopping = {}, live = null;
  function say(t) {
    if (!live) { live = el("p", "sr"); live.setAttribute("role", "status"); live.setAttribute("aria-live", "polite"); pane.parentNode.insertBefore(live, pane.nextSibling); }
    live.textContent = t;
  }
  function stop(j) {
    if (stopping[j.jobId]) return; // already asked: ignore further clicks
    api.stopJob({ id: j.jobId }).then(function (r) {
      if (r && r.status === "stopping") { stopping[j.jobId] = true; say("Stopping " + base(j.path) + "…"); seen.queue = null; return refresh(); }
      return refresh().then(after);
    }).catch(fail);
  }
  T.queue = function () {
    return api.queue().then(function (q) {
      var gone = Object.keys(stopping).filter(function (id) { return !q.working.some(function (j) { return j.jobId === id; }); });
      gone.forEach(function (id) { delete stopping[id]; });
      var wasOnStop = gone.some(function (id) { var a = document.activeElement; return a && a.getAttribute && a.getAttribute("data-key") === "stop-" + id; });
      draw("queue", q, function () {
        pane.textContent = "";
        pane.appendChild(heading("Working now", "qh"));
        if (!q.working.length) pane.appendChild(empty("Nothing is being converted right now. Files waiting for their turn will be listed here."));
        q.working.forEach(function (j) {
          var c = el("div", "card"), top = el("div", "row between"), nm = base(j.path), conv = j.phase === "converting";
          top.appendChild(el("strong", null, nameOf(j))); top.appendChild(el("span", "badge info", conv ? "Converting" : "Checking the new file")); c.appendChild(top);
          var pr = el("div", "progress"), bar = el("span"); pr.setAttribute("role", "progressbar"); pr.setAttribute("aria-valuemin", 0); pr.setAttribute("aria-valuemax", 100);
          pr.setAttribute("aria-valuenow", j.percent); pr.setAttribute("aria-label", (conv ? "Converting " : "Checking ") + nm); bar.style.width = j.percent + "%"; pr.appendChild(bar); c.appendChild(pr);
          var line = el("p", "small muted", conv ? working(j)
            : "Playing the new file start to end before it replaces the original"); line.style.margin = "8px 0";
          c.appendChild(line);
          if (stopping[j.jobId]) line.textContent = "Stopping…";
          if (conv) { var sb = btn("Stop this file", "stop-" + j.jobId, function () { stop(j); }); if (stopping[j.jobId]) sb.setAttribute("aria-disabled", "true"); c.appendChild(sb); c.appendChild(el("span", "hint small", " It moves to Skipped, where you can convert it again.")); }
          pane.appendChild(c);
        });
        pane.appendChild(el("h2", null, "Up next"));
        var next = q.upNext.map(function (u) { return u.library + ": " + ui.count(u.count, "file", "files") + (u.count && u.order ? ", " + (FIRST[u.order] || u.order) + " first" : ""); });
        pane.appendChild(el("p", "muted", next.length ? next.join(". ") + "." : "Nothing is waiting."));
      });
      if (wasOnStop) { var nx = pane.querySelector('[data-key^="stop-"]:not([aria-disabled="true"])'); (nx || $("qh")).focus(); }
    });
  };

  // ---- Skipped
  // Badge style per reason: info for what resolves itself or was the person's own doing, warn for a risk, idle otherwise.
  var KIND = { user_stopped: "info", restored: "info", still_changing: "info", hdr_dv: "warn", shared_file: "warn", cannot_keep_track: "warn", bitmap_subs: "warn", symlink: "idle", unreadable: "warn", dv: "info", dv_profile5: "idle", already_in_format: "idle" };
  // Reasons a flow's rule causes that a Standard flow can change (piece B: each flow has its own kind); "Change rule"
  // opens that flow's page. flowOf maps a library id to its flow.
  var RULE = { hdr_dv: 1, cannot_keep_track: 1, setting_cannot_run: 1, dv: 1 }, flowOf = null;
  var reason = "", shown = 0, tb, more, count;
  T.skipped = function () {
    if (!$("reason")) {
      pane.textContent = "";
      var h = heading("Skipped", "sh"); pane.appendChild(h); h.id = "sh";
      pane.appendChild(el("p", "muted", "Nothing was changed on these files. To change a rule, open the library's flow."));
      var f = el("div", "field"), lb = el("label", null, "Show"), sel = el("select"); lb.htmlFor = "reason"; sel.id = "reason";
      sel.appendChild(el("option", null, "All reasons")).value = "";
      // One entry per label (already_hevc and already_in_format share one, design); a shared entry sends its codes in one
      // request, reason=a,b, which the server pages as one list (UI-API-CONTRACT.md 22:30Z).
      var byLabel = {}, order = [];
      Object.keys(ui.REASONS).forEach(function (k) { var t = ui.badge(k); if (!byLabel[t]) { byLabel[t] = []; order.push(t); } byLabel[t].push(k); });
      order.forEach(function (t) { sel.appendChild(el("option", null, t)).value = byLabel[t].join(","); });
      sel.onchange = function () { reason = sel.value; shown = 0; seen.skipped = null; refresh(); };
      f.appendChild(lb); f.appendChild(sel); pane.appendChild(f);
      var t = table(["File", "Why it was skipped", ""]); tb = t; pane.appendChild(t.wrap);
      count = el("p", "hint"); pane.appendChild(count);
      more = btn("Show more", "more", function () { shown += 50; seen.skipped = null; refresh(); }, "btn"); more.hidden = true; pane.appendChild(more);
      pane.appendChild(el("div", null)).id = "empty-slot";
    }
    var want = Math.max(shown, 50);
    if (flowOf === null) return Promise.all([api.listLibraries(), api.listFlows()]).then(function (r) {
      var byId = {}; flowOf = {}; r[1].items.forEach(function (f) { byId[f.id] = f; }); r[0].items.forEach(function (l) { if (byId[l.flowId]) flowOf[l.id] = byId[l.flowId]; });
    }, function () { flowOf = {}; }).then(T.skipped); // not known: no Change rule links
    return api.skipped({ reason: reason || null, limit: want, offset: 0 }).then(function (r) {
      shown = want;
      draw("skipped", r, function () {
        tb.body.textContent = "";
        r.items.forEach(function (i) {
          var why = el("div");
          why.appendChild(el("span", "badge " + (KIND[i.reason] || "idle"), ui.badge(i.reason))); why.appendChild(document.createElement("br")); why.appendChild(document.createTextNode(i.detail || "")); // the engine's detail; never the raw code
          var a = null;
          if (i.reason === "user_stopped" || i.reason === "restored") a = btn("Convert again", "again-" + i.id, function () { act(api.convertAgain({ id: i.id }), true); });
          else if (i.libraryId && RULE[i.reason] != null && flowOf[i.libraryId] && flowOf[i.libraryId].kind === "standard") { a = el("a", "btn small", "Change rule"); a.href = "/flow/?id=" + encodeURIComponent(flowOf[i.libraryId].id); }
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
          t.row([named(i, i.newPath || i.path, i.newPath && i.path, "was"), ui.bytes(i.beforeBytes), ui.bytes(i.afterBytes), o]);
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
    var to = i.newPath && base(i.newPath) !== base(i.path) ? base(i.newPath) : "";
    var say = el("p", null, (to ? "The original (" + base(i.path) + ", " + ui.bytes(i.originalBytes) + ") goes back in place and the smaller version, " + to + ", is deleted."
      : "The original (" + ui.bytes(i.originalBytes) + ") goes back in place and the smaller version is deleted.") + " This file will not be converted again unless you press Convert again, under "), sk = el("a", null, "Activity > Skipped");
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
          t.row([named(i, i.path, i.newPath, "now"), ui.bytes(i.originalBytes), ui.bytes(i.nowBytes), new Date(i.deleteOn).toLocaleDateString([], { month: "short", day: "numeric" }), btn("Get original back", "restore-" + i.keptId, function () { openPanel(i); })]);
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
