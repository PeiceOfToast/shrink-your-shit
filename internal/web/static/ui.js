/* Small shared helpers and the Preview card. Used by the first-run wizard now and by the
   library pages later, so the estimate looks and reads the same everywhere. */
(function (g) {
  "use strict";
  var ui = {};
  ui.QUALITY = ["Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];
  // Skipped reasons are an open list: anything not named here is shown from its own text.
  ui.REASONS = {
    already_efficient: "already efficient", would_not_shrink: "would not get smaller", already_hevc: "already in the new format",
    hdr_dv: "HDR or Dolby Vision", bitmap_subs: "picture subtitles that cannot be kept", shared_file: "shared with another folder",
    symlink: "links to files elsewhere", unreadable: "unreadable", cannot_keep_track: "a track that cannot be kept",
    user_stopped: "you stopped it", restored: "you restored the original"
  };
  ui.reason = function (code, detail) { return ui.REASONS[code] || detail || String(code).replace(/_/g, " "); };

  function one(n, u) { return (n / u).toFixed(1); }
  ui.bytes = function (n) { return n >= 1e12 ? one(n, 1e12) + " TB" : one(n, 1e9) + " GB"; };
  ui.range = function (lo, hi) {
    return hi >= 1e12 ? one(lo, 1e12) + " - " + one(hi, 1e12) + " TB" : one(lo, 1e9) + " - " + one(hi, 1e9) + " GB";
  };
  ui.num = function (n) { return Number(n).toLocaleString("en-US"); };
  ui.el = function (tag, cls, txt) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (txt != null) e.textContent = txt;
    return e;
  };

  // Preview card: host is the element (give it aria-live="polite"). start(body) asks for an
  // estimate; mark() says the estimate is out of date. Always a range, never one figure.
  ui.preview = function (host, libId, getSetting) {
    var timer, poll, tries, gen = 0;
    function say(heading, rows) {
      host.textContent = "";
      host.appendChild(ui.el("h2", null, "Estimate for your files")).style.marginTop = "0";
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
      ul.appendChild(ui.el("li", null, ui.num(r.files) + " videos found (" + ui.bytes(r.totalBytes) + ")"));
      ul.appendChild(ui.el("li", null, ui.num(r.canShrink) + " can be shrunk"));
      if (skipped) ul.appendChild(ui.el("li", null, ui.num(skipped) + " will be skipped: " + parts.join(", ")));
      say("", [big, ul, ui.el("p", "hint", "This is a guess, not a promise. Real savings are measured on each file.")]);
    }
    function giveUp() { say("", [note("We couldn't estimate this library. You can still start.")]); }
    function ask() {
      var my = ++gen; tries = 0;
      say("", [note("Working out an estimate...")]);
      (function once() {
        g.SYP.api.preview({ id: libId }, { setting: getSetting() }).then(function (r) {
          if (my !== gen) return;
          if (r.status === "ready") return show(r);
          if (r.status === "failed" || ++tries > 30) return giveUp();
          poll = setTimeout(once, 2000);
        }, function () { if (my === gen) giveUp(); });
      })();
    }
    return {
      start: ask,
      // The setting changed: say so now, and refresh shortly after the last change.
      mark: function () {
        clearTimeout(timer); clearTimeout(poll); gen++;
        say("", [note("Your choice changed. Updating the estimate...")]);
        timer = setTimeout(ask, 500);
      },
      stop: function () { clearTimeout(timer); clearTimeout(poll); gen++; }
    };
  };
  g.SYP = g.SYP || {};
  g.SYP.ui = ui;
})(window);
