/* Activity (redesign C): one Files view. The two measured numbers; five filter cards (Unprocessed, Processing,
   Processed, Skipped, Failed) with a Library choice and Search, all kept in the address; one table with the shared
   columns, the card's own columns and a Log button on every row; the Log panel. Data: GET /api/activity/files and
   GET /api/activity/files/{id}/log (workshop S5, UI-API-CONTRACT.md 21:00Z). Polls; a redraw keeps focus. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el;
  var pane = document.getElementById("pane"), seen = "", busy = false, panel = null, flowOf = null, summary = {};
  var CARDS = [["unprocessed", "Unprocessed"], ["processing", "Processing"], ["processed", "Processed"], ["skipped", "Skipped"], ["failed", "Failed"]];
  // The old tab addresses open their card.
  var FROM_TAB = { Queue: null, Skipped: "skipped", Failed: "failed", Done: "processed", "Kept originals": "processed" };
  var KIND = { user_stopped: "info", restored: "info", still_changing: "info", hdr_dv: "warn", shared_file: "warn", cannot_keep_track: "warn", bitmap_subs: "warn", symlink: "idle", unreadable: "warn" };
  var RULE = { hdr_dv: 1, cannot_keep_track: 1, setting_cannot_run: 1 }; // rules a Standard flow can change ("Change rule")
  var st = { show: null, library: "", q: "", kept: false, limit: 50 }, processing = {}, stopping = {};

  function $(id) { return document.getElementById(id); }
  function base(p) { return p.split(/[\\/]/).pop(); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function say(t) { $("said").textContent = ""; $("said").textContent = t; }
  function at(t) { return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }); }
  function length(sec) { var m = Math.round(sec / 60), h = Math.floor(m / 60); return h ? h + " h " + (m % 60) + " min" : m + " min"; }
  function btn(txt, key, fn, cls) { var b = el("button", cls || "btn small", txt); b.type = "button"; b.setAttribute("data-key", key); b.onclick = fn; return b; }

  // ---- the address keeps the state (?show=processing&library=1&q=alien&kept=1)
  function readUrl() {
    var u = new URLSearchParams(location.search);
    st.show = u.get("show") || FROM_TAB[pane.getAttribute("data-tab")] || null;
    st.library = u.get("library") || ""; st.q = u.get("q") || "";
    st.kept = u.get("kept") === "1" || pane.getAttribute("data-tab") === "Kept originals";
  }
  function writeUrl() {
    var u = new URLSearchParams(); u.set("show", st.show);
    if (st.library) u.set("library", st.library); if (st.q) u.set("q", st.q); if (st.kept && st.show === "processed") u.set("kept", "1");
    history.replaceState(null, "", "/activity/?" + u.toString());
  }
  function choose(show) { st.show = show; st.kept = false; st.limit = 50; seen = ""; writeUrl(); refresh(); }

  // ---- header and cards
  function header(s) {
    $("stats").hidden = false; $("st-freed").textContent = ui.bytes(s.freedBytes); $("st-wait").textContent = ui.bytes(s.waitingBytes);
  }
  function cards(counts) {
    var box = $("cards"); box.textContent = "";
    CARDS.forEach(function (c) {
      var b = el("button", "card selectable"), n = el("strong", "stat", ui.num(counts[c[0]] || 0)); b.type = "button"; b.setAttribute("data-key", "card-" + c[0]);
      b.setAttribute("aria-pressed", st.show === c[0] ? "true" : "false"); b.style.minWidth = "9rem"; b.style.textAlign = "left";
      b.appendChild(n); b.appendChild(el("span", "stat-label", c[1])); b.onclick = function () { choose(c[0]); }; box.appendChild(b);
    });
    var chip = $("chip"); chip.textContent = "";
    if (st.show === "processed" && counts.keptOriginals) { // the Kept originals filter, on the Processed card
      var k = el("button", "btn small", "Original still kept (" + ui.num(counts.keptOriginals) + ")"); k.type = "button"; k.setAttribute("data-key", "chip-kept");
      k.setAttribute("aria-pressed", st.kept ? "true" : "false"); k.onclick = function () { st.kept = !st.kept; st.limit = 50; seen = ""; writeUrl(); refresh(); }; chip.appendChild(k);
    }
  }

  // ---- actions
  function act(p, words) { return p.then(function () { $("err").hidden = true; if (words) say(words); seen = ""; return refresh(); }, fail); }
  function stop(f) {
    if (stopping[f.jobId]) return;
    api.stopJob({ id: f.jobId }).then(function (r) {
      if (r && r.status === "stopping") { stopping[f.jobId] = true; say("Stopping " + f.name + "…"); }
      seen = ""; return refresh();
    }).catch(fail);
  }
  // Get original back: the one confirm panel, under the table; its words are unchanged (25a-2 names both files).
  function confirmRestore(f) {
    if (!panel) { panel = el("div", "card"); panel.id = "confirm"; panel.setAttribute("role", "group"); panel.setAttribute("aria-label", "Confirm getting the original back"); }
    panel.textContent = ""; pane.appendChild(panel);
    var h = el("h3", null, "Get the original of “" + f.name + "” back?"); h.tabIndex = -1; panel.appendChild(h);
    var to = f.newPath && base(f.newPath) !== base(f.path) ? base(f.newPath) : "";
    panel.appendChild(el("p", null, (to ? "The original (" + f.name + ", " + ui.bytes(f.beforeBytes) + ") goes back in place and the smaller version, " + to + ", is deleted."
      : "The original (" + ui.bytes(f.beforeBytes) + ") goes back in place and the smaller version is deleted.") + " This file will not be converted again unless you press Convert again, under Skipped."));
    var row = el("div", "row");
    row.appendChild(btn("Get original back", "confirm", function () { panel.remove(); act(api.restoreKept({ id: f.keptId }), "Original of " + f.name + " is back in place."); }, "btn primary"));
    row.appendChild(btn("Cancel", "cancel", function () { panel.remove(); var b = pane.querySelector('[data-key="restore-' + f.keptId + '"]'); if (b) b.focus(); }));
    panel.appendChild(row); panel.hidden = false; panel.scrollIntoView({ block: "nearest" }); h.focus();
  }

  // ---- the table
  var EXTRA = { unprocessed: ["Status"], processing: ["Progress", "ETA"], processed: ["Before → after", "Flow", "Finished", "Original"], skipped: ["Reason"], failed: ["What went wrong"] };
  function nameCell(f) {
    var d = el("div", null, f.newPath ? base(f.newPath) : f.name);
    if (f.newPath && base(f.newPath) !== f.name) d.appendChild(el("div", "muted small", "was " + f.name)); // renamed by a new file type (25a-2)
    return d;
  }
  function folderCell(f) { var d = el("div", "mono", f.folder); if (!st.library) d.appendChild(el("div", "muted small", f.library)); return d; }
  function extra(f) {
    var s = st.show;
    if (s === "unprocessed") return [f.status === "retrying" ? "Trying again at " + at(f.retryAt) + " (tried " + ui.count(f.attempts, "time", "times") + " so far). The original is untouched." : "Waiting"];
    if (s === "processing") {
      var pr = el("div", "progress"), bar = el("span"); pr.setAttribute("role", "progressbar"); pr.setAttribute("aria-valuemin", 0); pr.setAttribute("aria-valuemax", 100);
      pr.setAttribute("aria-valuenow", f.percent); pr.setAttribute("aria-label", f.name); bar.style.width = f.percent + "%"; pr.appendChild(bar);
      var c = el("div"); c.appendChild(pr); c.appendChild(el("span", "small", f.percent + "%"));
      return [c, f.etaSeconds != null ? Math.max(1, Math.round(f.etaSeconds / 60)) + " min left" : ""];
    }
    if (s === "processed") {
      var pct = Math.round(Math.abs(1 - f.afterBytes / f.beforeBytes) * 100), grew = f.afterBytes > f.beforeBytes; // growth is plain text, not an error
      var flow = f.flow ? el("a", null, f.flow.name) : ""; if (flow) flow.href = "/flow/?id=" + encodeURIComponent(f.flow.id);
      var o = el("span");
      if (f.originalState === "kept" && f.keptId) { o.appendChild(document.createTextNode("Kept " + f.keptDaysLeft + (f.keptDaysLeft === 1 ? " more day" : " more days") + " · ")); o.appendChild(btn("Get original back", "restore-" + f.keptId, function () { confirmRestore(f); }, "btn small")); }
      else o.textContent = { kept_beside: "Kept beside the new file", restored: "Back in place", deleted: "Deleted", left_in_place: "Left in place" + (f.outputDir ? " · new file in " + f.outputDir : "") }[f.originalState] || "";
      return [ui.bytes(f.beforeBytes) + " → " + ui.bytes(f.afterBytes) + ", " + pct + "% " + (grew ? "bigger" : "smaller"), flow, at(f.finishedAt), o];
    }
    if (s === "skipped") { var w = el("div"); w.appendChild(el("span", "badge " + (KIND[f.reason] || "idle"), ui.badge(f.reason))); w.appendChild(document.createElement("br")); w.appendChild(document.createTextNode(f.detail || "")); return [w]; }
    var wr = el("div"); wr.appendChild(el("strong", null, f.title)); wr.appendChild(document.createElement("br")); wr.appendChild(document.createTextNode(f.detail || ""));
    if (f.attempts > 1) { wr.appendChild(document.createElement("br")); wr.appendChild(el("span", "muted small", "Tried " + f.attempts + " times")); }
    return [wr];
  }
  function actions(f) {
    var d = el("div", "row"), s = st.show;
    if (s === "processing") { var sb = btn("Stop this file", "stop-" + f.jobId, function () { stop(f); }); if (stopping[f.jobId]) sb.setAttribute("aria-disabled", "true"); d.appendChild(sb); }
    if (s === "skipped") {
      if (f.reason === "user_stopped" || f.reason === "restored") d.appendChild(btn("Convert again", "again-" + f.skippedId, function () { act(api.convertAgain({ id: f.skippedId }), "Queued again."); }));
      else if (RULE[f.reason] && flowOf[f.libraryId] && flowOf[f.libraryId].kind === "standard") { var a = el("a", "btn small", "Change rule"); a.href = "/flow/?id=" + encodeURIComponent(flowOf[f.libraryId].id); d.appendChild(a); }
    }
    if (s === "failed") d.appendChild(btn("Retry", "retry-" + f.jobId, function () { act(api.retryJob({ id: f.jobId }), "Queued to try again."); }));
    d.appendChild(btn("Log", "log-" + f.id, function () { openLog(f); }));
    return d;
  }
  var EMPTY = { unprocessed: function () { return "Nothing waiting. New videos are found at the next scan" + (summary.nextScan ? " (" + at(summary.nextScan) + ")" : "") + "."; },
    processed: "Nothing converted yet.", skipped: "No files were left alone.", failed: "Nothing failed." };
  function draw(r) {
    var j = JSON.stringify([r, st, stopping]); if (j === seen) return; seen = j;
    var a = document.activeElement, k = a && a.getAttribute && a.getAttribute("data-key");
    cards(r.counts); pane.textContent = "";
    if (!r.items.length) {
      var e = el("div", "card empty"), p = el("p");
      if (st.show === "processing") {
        if (summary.workersRunning === false) { p.appendChild(document.createTextNode("Nothing is converting, because no worker is running. ")); var g = el("a", null, "Go to Workers"); g.href = "/workers/"; p.appendChild(g); }
        else p.textContent = "Nothing is converting right now.";
      } else p.textContent = typeof EMPTY[st.show] === "function" ? EMPTY[st.show]() : EMPTY[st.show];
      if (st.q || st.library || st.kept) p.textContent = "No files match."; // a filter, not an empty card
      e.appendChild(p); pane.appendChild(e);
    } else {
      var cols = ["Folder", "File name", "Codec", "Resolution", "Size", "Length"].concat(EXTRA[st.show], [""]);
      var w = el("div", "table-wrap"), t = el("table", "files"), hr = el("tr"), tb = el("tbody");
      cols.forEach(function (c) { var th = el("th"); th.scope = "col"; if (c) th.textContent = c; else th.appendChild(el("span", "sr", "Actions")); hr.appendChild(th); });
      var hd = el("thead"); hd.appendChild(hr); t.appendChild(hd); t.appendChild(tb); w.appendChild(t); pane.appendChild(w);
      r.items.forEach(function (f) {
        var tr = el("tr");
        [folderCell(f), nameCell(f), f.codec || "", f.resolution || "", ui.bytes(f.bytes), f.durationSec != null ? length(f.durationSec) : ""].concat(extra(f), [actions(f)]).forEach(function (c, i) {
          var td = el("td"); td.setAttribute("data-label", cols[i]); // the label a narrow screen shows before each value
          if (typeof c === "string") td.textContent = c; else if (c) td.appendChild(c); tr.appendChild(td);
        });
        tb.appendChild(tr);
        if (st.show === "processing") { // the live values, in a second line under the row
          var t2 = el("tr"), td2 = el("td", "small muted", [["Converting to", f.convertingTo], ["Encoder", f.encoder], ["Decoder", f.decoder], ["Bitrate", f.bitrateKbps ? (f.bitrateKbps / 1000).toFixed(1) + " Mbps" : null], ["FPS", f.fps], ["Worker", f.worker]]
            .filter(function (x) { return x[1] != null && x[1] !== ""; }).map(function (x) { return x[0] + " " + x[1]; }).join(" · "));
          td2.colSpan = cols.length; t2.appendChild(td2); tb.appendChild(t2);
        }
      });
      if (r.items.length < r.total) pane.appendChild(btn("Show more", "more", function () { st.limit += 50; seen = ""; refresh(); }, "btn"));
    }
    var n = k ? document.querySelector('[data-key="' + k + '"]') : null; if (n) n.focus();
  }

  // When a file leaves Processing, one polite message: "Alien (1979).mkv finished: 40% smaller."
  function announceFinished(r) {
    if (st.show !== "processing") { processing = {}; return; }
    var now = {}; r.items.forEach(function (f) { now[f.jobId] = f.name; });
    Object.keys(processing).forEach(function (id) {
      if (now[id]) return; delete stopping[id];
      api.activityFiles({ show: "processed", limit: 10 }).then(function (p) {
        var d = p.items.filter(function (x) { return String(x.jobId) === String(id); })[0];
        if (d) say(d.name + " finished: " + Math.round(Math.abs(1 - d.afterBytes / d.beforeBytes) * 100) + "% " + (d.afterBytes > d.beforeBytes ? "bigger." : "smaller."));
      }, function () {});
    });
    processing = now;
  }
  function refresh() {
    if (busy) return Promise.resolve(); busy = true;
    var q = { show: st.show, library: st.library || null, q: st.q || null, kept: st.kept && st.show === "processed" ? 1 : null, limit: st.limit, offset: 0 };
    return Promise.all([api.activitySummary(), api.activityFiles(q)]).then(function (r) {
      busy = false; summary = r[0]; header(r[0]); $("err").hidden = true;
      if (!st.show) { st.show = r[1].counts.unprocessed ? "unprocessed" : "processed"; writeUrl(); return refresh(); } // the default card
      announceFinished(r[1]); draw(r[1]);
    }, function (e) { busy = false; fail(e); });
  }

  // ---- the Log panel: a plain summary, then the full log as recorded; it follows live on a Processing row.
  // S5's limits (contract 23:07Z): a log comes in pages of up to 64 KiB, so a finished log is read page after page until
  // a page brings nothing; Copy and Download wait for that. A long log arrives already cut, with the server's own
  // "… N KiB left out …" line where it was cut. A job from before S5 has a summary and no text.
  var logFor = null, logFrom = 0, logTimer = null, following = true, logText = "", logLoading = Promise.resolve();
  function noFullLog(none) {
    var pre = $("log-text"); if (none) { pre.textContent = "No full log for this file."; pre.classList.add("muted"); } else pre.classList.remove("muted");
    ["log-copy", "log-dl"].forEach(function (k) { $(k).setAttribute("aria-disabled", none ? "true" : "false"); });
  }
  var STEP = { found: "Found", checked: "Checked", converted: "Converted", verified: "Checked the result", kept: "Kept", replaced: "Replaced" };
  function pullLog() {
    var f = logFor; if (!f) return Promise.resolve();
    return api.fileLog({ id: f.id, from: logFrom }).then(function (r) {
      if (logFor !== f) return; // the panel moved on to another file
      var ol = $("log-sum"); ol.textContent = "";
      (r.summary || []).forEach(function (s) { ol.appendChild(el("li", null, (STEP[s.step] || s.step) + " · " + at(s.at) + " · " + (s.ok ? "✓ " : "✗ ") + s.words)); });
      if (r.text) { logText += r.text; var pre = $("log-text"); noFullLog(false); pre.textContent = logText; if (following) pre.scrollTop = pre.scrollHeight; }
      logFrom = r.next; $("log-follow").hidden = !r.live;
      clearTimeout(logTimer);
      // A live log catches up at once while pages come full (the server's 64 KiB are bytes, so count bytes), then
      // looks again every 2 s.
      if (r.live) { if (r.text && new TextEncoder().encode(r.text).length >= 60000) return pullLog(); if (following) logTimer = setTimeout(function () { logLoading = pullLog(); }, 2000); return; }
      if (r.text) return pullLog(); // a finished log: the next page, until one brings nothing
      if (!logText) noFullLog(true); // nothing at all: a job from before the full logs
    }, function (e) { $("log-text").textContent = e && e.message || "This log can't be read."; });
  }
  function openLog(f) {
    logFor = f; logFrom = 0; logText = ""; following = true;
    $("log-h").textContent = "Log: " + (f.newPath ? base(f.newPath) : f.name); $("log-sum").textContent = ""; $("log-text").textContent = "";
    $("log-follow").textContent = "Stop following"; $("log-follow").setAttribute("aria-pressed", "true");
    noFullLog(false); $("log-said").textContent = ""; $("log").showModal(); $("log-close").focus(); logLoading = pullLog();
  }

  function init() {
    readUrl(); if (st.show) writeUrl(); // an old tab address becomes the Files view's own (?show=...)
    $("q").value = st.q;
    var t = null; $("q").addEventListener("input", function () { clearTimeout(t); t = setTimeout(function () { st.q = $("q").value.trim(); st.limit = 50; seen = ""; writeUrl(); refresh(); }, 300); });
    $("lib").onchange = function () { st.library = $("lib").value; st.limit = 50; seen = ""; writeUrl(); refresh(); };
    $("log-close").onclick = function () { $("log").close(); };
    // Escape closes it too. Focus returns to that row's Log button (found again: a poll may have redrawn the row).
    $("log").addEventListener("close", function () { clearTimeout(logTimer); var b = logFor && document.querySelector('[data-key="log-' + logFor.id + '"]'); logFor = null; if (b) b.focus(); });
    $("log-follow").onclick = function () { following = !following; this.textContent = following ? "Stop following" : "Follow"; this.setAttribute("aria-pressed", following ? "true" : "false"); if (following) logLoading = pullLog(); };
    // Copy and Download take the whole log: they wait until every page is in, and say so meanwhile.
    function whole(then) { var said = $("log-said"), waiting = true;
      setTimeout(function () { if (waiting) said.textContent = "Getting the whole log…"; }, 150);
      return logLoading.then(function () { waiting = false; return then(); }); }
    $("log-copy").onclick = function () { if (this.getAttribute("aria-disabled") === "true") return; whole(function () { return navigator.clipboard ? navigator.clipboard.writeText(logText) : Promise.reject(); }).then(function () { $("log-said").textContent = "Log copied."; }, function () { $("log-said").textContent = "Copying isn't possible here. Select the text instead."; }); };
    $("log-dl").onclick = function () { if (this.getAttribute("aria-disabled") === "true") return; var name = (logFor ? base(logFor.name) : "file") + ".log"; whole(function () { $("log-said").textContent = ""; var u = URL.createObjectURL(new Blob([logText], { type: "text/plain" })), a = el("a"); a.href = u; a.download = name; a.click(); URL.revokeObjectURL(u); }); };
    Promise.all([api.listLibraries(), api.listFlows()]).then(function (r) {
      var byId = {}; flowOf = {}; r[1].items.forEach(function (f) { byId[f.id] = f; });
      r[0].items.forEach(function (l) { if (byId[l.flowId]) flowOf[l.id] = byId[l.flowId]; var o = el("option", null, l.name); o.value = l.id; $("lib").appendChild(o); });
      $("lib").value = st.library;
    }, function () { flowOf = {}; }).then(function () {
      refresh();
      setInterval(function () { if (!document.hidden) refresh(); }, 2000); // Processing's live values; the others change slowly but cheaply
    });
  }
  S.ready.then(init);
})();
