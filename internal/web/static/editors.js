/* Setting editors, shared by first-run and (later) the library page. Each draws into host
   and returns {get()} with the setting body the contract's PUT .../setting takes. */
(function (g) {
  "use strict";
  var ui = g.SYP.ui, el = ui.el, n = 0;
  function uid() { return "ed" + (++n); }

  // Five-position slider: native range, "3 of 5, Balanced" for screen readers, readout below.
  function slider(host, quality, onChange, label) {
    var id = uid(), wrap = el("div", "slider-wrap"), r = el("input"), ticks = el("div", "ticks"), out = el("p", "slider-readout");
    r.type = "range"; r.min = 1; r.max = 5; r.value = quality || 3; r.id = id;
    r.setAttribute("aria-label", label);
    ticks.setAttribute("aria-hidden", "true");
    ui.QUALITY.forEach(function (q) { ticks.appendChild(el("span", null, q)); });
    function sync() {
      var i = +r.value - 1;
      r.setAttribute("aria-valuetext", (i + 1) + " of 5, " + ui.QUALITY[i]);
      Array.prototype.forEach.call(ticks.children, function (t, k) { t.className = k === i ? "on" : ""; });
      out.textContent = ui.QUALITY[i];
    }
    r.addEventListener("input", function () { sync(); if (onChange) onChange(); });
    wrap.appendChild(r); wrap.appendChild(ticks); out.setAttribute("aria-hidden", "true");
    host.appendChild(wrap); host.appendChild(out); sync();
    return { get: function () { return { quality: +r.value }; } };
  }

  // The nine dropdowns, in four groups, all at their defaults (the same as Balanced).
  // [field, label, [value, text] choices, default index, hint]. The values are what the setting call
  // carries; the table is written down in UI-API-CONTRACT.md.
  var GROUPS = [
    ["Video", [
      ["quality", "Quality", [[1, "Smallest file"], [2, "Smaller"], [3, "Balanced"], [4, "Better quality"], [5, "Best quality"]], 2, "Same scale as the slider in Basic."],
      ["speed", "Speed", [["faster", "Faster"], ["normal", "Normal"], ["slower", "Slower, slightly smaller files"]], 1, "Slower takes about twice as long."],
      ["format", "Format", [["hevc", "HEVC - smaller files"]], 0, ""],
      ["resolution", "Resolution", [["keep", "Keep as is"], ["1080", "At most 1080p"], ["720", "At most 720p"], ["480", "At most 480p"]], 0, "Videos are only ever made smaller, never stretched."]]],
    ["Audio and subtitles", [
      ["audio", "Audio", [["keep_all", "Keep all tracks"], ["my_languages", "Keep only my languages"], ["aac", "Convert to AAC"], ["lossless_only", "Convert lossless tracks only"]], 0, ""],
      ["subtitles", "Subtitles", [["keep_all", "Keep all"], ["my_languages", "Keep only my languages"], ["text_only", "Text subtitles only"], ["srt", "Save as separate .srt files"]], 0, "Subtitle pictures are never burned into the video."]]],
    ["New file", [
      ["fileType", "File type", [["keep", "Keep as is"], ["mkv", "MKV"], ["mp4", "MP4"]], 0, "If MP4 cannot hold a track, the file is skipped and the reason is shown. Nothing is dropped silently."],
      ["afterConverting", "After converting", [["replace", "Replace the original"], ["beside", "Put the new file next to the original"]], 0, "How long originals are kept is set in Settings."]]],
    ["Which files", [["order", "Do first", [["newest", "Newest files"], ["oldest", "Oldest files"], ["largest", "Largest files"], ["smallest", "Smallest files"]], 0, ""]]]
  ];
  function selectors(host, onChange) {
    var fields = {}, rules = {};
    function changed() { if (onChange) onChange(); }
    GROUPS.forEach(function (gr) {
      var fs = el("fieldset"); fs.appendChild(el("legend", null, gr[0]));
      gr[1].forEach(function (f) {
        var id = uid(), box = el("div", "field"), lab = el("label", null, f[1]), sel = el("select");
        lab.htmlFor = id; sel.id = id;
        f[2].forEach(function (o) { var op = el("option", null, o[1]); op.value = o[0]; sel.appendChild(op); });
        sel.value = f[2][f[3]][0]; fields[f[0]] = sel; sel.addEventListener("change", changed);
        box.appendChild(lab); box.appendChild(sel);
        if (f[4]) { var h = el("span", "hint", f[4]); h.id = id + "h"; sel.setAttribute("aria-describedby", h.id); box.appendChild(h); }
        fs.appendChild(box);
      });
      if (gr[0] === "Which files") {
        var ig = el("div", "field"), igId = uid(), igIn = el("input"), igH = el("span", "hint", "One word or path per line.");
        igIn.type = "text"; igIn.id = igId; igH.id = igId + "h"; igIn.setAttribute("aria-describedby", igH.id);
        var l = el("label", null, "Ignore paths containing"); l.htmlFor = igId;
        ig.appendChild(l); ig.appendChild(igIn); ig.appendChild(igH); fs.appendChild(ig); rules.ignore = igIn;
        igIn.addEventListener("change", changed);
        [["skipHdr", "Skip HDR and Dolby Vision files", "Recommended. Converting them can damage the picture."], ["skipSmall", "Skip files that are already small", ""]].forEach(function (c) {
          var row = el("div", "check"), cb = el("input"), id = uid(), lb = el("label", null, c[1]);
          cb.type = "checkbox"; cb.id = id; cb.checked = true; lb.htmlFor = id;
          if (c[2]) { lb.appendChild(document.createElement("br")); lb.appendChild(el("span", "hint", c[2])); }
          row.appendChild(cb); row.appendChild(lb); fs.appendChild(row); rules[c[0]] = cb; cb.addEventListener("change", changed);
        });
        var ms = el("div", "field"), msId = uid(), msIn = el("input"), ml = el("label", null, "Keep the new file only if it is at least this much smaller (%)");
        msIn.type = "number"; msIn.id = msId; msIn.min = 1; msIn.max = 90; msIn.value = 10; msIn.style.width = "6rem"; ml.htmlFor = msId;
        ms.appendChild(ml); ms.appendChild(msIn); fs.appendChild(ms); rules.minSavingPct = msIn; msIn.addEventListener("change", changed);
      }
      host.appendChild(fs);
    });
    return {
      get: function () {
        var b = {}, k;
        for (k in fields) b[k] = k === "quality" ? +fields[k].value : fields[k].value;
        b.ignore = rules.ignore.value ? rules.ignore.value.split("\n") : [];
        b.skipHdr = rules.skipHdr.checked; b.skipSmall = rules.skipSmall.checked; b.minSavingPct = +rules.minSavingPct.value;
        return b;
      }
    };
  }

  // Advanced: pick a starting point; the builder arrives later.
  function startFrom(host, onChange) {
    var fs = el("fieldset"), q = 3, opts = [["Smallest file", 1], ["Balanced", 3], ["Better quality", 4]];
    fs.appendChild(el("legend", null, "Start from"));
    opts.forEach(function (o) {
      var row = el("div", "check"), r = el("input"), id = uid(), lb = el("label", null, o[0]);
      r.type = "radio"; r.name = "startFrom"; r.id = id; r.checked = o[1] === 3; lb.htmlFor = id;
      r.addEventListener("change", function () { q = o[1]; if (onChange) onChange(); });
      row.appendChild(r); row.appendChild(lb); fs.appendChild(row);
    });
    host.appendChild(fs);
    return { get: function () { return { quality: q }; } };
  }
  g.SYP.editors = { slider: slider, selectors: selectors, startFrom: startFrom };
})(window);
