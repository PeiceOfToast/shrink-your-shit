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
  // The Standard editor (redesign E; workshop S6, as proposed): nine dropdowns and two yes/no boxes. Each choice is
  // [value, label, hint shown when it is chosen]; a field-level hint shows when the choice has none.
  var LEVELS = { high: "High", medium: "Medium", low: "Low" };
  var GROUPS = [
    ["Video", [
      ["quality", "Quality", [[1, "Smallest file"], [2, "Smaller"], [3, "Balanced"], [4, "Better quality"], [5, "Best quality"]], 2, "Same scale as the slider in Basic."],
      ["speed", "Speed", [["faster", "Faster"], ["normal", "Normal"], ["slower", "Slower, slightly smaller files"]], 1, "Slower takes about twice as long."],
      ["format", "Video format", [["hevc", "HEVC", "Small files that play on most TVs and phones made since 2016."], ["h264", "H.264", "Plays on everything, even old devices. Files are about twice the size of HEVC."],
        ["av1", "AV1", "The smallest files, but slow to make and only for newer TVs and devices."], ["vp9", "VP9", "Small files for web browsers and Android. Few TVs play it."]], 0, ""],
      ["resolution", "Resolution", [["keep", "Keep as is"], ["1080", "At most 1080p"], ["720", "At most 720p"], ["480", "At most 480p"]], 0, "Videos are only ever made smaller, never stretched."],
      ["maxBitrate", "Max bitrate", [["none", "No limit"], ["high", "High"], ["medium", "Medium"], ["low", "Low"]], 0, ""]]],
    ["Audio and subtitles", [
      ["audio", "Audio", [["stereo_aac", "Stereo AAC", "Plays on every TV and phone. Surround sound becomes stereo."],
        ["stereo_aac_surround", "Stereo AAC, plus surround as it is", "Adds a stereo track for phones and TVs, and keeps the original surround track for sound systems."],
        ["eac3_surround", "Surround E-AC-3", "The best surround sound for modern TVs and sound bars."],
        ["ac3_surround", "Surround AC-3", "Surround sound that plays on almost every TV and sound bar; older than E-AC-3."],
        ["stereo_opus", "Stereo Opus", "The smallest audio, for MKV files. Doesn't play on Apple TV or most TVs."], ["as_is", "As it is", "Tracks are copied unchanged."]], 0, ""],
      ["subtitles", "Subtitles", [["keep_all", "Keep all"], ["my_languages", "Keep only my languages"], ["text_only", "Text subtitles only"], ["srt", "Save as separate .srt files"]], 0, "Subtitle pictures are never burned into the video."]]],
    ["New file", [
      ["fileType", "File type", [["mkv", "MKV", "Holds every kind of track; the usual choice for Plex and Jellyfin."],
        ["mp4", "MP4", "Best for phones, browsers and Apple TV. Picture subtitles and some surround formats can't go in: picture subtitles are skipped with a reason, and that surround is converted to E-AC-3."],
        ["webm", "WebM", "For web browsers. Only with AV1 or VP9 video and Opus audio."], ["keep", "Keep as is", "Each file keeps its current type."]], 0, ""],
      ["afterConverting", "After converting", [["replace", "Replace the original", "The original is kept for 7 days (Settings > Safety), then deleted."], ["beside", "Put the new file next to the original", "Both stay. Nothing is deleted."],
        ["folder", "Save to another folder", "The original stays where it is. New files go to the folder below, in the same sub-folders."]], 0, ""]]],
    ["Which files", []] // which files go first is one global setting: Settings > Performance, "Do first"
  ];
  var NEEDS = "Keep only my languages needs your languages. Choose them in ", SRT = "Picture subtitles stay inside the file; only text subtitles are saved as .srt files.",
    SURROUND = "Stereo tracks stay stereo: surround sound can't be made from stereo.";
  // values: a saved setting (with the server's available* lists and bitrateLadder); anything missing stays at its
  // default. values.checkOutput(path) checks the "Save to another folder" folder (the caller knows the flow).
  function selectors(host, onChange, values) {
    var fields = {}, v = values || {}, syncs = [], noLanguages = false, ladder = v.bitrateLadder || null, x = {};
    function changed() { if (onChange) onChange(); }
    function syncAll() { syncs.forEach(function (f) { f(); }); }
    // A choice this version can't save (the server's lists) is disabled with its reason.
    function blocked(name, val) {
      var f = fields;
      if (name === "format" && Array.isArray(v.availableFormats) && v.availableFormats.indexOf(val) < 0) return " (not available on this computer)";
      if (name === "audio" && Array.isArray(v.availableAudio) && v.availableAudio.indexOf(val) < 0) return " (not available yet)";
      if (name === "fileType" && v.availableFileTypes && f.afterConverting) { var ok = v.availableFileTypes[f.afterConverting.value]; if (ok && ok.indexOf(val) < 0) return " (not available yet)"; }
      return "";
    }
    // A choice that doesn't go with another one keeps its reason but stays selectable (design, "Choices that can't go
    // together"): picking it moves the other field, with a note (resolve below).
    function pairWhy(name, val) {
      var f = fields;
      if (name === "audio" && val === "stereo_opus" && f.fileType && f.fileType.value === "mp4") return " (MKV only)";
      if (name === "fileType" && val === "webm" && f.format && f.audio && !((f.format.value === "av1" || f.format.value === "vp9") && f.audio.value === "stereo_opus")) return " (needs AV1 or VP9 and Opus)";
      return "";
    }
    // The field the person just changed wins; the other moves to the nearest choice that works, with design's words
    // in a note under it ("<Field> changed to <choice>: <reason>.") and one polite announcement.
    var NAME = { audio: "Audio", fileType: "File type", format: "Video format" }, notes = {}, live = el("p", "sr");
    live.setAttribute("role", "status");
    function choiceText(name, val) { var g2 = GROUPS.reduce(function (a, gr) { return a.concat(gr[1]); }, []).filter(function (x) { return x[0] === name; })[0]; return (g2[2].filter(function (o) { return String(o[0]) === val; })[0] || [0, val])[1]; }
    function move(name, val, reason, said) { fields[name].value = val; said.push([name, NAME[name] + " changed to " + choiceText(name, val) + ": " + reason]); }
    function resolve(changed) {
      var F = fields, said = [], fmt = F.format.value, au = F.audio.value, ft = F.fileType.value;
      if (changed === "fileType" && ft === "mp4" && au === "stereo_opus") move("audio", "stereo_aac", "MP4 files can't hold Opus.", said);
      if (changed === "fileType" && ft === "webm") {
        if (fmt !== "av1" && fmt !== "vp9") move("format", blocked("format", "av1") ? "vp9" : "av1", "WebM files need AV1 or VP9 video.", said);
        if (au !== "stereo_opus") move("audio", "stereo_opus", "WebM files need Opus audio.", said);
      }
      if (changed === "audio" && au === "stereo_opus" && ft === "mp4") move("fileType", "mkv", "MP4 files can't hold Opus.", said);
      if (changed === "audio" && au !== "stereo_opus" && ft === "webm") move("fileType", "mkv", "WebM files need Opus audio.", said);
      if (changed === "format" && (fmt === "hevc" || fmt === "h264") && ft === "webm") move("fileType", "mkv", "WebM files can only hold AV1 or VP9 video.", said);
      // A new move replaces earlier notes (their reason may no longer hold, e.g. WebM is gone); otherwise notes stay.
      if (said.length) {
        Object.keys(notes).forEach(function (k) { notes[k].hidden = true; notes[k].textContent = ""; });
        said.forEach(function (x) { notes[x[0]].textContent = x[1]; notes[x[0]].hidden = false; });
        live.textContent = ""; live.textContent = said.map(function (x) { return x[1]; }).join(" ");
      }
    }
    function label(name, o) { // Max bitrate carries its 1080p number for the chosen Video format (the server's ladder)
      if (name !== "maxBitrate" || o[0] === "none" || !ladder || !fields.format) return o[1];
      var row = (ladder[fields.format.value] || {})[o[0]]; return row ? o[1] + " · " + row["1080"] + " Mbps at 1080p" : o[1];
    }
    GROUPS.forEach(function (gr) {
      var fs = el("fieldset"); fs.appendChild(el("legend", null, gr[0]));
      gr[1].forEach(function (f) {
        var id = uid(), box = el("div", "field"), lab = el("label", null, f[1]), sel = el("select"), hint = el("span", "hint"), lines = [], note = el("span", "hint");
        lab.htmlFor = id; sel.id = id; hint.id = id + "h"; note.id = id + "m"; note.hidden = true; notes[f[0]] = note;
        f[2].forEach(function (o) { var op = el("option", null, o[1]); op.value = o[0]; sel.appendChild(op); });
        sel.value = v[f[0]] != null ? String(v[f[0]]) : f[2][f[3]][0];
        if (sel.selectedIndex < 0) sel.value = f[2][f[3]][0];
        fields[f[0]] = sel; box.appendChild(lab); box.appendChild(sel);
        if (f[0] === "subtitles") {
          var need = el("span", "hint", NEEDS), go = el("a", null, "Settings > General"); go.href = "/settings/"; need.appendChild(go); need.appendChild(document.createTextNode(".")); need.id = id + "l";
          lines.push([need, function (val) { return noLanguages && val === "my_languages"; }]);
          var srt = el("span", "hint", SRT); srt.id = id + "s"; lines.push([srt, function (val) { return val === "srt"; }]);
        }
        if (f[0] === "audio") { var sur = el("span", "hint", SURROUND); sur.id = id + "r"; lines.push([sur, function (val) { return val === "eac3_surround" || val === "ac3_surround"; }]); }
        box.appendChild(note); lines.forEach(function (l) { box.appendChild(l[0]); }); box.appendChild(hint);
        syncs.push(function () {
          Array.prototype.forEach.call(sel.options, function (op, k) { var why = blocked(f[0], op.value); op.disabled = !!why; op.textContent = label(f[0], f[2][k]) + (why || pairWhy(f[0], op.value)); });
          if (sel.options[sel.selectedIndex].disabled) { var first = Array.prototype.filter.call(sel.options, function (op) { return !op.disabled; })[0]; if (first) sel.value = first.value; }
          var ch = f[2].filter(function (o) { return String(o[0]) === sel.value; })[0], text = ch && ch[2] || f[4];
          if (f[0] === "maxBitrate" && sel.value !== "none" && ladder && (ladder[fields.format.value] || {})[sel.value]) {
            var r = ladder[fields.format.value][sel.value];
            text = "Limits each file by its size: 4K " + r["2160"] + " · 1440p " + r["1440"] + " · 1080p " + r["1080"] + " · 720p " + r["720"] + " · SD " + r.sd + " Mbps. A file is never given more than it has now. Quality still sets the picture; the limit only affects busy scenes.";
          }
          hint.textContent = text || ""; hint.hidden = !text;
          var ids = note.hidden ? [] : [note.id]; lines.forEach(function (l) { var on = l[1](sel.value); l[0].hidden = !on; if (on) ids.push(l[0].id); }); if (text) ids.push(hint.id);
          if (ids.length) sel.setAttribute("aria-describedby", ids.join(" ")); else sel.removeAttribute("aria-describedby");
        });
        // The person's own change clears this field's note, then may move another field (with its note).
        sel.addEventListener("change", function () { note.hidden = true; note.textContent = ""; resolve(f[0]); syncAll(); changed(); });
        fs.appendChild(box);
        if (f[0] === "audio") { // "Only my languages" belongs to Audio (a declared yes/no box)
          var row = el("div", "check"), cb = el("input"), cid = uid(), lb = el("label", null, "Only my languages"), oh = el("span", "hint");
          cb.type = "checkbox"; cb.id = cid; cb.checked = v.onlyMyLanguages != null ? !!v.onlyMyLanguages : true; lb.htmlFor = cid; oh.id = cid + "h"; cb.setAttribute("aria-describedby", oh.id);
          lb.appendChild(document.createElement("br")); lb.appendChild(oh); row.appendChild(cb); row.appendChild(lb); fs.appendChild(row); x.only = cb; x.onlyHint = oh;
          cb.addEventListener("change", changed);
        }
        if (f[0] === "afterConverting") { // "Save to another folder" needs its folder: a field the choice needs, not an expander
          var ob = el("div", "field"), oid = uid(), ol = el("label", null, "Folder for new files"), orow = el("div", "row"), oin = el("input"), ob2 = el("button", "btn", "Browse..."), ores = el("ul", "stack"), olist = el("div", "card");
          oin.id = oid; ol.htmlFor = oid; oin.size = 34; oin.value = v.outputDir || ""; oin.autocomplete = "off"; oin.spellcheck = false; ob2.type = "button"; olist.hidden = true;
          ores.style.cssText = "list-style:none;padding:0"; ores.setAttribute("aria-live", "polite"); ores.id = oid + "c"; oin.setAttribute("aria-describedby", ores.id);
          orow.appendChild(oin); orow.appendChild(ob2); ob.appendChild(ol); ob.appendChild(orow); ob.appendChild(olist); ob.appendChild(ores); fs.appendChild(ob); x.out = oin; x.outBox = ob;
          var check = function () {
            ores.textContent = ""; var path = oin.value.trim(); if (!path || !v.checkOutput) return;
            v.checkOutput(path).then(function (r) {
              ores.textContent = ""; (r.blockers || []).forEach(function (b) { ores.appendChild(el("li", "notice bad", b.message)); });
              if (!(r.blockers || []).length) ores.appendChild(el("li", "small", "OK: new files can be saved here."));
            }, function (e) { ores.textContent = ""; ores.appendChild(el("li", "notice bad", e && e.message || "This folder can't be checked.")); });
          };
          oin.addEventListener("change", function () { check(); changed(); });
          var browse = function (path) {
            g.SYP.api.browseDirs({ path: path }).then(function (r) {
              olist.textContent = ""; olist.hidden = false; olist.appendChild(el("p", "mono small", r.path));
              if (r.parent && r.parent !== r.path) olist.appendChild(el("button", "btn small", "Up one folder")).onclick = function () { browse(r.parent); };
              r.dirs.forEach(function (d) { var b = el("button", "btn small", d); b.type = "button"; b.onclick = function () { browse(r.path.replace(/[\\/]$/, "") + "/" + d); }; olist.appendChild(b); });
              var use = el("button", "btn small primary-outline", "Use this folder"); use.type = "button"; use.onclick = function () { oin.value = r.path; olist.hidden = true; check(); changed(); oin.focus(); }; olist.appendChild(use);
            }, function () {});
          };
          ob2.onclick = function () { browse(oin.value.trim() || "/"); };
          syncs.push(function () { ob.hidden = fields.afterConverting.value !== "folder"; });
          if (oin.value) check();
        }
      });
      if (gr[0] === "Which files") {
        var ig = el("div", "field"), igId = uid(), igIn = el("textarea"), igH = el("span", "hint", "One word or path per line.");
        igIn.rows = 2; igIn.value = (v.ignore || []).join("\n"); igIn.id = igId; igH.id = igId + "h"; igIn.setAttribute("aria-describedby", igH.id);
        var l = el("label", null, "Ignore paths containing"); l.htmlFor = igId;
        ig.appendChild(l); ig.appendChild(igIn); ig.appendChild(igH); fs.appendChild(ig); x.ignore = igIn;
        igIn.addEventListener("change", changed);
        // Workshop S3: HDR10 is always converted with its HDR kept; only Dolby Vision is a choice (design, REDESIGN E).
        var dv = el("div", "check"), dcb = el("input"), did = uid(), dlb = el("label", null, "Include Dolby Vision files"), dh = el("span", "hint", "Off: Dolby Vision files are left as they are. On: they become HDR10 and the Dolby Vision layer is removed. Some Dolby Vision files (profile 5) are always left alone, because converting them would show wrong colours.");
        dcb.type = "checkbox"; dcb.id = did; dcb.checked = !!v.includeDolbyVision; dlb.htmlFor = did; dh.id = did + "h"; dcb.setAttribute("aria-describedby", dh.id);
        dlb.appendChild(document.createElement("br")); dlb.appendChild(dh); dv.appendChild(dcb); dv.appendChild(dlb); fs.appendChild(dv); x.dv = dcb;
        dcb.addEventListener("change", changed);
      }
      host.appendChild(fs);
    });
    // "Only my languages": the names come from Settings > General (the same list and names Settings shows).
    var names = [];
    function syncOnly() {
      var t = x.onlyHint; t.textContent = "";
      t.appendChild(document.createTextNode(names.length ? "Only audio in your languages is kept: " + names.join(", ") + ". " : "Only audio in your languages is kept. "));
      var a = el("a", null, names.length ? "Change them in Settings > General." : "Choose them in Settings > General."); a.href = "/settings/"; t.appendChild(a);
    }
    syncs.push(syncOnly); host.appendChild(live); syncAll();
    Promise.all([g.SYP.api.getSettings(), fetch("/assets/languages.json").then(function (r) { return r.json(); }).then(null, function () { return []; })]).then(function (r) {
      var codes = r[0].languages || [], map = {}; r[1].forEach(function (l) { map[l[0]] = l[1]; });
      noLanguages = !codes.length; names = codes.map(function (c) { return map[c] || c; }); syncAll();
    }, function () {});
    return {
      get: function () {
        var b = {}, k;
        for (k in fields) b[k] = k === "quality" ? +fields[k].value : fields[k].value;
        b.onlyMyLanguages = x.only.checked; b.includeDolbyVision = x.dv.checked;
        b.ignore = x.ignore.value.split("\n").map(function (y) { return y.trim(); }).filter(Boolean);
        if (b.afterConverting === "folder") b.outputDir = x.out.value.trim();
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
