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

  // The Basic editor (redesign A1): the slider, "Your files become", the Picture line, Audio with its hints and "Your
  // language", and three fixed lines. s is the setting answer: quality, audio, and becomes (the flow's fixed facts from
  // the server, contract A5: {video, resolution, fileType}); without becomes the box is not shown (no guessed facts).
  // langs: the person's language codes; names: code -> name. noLangLine: first-run asks "Your language" itself (A4), so
  // the "Your language: ... Change it in Settings" line is left out. get() gives {quality, audio}.
  var PICTURE = ["The smallest files. Fine detail can soften, mostly in dark or busy scenes.", "Small files, with a small loss of fine detail.",
    "Little visible change for most people. A good start.", "Keeps more detail. Files stay larger.", "Hard to tell from the original. Files stay largest."];
  var AUDIO = [["stereo_aac", "Stereo AAC in my language", "Audio in your language becomes stereo AAC, which plays on every TV and phone. Surround sound becomes stereo."],
    ["my_languages", "My language, as it is", "Only tracks in your language are kept, unchanged, surround included."],
    ["keep_all", "Keep all audio", "Every audio track is kept, unchanged."]];
  var FIXED = ["Every video not already in this format is converted, even if the new file isn't smaller, so your library ends up in one format. Activity shows the size before and after.",
    "Each new file is checked against the original before it is kept. If the check fails, the original stays.",
    "HDR stays HDR. Dolby Vision files are left as they are."];
  function basic(host, s, langs, names, onChange, label, noLangLine) {
    var sl = slider(host, s.quality, function () { draw(); if (onChange) onChange(); }, label), range = host.querySelector("input[type=range]");
    var box = el("div", "become"), line = el("span"), pic = el("p"), pt = el("span"), f = el("div", "field"), lb = el("label", null, "Audio"),
      sel = el("select"), hint = el("span", "hint"), mine = el("span", "hint"), ul = el("ul", "small muted");
    box.id = uid(); box.setAttribute("aria-live", "polite"); box.appendChild(el("strong", null, "Your files become")); box.appendChild(line);
    pic.id = uid(); pic.setAttribute("aria-live", "polite"); pic.appendChild(el("strong", null, "Picture:")); pic.appendChild(document.createTextNode(" ")); pic.appendChild(pt);
    range.setAttribute("aria-describedby", (s.becomes ? box.id + " " : "") + pic.id);
    sel.id = uid(); lb.htmlFor = sel.id; hint.id = uid(); mine.id = uid(); sel.setAttribute("aria-describedby", hint.id + " " + mine.id);
    AUDIO.forEach(function (a) { var o = el("option", null, a[1]); o.value = a[0]; sel.appendChild(o); });
    sel.value = AUDIO.some(function (a) { return a[0] === s.audio; }) ? s.audio : "stereo_aac";
    // "English", "English and German", "English, German and French"
    var list = langs.map(function (c) { return names[c] || c; }), said = list.length < 2 ? list.join("") : list.slice(0, -1).join(", ") + " and " + list[list.length - 1];
    if (list.length) { mine.appendChild(document.createTextNode("Your language: ")); mine.appendChild(el("strong", null, said)); mine.appendChild(document.createTextNode(". ")); }
    var to = el("a", null, list.length ? "Change it in Settings > General." : "Choose your language in Settings > General first."); to.href = "/settings/"; mine.appendChild(to);
    function audioWords() {
      // No language yet (design, 20:08Z): "... (not chosen yet)".
      return { stereo_aac: said ? "stereo AAC in " + said : "stereo AAC in your language (not chosen yet)",
        my_languages: said ? said + " audio as it is" : "audio in your language as it is (not chosen yet)", keep_all: "all audio as it is" }[sel.value];
    }
    function draw() {
      var b = s.becomes;
      if (b) line.textContent = [b.video + " video", b.resolution === "same" ? "same resolution" : "at most " + b.resolution, b.fileType + " file", audioWords()].join(" \u00b7 ");
      pt.textContent = PICTURE[sl.get().quality - 1];
      hint.textContent = AUDIO.filter(function (a) { return a[0] === sel.value; })[0][2];
    }
    sel.addEventListener("change", function () { draw(); if (onChange) onChange(); });
    f.appendChild(lb); f.appendChild(sel); f.appendChild(hint); if (noLangLine) sel.setAttribute("aria-describedby", hint.id); else f.appendChild(mine);
    FIXED.forEach(function (t) { ul.appendChild(el("li", null, t)); });
    if (s.becomes) host.appendChild(box);
    host.appendChild(pic); host.appendChild(f); host.appendChild(ul); draw();
    return { get: function () { return { quality: sl.get().quality, audio: sel.value }; } };
  }

  // The nine dropdowns, in four groups, all at their defaults (the same as Balanced).
  // [field, label, [value, text] choices, default index, hint]. The values are what the setting call
  // carries; the table is written down in UI-API-CONTRACT.md.
  var GROUPS = [
    ["Video", [
      ["quality", "Quality", [[1, "Smallest file"], [2, "Smaller"], [3, "Balanced"], [4, "Better quality"], [5, "Best quality"]], 2, "Same scale as the slider in Basic."],
      ["speed", "Speed", [["faster", "Faster"], ["normal", "Normal"], ["slower", "Slower, slightly smaller files"]], 1, "Slower takes about twice as long."],
      ["format", "Format", [["hevc", "HEVC - smaller files"]], 0, ""],
      ["resolution", "Resolution", [["keep", "Keep as is"], ["1080", "At most 1080p"], ["720", "At most 720p"], ["480", "At most 480p"]], 0, "Videos are only ever made smaller, never stretched."],
      ["maxBitrate", "Max bitrate", [["none", "No limit"], ["40", "40 Mbps - 4K streaming"], ["20", "20 Mbps - 1080p, fast home network"], ["10", "10 Mbps - 1080p, most home networks"], ["6", "6 Mbps - 720p or slower Wi-Fi"], ["4", "4 Mbps - streaming away from home"]], 0, "Keeps files small enough to stream over slower connections. Quality still sets the picture; a low limit can soften very busy scenes."]]],
    ["Audio and subtitles", [
      ["audio", "Audio", [["keep_all", "Keep all tracks"], ["my_languages", "Keep only my languages"], ["aac", "Convert to AAC"], ["eac3", "Convert to E-AC-3"], ["opus", "Convert to Opus"], ["lossless_only", "Convert lossless tracks only"]], 0, ""],
      ["subtitles", "Subtitles", [["keep_all", "Keep all"], ["my_languages", "Keep only my languages"], ["text_only", "Text subtitles only"], ["srt", "Save as separate .srt files"]], 0, "Subtitle pictures are never burned into the video."]]],
    ["New file", [
      ["fileType", "File type", [["keep", "Keep as is"], ["mkv", "MKV"], ["mp4", "MP4"]], 0, "If MP4 cannot hold a track, the file is skipped and the reason is shown. Nothing is dropped silently."],
      ["afterConverting", "After converting", [["replace", "Replace the original"], ["beside", "Put the new file next to the original"]], 0, "How long originals are kept is set in Settings."]]],
    ["Which files", []] // which files go first is one global setting: Settings > Performance, "Do first"
  ];
  // Choices this version can save, from the setting answer (contract addendum 13): the others stay visible but
  // disabled, with design's line. A missing list (an older server) means every choice is available.
  var AVAIL = { audio: "availableAudio", subtitles: "availableSubtitles" };
  var ONLY_KEEP = { audio: "For now every audio track is kept. The other choices come in a later update.",
    subtitles: "For now every subtitle is kept. The other choices come in a later update." };
  // Lines that show under Audio or Subtitles only for some choices (slice 25b, design): "Keep only my languages"
  // while no language is chosen yet (it links to where they are chosen, so the server's 400 languages_needed
  // doesn't happen), and what "Save as separate .srt files" does with picture subtitles.
  var NEEDS = "Keep only my languages needs your languages. Choose them in ", SRT = "Picture subtitles stay inside the file; only text subtitles are saved as .srt files.";
  // values (optional): a saved setting to show; anything missing stays at its default.
  function selectors(host, onChange, values) {
    var fields = {}, rules = {}, v = values || {}, syncs = [], noLanguages = false;
    function changed() { if (onChange) onChange(); }
    GROUPS.forEach(function (gr) {
      var fs = el("fieldset"); fs.appendChild(el("legend", null, gr[0]));
      gr[1].forEach(function (f) {
        var id = uid(), box = el("div", "field"), lab = el("label", null, f[1]), sel = el("select");
        lab.htmlFor = id; sel.id = id;
        f[2].forEach(function (o) { var op = el("option", null, o[1]); op.value = o[0]; sel.appendChild(op); });
        sel.value = v[f[0]] != null ? String(v[f[0]]) : f[2][f[3]][0];
        if (sel.selectedIndex < 0) sel.value = f[2][f[3]][0];
        fields[f[0]] = sel; sel.addEventListener("change", changed);
        box.appendChild(lab); box.appendChild(sel);
        var avail = AVAIL[f[0]] && v[AVAIL[f[0]]], said = [];
        if (Array.isArray(avail) && avail.length < f[2].length) {
          Array.prototype.forEach.call(sel.options, function (op) { op.disabled = avail.indexOf(op.value) < 0; });
          if (sel.options[sel.selectedIndex].disabled) sel.value = avail[0] || f[2][f[3]][0];
          var only = avail.length === 1 && avail[0] === "keep_all", nt = el("span", "hint", only ? ONLY_KEEP[f[0]] : "Some choices come in a later update.");
          nt.id = id + "n"; box.appendChild(nt); said.push(nt.id);
        }
        var lines = [];
        if (AVAIL[f[0]]) {
          var need = el("span", "hint", NEEDS), go = el("a", null, "Settings > General"); go.href = "/settings/";
          need.appendChild(go); need.appendChild(document.createTextNode(".")); need.id = id + "l";
          lines.push([need, function (val) { return noLanguages && val === "my_languages"; }]);
        }
        if (f[0] === "subtitles") { var srt = el("span", "hint", SRT); srt.id = id + "s"; lines.push([srt, function (val) { return val === "srt"; }]); }
        lines.forEach(function (l) { box.appendChild(l[0]); });
        var hint = null;
        if (f[4]) { hint = el("span", "hint", f[4]); hint.id = id + "h"; box.appendChild(hint); }
        var sync = (function (sel, said, lines, hint) {
          return function () {
            var ids = said.slice();
            lines.forEach(function (l) { var on = l[1](sel.value); l[0].hidden = !on; if (on) ids.push(l[0].id); });
            if (hint) ids.push(hint.id);
            if (ids.length) sel.setAttribute("aria-describedby", ids.join(" ")); else sel.removeAttribute("aria-describedby");
          };
        })(sel, said, lines, hint);
        sync(); // the others keep this; File type's note (addendum 14) is added to it below
        if (lines.length) { sel.addEventListener("change", sync); syncs.push(sync); }
        fs.appendChild(box);
      });
      if (gr[0] === "Which files") {
        var ig = el("div", "field"), igId = uid(), igIn = el("textarea"), igH = el("span", "hint", "One word or path per line.");
        igIn.rows = 2; igIn.value = (v.ignore || []).join("\n"); igIn.id = igId; igH.id = igId + "h"; igIn.setAttribute("aria-describedby", igH.id);
        var l = el("label", null, "Ignore paths containing"); l.htmlFor = igId;
        ig.appendChild(l); ig.appendChild(igIn); ig.appendChild(igH); fs.appendChild(ig); rules.ignore = igIn;
        igIn.addEventListener("change", changed);
        [["skipHdr", "Skip HDR and Dolby Vision files", "Recommended. Converting them can damage the picture."]].forEach(function (c) {
          var row = el("div", "check"), cb = el("input"), id = uid(), lb = el("label", null, c[1]);
          cb.type = "checkbox"; cb.id = id; cb.checked = v[c[0]] != null ? !!v[c[0]] : true; lb.htmlFor = id;
          if (c[2]) { lb.appendChild(document.createElement("br")); lb.appendChild(el("span", "hint", c[2])); }
          row.appendChild(cb); row.appendChild(lb); fs.appendChild(row); rules[c[0]] = cb; cb.addEventListener("change", changed);
        });
      }
      host.appendChild(fs);
    });
    // File type follows "After converting" (contract addendum 14; 25a-2 lists every type for both): a type not listed
    // for the chosen mode is disabled, and a chosen one that is not falls back to "Keep as is", as Audio and Subtitles do.
    var ft = v.availableFileTypes, ftSel = fields.fileType, acSel = fields.afterConverting;
    if (ft && typeof ft === "object" && ftSel && acSel) {
      var syncFt = function () {
        var ok = ft[acSel.value];
        Array.prototype.forEach.call(ftSel.options, function (o) { o.disabled = !!ok && ok.indexOf(o.value) < 0; });
        if (ftSel.options[ftSel.selectedIndex].disabled) { ftSel.value = "keep"; changed(); }
      };
      acSel.addEventListener("change", syncFt);
      syncFt();
    }
    // The person's languages are one global setting (Settings > General), not part of the library's setting.
    g.SYP.api.getSettings().then(function (st) { noLanguages = !(st.languages || []).length; syncs.forEach(function (f) { f(); }); }, function () {});
    return {
      get: function () {
        var b = {}, k;
        for (k in fields) b[k] = k === "quality" ? +fields[k].value : fields[k].value;
        b.ignore = rules.ignore.value.split("\n").map(function (x) { return x.trim(); }).filter(Boolean);
        b.skipHdr = rules.skipHdr.checked; // no minimum saving and no "already small" since workshop S1 (always standardize)
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
  g.SYP.editors = { slider: slider, basic: basic, selectors: selectors, startFrom: startFrom };
})(window);
