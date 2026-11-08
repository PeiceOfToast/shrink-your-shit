/* Small shared helpers and the Preview card. Used by the first-run wizard now and by the
   library pages later, so the estimate looks and reads the same everywhere. */
(function (g) {
  "use strict";
  var ui = {};
  ui.QUALITY = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];
  // Skipped reasons are an open list: anything not named here is shown from its own text.
  ui.REASONS = {
    already_efficient: "already efficient", already_hevc: "already in the new format",
    hdr_dv: "HDR or Dolby Vision", bitmap_subs: "picture subtitles that cannot be kept", shared_file: "shared with another folder",
    // symlink: only links pointing outside the library (workshop, slice 23).
    symlink: "a link to a file outside this library", unreadable: "unreadable", cannot_keep_track: "a track that cannot be kept",
    user_stopped: "you stopped it", restored: "you restored the original",
    // Workshop slice 19 (contract: four more skipped reason codes); design's words, 2026-11-07.
    format_not_converted: "a format this version doesn't convert", no_video: "no video in the file",
    setting_cannot_run: "the library's setting can't be used on it",
    // Workshop slice 23 (file guards); design's words, 13:00Z.
    still_changing: "still changing, we'll look again on the next scan"
  };
  // A badge is the reason with a capital letter, unless design gave it a shorter name.
  ui.BADGES = { still_changing: "Still changing", symlink: "Link to outside this library" };
  ui.badge = function (code) { var t = ui.BADGES[code] || ui.REASONS[code] || ui.OTHER_REASON; return t.charAt(0).toUpperCase() + t.slice(1); };
  ui.OTHER_REASON = "another reason"; // a code this version doesn't know: never show the raw code
  ui.reason = function (code, detail) { return ui.REASONS[code] || detail || ui.OTHER_REASON; };

  // Sizes: MB below 1 GB, GB below 1 TB, one decimal. A range in one unit says it once; one that
  // crosses units says both ("800.0 MB - 1.2 GB").
  function unit(n) { return n >= 1e12 ? [1e12, "TB"] : n >= 1e9 ? [1e9, "GB"] : [1e6, "MB"]; }
  function num1(n, u) { return (n / u[0]).toFixed(1); }
  ui.bytes = function (n) { var u = unit(n); return num1(n, u) + " " + u[1]; };
  ui.range = function (lo, hi) {
    var a = unit(lo), b = unit(hi);
    return a[1] === b[1] ? num1(lo, b) + " - " + num1(hi, b) + " " + b[1] : ui.bytes(lo) + " - " + ui.bytes(hi);
  };
  ui.num = function (n) { return Number(n).toLocaleString("en-US"); };
  // "1 video", "2 videos": a count with its noun in the right number.
  ui.count = function (n, one, many) { return ui.num(n) + " " + (Number(n) === 1 ? one : many); };
  ui.el = function (tag, cls, txt) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (txt != null) e.textContent = txt;
    return e;
  };

  // The server's warnings about a setting (contract, slice 25a): one warn notice directly above the row holding
  // the Save button, one sentence per line, announced when it appears. The UI never has its own copy of the text.
  ui.warnings = function (saveBtn) {
    var n = ui.el("div", "notice warn"), row = saveBtn.parentNode; n.setAttribute("aria-live", "polite"); n.hidden = true;
    row.parentNode.insertBefore(n, row);
    return { show: function (list) {
      n.textContent = ""; (list || []).forEach(function (w) { n.appendChild(ui.el("p", null, w)).style.margin = "0"; });
      n.hidden = !(list && list.length);
    } };
  };

  // Preview card body: host is the element (give it aria-live="polite"; the caller adds the heading).
  // start() asks for an estimate; mark() says it is out of date. Always a range, never one figure.
  // extra (optional): {sample: () => path of the sample file or null, onSample: (sample or null) => ...}.
  ui.preview = function (host, libId, getSetting, extra) {
    extra = extra || {};
    var timer, poll, gen = 0;
    function say(rows) {
      host.textContent = "";
      rows.forEach(function (r) { host.appendChild(r); });
    }
    function note(t) { return ui.el("p", "muted", t); }
    function show(r) {
      var skipped = 0, parts = [], k;
      for (k in r.skipped || {}) { skipped += r.skipped[k]; parts.push(ui.num(r.skipped[k]) + " " + ui.reason(k)); }
      var big = ui.el("p"); big.appendChild(ui.el("span", "stat", ui.range(r.estimateBytesLow, r.estimateBytesHigh)));
      big.appendChild(document.createElement("br"));
      big.appendChild(ui.el("span", "stat-label", "possible saving, an estimate from file sizes"));
      var ul = ui.el("ul");
      ul.appendChild(ui.el("li", null, ui.count(r.files, "video", "videos") + " found (" + ui.bytes(r.totalBytes) + ")"));
      // Before slice 12 shared (hardlinked) files are converted but free nothing (contract addendum 12).
      var sh = r.sharedNoSaving || 0;
      ul.appendChild(ui.el("li", null, sh ? ui.num(r.canShrink) + " will be converted; " + ui.num(sh) + " of them " + (sh === 1 ? "is" : "are") + " shared with another folder and will free no space"
        : ui.num(r.canShrink) + " can be shrunk"));
      if (skipped) ul.appendChild(ui.el("li", null, ui.num(skipped) + " will be skipped: " + parts.join(", ")));
      say([big, ul, ui.el("p", "hint", "This is a guess, not a promise. Real savings are measured on each file.")]);
    }
    // Only a real failure says this (FIRST-RUN.md "States and errors").
    function giveUp() { say([note("We couldn't estimate this library. You can still start.")]); }
    // While the server is still working: a progress bar, its text (not read out on every poll), a line
    // for screen readers at most every 10%, and after two minutes a note that starting now is fine.
    // Polling never gives up while the answer is "computing" (contract, workshop addition for slice 19).
    function waiting(w, pr, late) {
      if (!w.box) {
        w.box = ui.el("div"); w.text = ui.el("p", "muted", "Working out an estimate..."); w.text.setAttribute("aria-hidden", "true");
        w.bar = ui.el("div", "progress"); w.fill = ui.el("span"); w.bar.appendChild(w.fill); w.bar.hidden = true;
        w.bar.setAttribute("role", "progressbar"); w.bar.setAttribute("aria-valuemin", 0); w.bar.setAttribute("aria-valuemax", 100);
        w.bar.setAttribute("aria-label", "Estimate progress"); w.sr = ui.el("p", "sr", "Working out an estimate.");
        w.late = ui.el("p", null, "This is taking a while. You can start now; the estimate will appear here when it's ready."); w.late.hidden = true;
        w.box.appendChild(w.text); w.box.appendChild(w.bar); w.box.appendChild(w.sr); w.box.appendChild(w.late); say([w.box]); w.step = -1;
      }
      if (pr && pr.total > 0) {
        var pct = Math.min(100, Math.floor(pr.done * 100 / pr.total));
        w.text.textContent = "Checking " + ui.num(pr.done) + " of " + ui.count(pr.total, "file", "files") + "...";
        w.bar.hidden = false; w.bar.setAttribute("aria-valuenow", pct); w.fill.style.width = pct + "%";
        if (Math.floor(pct / 10) !== w.step) { w.step = Math.floor(pct / 10); w.sr.textContent = "Checking files, " + w.step * 10 + "% done."; }
      }
      if (late) w.late.hidden = false;
    }
    function ask() {
      var my = ++gen, t0 = Date.now(), w = {};
      say([note("Working out an estimate...")]);
      (function once() {
        g.SYP.api.preview({ id: libId }, { setting: getSetting(), sampleFile: extra.sample ? extra.sample() : null }).then(function (r) {
          if (my !== gen) return;
          if (r.status === "ready") { show(r); if (extra.onSample) extra.onSample(r.sample || null); if (extra.onWarnings) extra.onWarnings(r.warnings); return; }
          if (r.status === "failed") return giveUp();
          var t = Date.now() - t0;
          waiting(w, r.progress, t >= 120000);
          poll = setTimeout(once, t < 60000 ? 2000 : 5000); // every 2 s for the first minute, then every 5 s
        }, function (e) {
          if (my !== gen) return;
          // The setting itself can't run yet (slice 25b: "Keep only my languages" with no languages chosen): the
          // server says why, in words for the person; anything else is a real failure.
          if (e && (e.code === "languages_needed" || e.code === "not_available") && e.message) say([note(e.message)]); else giveUp();
        });
      })();
    }
    return {
      start: ask,
      // The setting changed: say so now, and refresh shortly after the last change.
      mark: function () {
        clearTimeout(timer); clearTimeout(poll); gen++;
        say([note("Your choice changed. Updating the estimate...")]); if (extra.onSample) extra.onSample(null);
        timer = setTimeout(ask, 500);
      },
      stop: function () { clearTimeout(timer); clearTimeout(poll); gen++; }
    };
  };
  g.SYP = g.SYP || {};
  g.SYP.ui = ui;
})(window);
