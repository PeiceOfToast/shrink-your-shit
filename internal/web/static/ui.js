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

  // Preview card body: host is the element (give it aria-live="polite"; the caller adds the heading).
  // start() asks for an estimate; mark() says it is out of date. Always a range, never one figure.
  // extra (optional): {sample: () => path of the sample file or null, onSample: (sample or null) => ...}.
  ui.preview = function (host, libId, getSetting, extra) {
    extra = extra || {};
    var timer, poll, tries, gen = 0;
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
      ul.appendChild(ui.el("li", null, ui.num(r.files) + " videos found (" + ui.bytes(r.totalBytes) + ")"));
      // Before slice 12 shared (hardlinked) files are converted but free nothing (contract addendum 12).
      var sh = r.sharedNoSaving || 0;
      ul.appendChild(ui.el("li", null, sh ? ui.num(r.canShrink) + " will be converted; " + ui.num(sh) + " of them " + (sh === 1 ? "is" : "are") + " shared with another folder and will free no space"
        : ui.num(r.canShrink) + " can be shrunk"));
      if (skipped) ul.appendChild(ui.el("li", null, ui.num(skipped) + " will be skipped: " + parts.join(", ")));
      say([big, ul, ui.el("p", "hint", "This is a guess, not a promise. Real savings are measured on each file.")]);
    }
    function giveUp() { say([note("We couldn't estimate this library. You can still start.")]); }
    function ask() {
      var my = ++gen; tries = 0;
      say([note("Working out an estimate...")]);
      (function once() {
        g.SYP.api.preview({ id: libId }, { setting: getSetting(), sampleFile: extra.sample ? extra.sample() : null }).then(function (r) {
          if (my !== gen) return;
          if (r.status === "ready") { show(r); if (extra.onSample) extra.onSample(r.sample || null); return; }
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
        say([note("Your choice changed. Updating the estimate...")]); if (extra.onSample) extra.onSample(null);
        timer = setTimeout(ask, 500);
      },
      stop: function () { clearTimeout(timer); clearTimeout(poll); gen++; }
    };
  };
  g.SYP = g.SYP || {};
  g.SYP.ui = ui;
})(window);
