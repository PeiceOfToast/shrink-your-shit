/* Workers (redesign D): one card per worker with its status in words, Start/Stop (a small confirm when files are
   converting), Files at once and the graphics card; the one-time "stopped after the update" notice. Polls every few
   seconds; a redraw only happens when something changed and puts focus back on the same control. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api, ui = S.ui, el = ui.el, seen = "", asking = null, last = null;
  function $(id) { return document.getElementById(id); }
  function fail(e) { var b = $("err"); b.textContent = e && e.message || "Something went wrong. Nothing was changed."; b.hidden = false; }
  function say(t) { $("said").textContent = ""; $("said").textContent = t; }
  function at(t) { return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }); }
  function btn(txt, key, fn, cls) { var b = el("button", cls || "btn", txt); b.type = "button"; b.setAttribute("data-key", key); b.onclick = fn; return b; }
  // S2 states: stopped | idle (running, no files) | converting | stopping | quiet_hours | unreachable.
  function running(w) { return w.state === "idle" || w.state === "converting" || w.state === "quiet_hours"; }

  function status(w) {
    var n = (w.converting || []).length;
    if (w.state === "converting") return ["ok", "Converting " + ui.count(n, "file", "files")];
    if (w.state === "idle") return ["ok", "Running, waiting for files"];
    if (w.state === "quiet_hours") return ["info", "Paused for quiet hours" + (w.quietUntil ? " until " + at(w.quietUntil) : "")];
    // After Stop now the server says "stopping" for a moment while it puts the files back: the page knows it asked for
    // "now", so it says a neutral "Stopping…" instead of "after the current file" (lead, 22:01Z).
    if (w.state === "stopping") return ["info", stoppedNow[w.id] ? "Stopping…" : "Stopping after the current file..."];
    if (w.state === "unreachable") return ["bad", "Can't reach this worker"];
    return ["idle", "Stopped"];
  }
  function act(p, words) { return p.then(function () { $("err").hidden = true; if (words) say(words); asking = null; seen = ""; return refresh().then(next); }, fail); } // next: poll fast at once while stopping
  var stoppedNow = {};
  function stop(w, when) { if (when === "now") stoppedNow[w.id] = true; else delete stoppedNow[w.id]; return act(api.stopWorker({ id: w.id }, { when: when }), when === "now" ? "Stopped. The files go back to wait for their turn." : "Stopping after these files."); }

  function card(w) {
    var c = el("div", "card"), top = el("div", "row between"), left = el("div"), h = el("h2", null, w.name), st = status(w), n = (w.converting || []).length;
    h.style.margin = "0"; left.appendChild(h); if (w.host) left.appendChild(el("span", "mono muted", w.host));
    top.appendChild(left); top.appendChild(el("span", "badge " + st[0], st[1])); c.appendChild(top);
    if (n) {
      var ul = el("ul", "small");
      w.converting.forEach(function (f) { var li = el("li"), a = el("a", null, f.path.split(/[\\/]/).pop()); a.href = "/activity/"; li.appendChild(a); li.appendChild(document.createTextNode(" " + f.percent + "%")); ul.appendChild(li); });
      c.appendChild(ul);
    }
    if (w.state === "quiet_hours") { var q = el("p", "small"), qa = el("a", null, "Quiet hours are set in Settings > Performance."); qa.href = "/settings/performance/"; q.appendChild(qa); c.appendChild(q); }
    if (w.state !== "unreachable") {
      var row = el("div", "row"); row.style.marginTop = "12px";
      if (w.state === "stopped") row.appendChild(btn("Start", "toggle-" + w.id, function () { act(api.startWorker({ id: w.id }), "Started."); }, "btn primary"));
      else {
        var b = btn("Stop", "toggle-" + w.id, function () {
          if (w.state === "stopping") return;
          if (running(w) && n) { asking = asking === w.id ? null : w.id; seen = ""; draw(last); var f = document.querySelector('[data-key="after-' + w.id + '"]'); if (f) f.focus(); return; }
          stop(w, "now"); // nothing is converting: it stops at once
        });
        if (w.state === "stopping") b.setAttribute("aria-disabled", "true");
        b.setAttribute("aria-expanded", asking === w.id ? "true" : "false"); row.appendChild(b);
      }
      c.appendChild(row);
      if (asking === w.id && n) { // the confirm under the button
        var cf = el("div", "notice warn"), d = el("div"), r2 = el("div", "row");
        cf.setAttribute("role", "group"); cf.setAttribute("aria-label", "Stop " + w.name);
        d.appendChild(el("strong", null, ui.count(n, "file", "files") + (n === 1 ? " is" : " are") + " converting."));
        r2.appendChild(btn("Stop after these files", "after-" + w.id, function () { stop(w, "after_current"); }, "btn primary small"));
        r2.appendChild(btn("Stop now", "now-" + w.id, function () { stop(w, "now"); }, "btn small"));
        d.appendChild(r2); cf.appendChild(d); c.appendChild(cf);
      }
      var fld = el("div", "field"), lb = el("label", null, "Files at once"), sel = el("select"), id = "fa-" + w.id;
      sel.id = id; lb.htmlFor = id; sel.setAttribute("data-key", id);
      [1, 2, 3, 4, 5, 6, 7, 8].forEach(function (k) { var o = el("option", null, String(k)); o.value = k; sel.appendChild(o); });
      sel.value = w.filesAtOnce || 1;
      sel.onchange = function () { act(api.setWorker({ id: w.id }, { filesAtOnce: +sel.value }), "Files at once: " + sel.value + "."); };
      fld.style.marginTop = "12px"; fld.appendChild(lb); fld.appendChild(sel); c.appendChild(fld);
    }
    // The graphics card by vendor (design 20:17Z; S2 gpu {found, vendor}); null = detection didn't run, so no line.
    if (w.gpu) c.appendChild(el("p", "small muted", w.gpu.found ? "An " + w.gpu.vendor + " graphics card found. Converting uses the processor for now." : "None found. Converting uses the processor."));
    return c;
  }

  function draw(r) {
    var j = JSON.stringify([r, asking]); if (j === seen) return; seen = j;
    var a = document.activeElement, k = a && a.getAttribute && a.getAttribute("data-key");
    // The one-time notice; its second sentence when the update changed existing Basic libraries (S4's basicFlowsChanged;
    // design's words until piece B, REDESIGN D2).
    $("update-note").hidden = !r.stoppedByUpdate; $("update-basic").hidden = !r.basicFlowsChanged;
    r.workers.forEach(function (w) { if (w.state !== "stopping") delete stoppedNow[w.id]; }); // stopped (or started again): forget the "now"
    var list = $("worker-list"); list.textContent = "";
    r.workers.forEach(function (w) { list.appendChild(card(w)); });
    // Start all / Stop all only with more than one worker (D3).
    var all = $("all"); all.textContent = ""; all.hidden = r.workers.length < 2;
    if (r.workers.length > 1) {
      all.appendChild(btn("Start all", "start-all", function () { act(Promise.all(r.workers.filter(function (w) { return w.state === "stopped"; }).map(function (w) { return api.startWorker({ id: w.id }); })), "Started."); }));
      all.appendChild(btn("Stop all", "stop-all", function () { act(Promise.all(r.workers.filter(running).map(function (w) { return api.stopWorker({ id: w.id }, { when: "after_current" }); })), "Stopping after the current files."); }));
    }
    var n = k ? document.querySelector('[data-key="' + k + '"]') : null; if (n) n.focus();
  }
  // When a worker starts or stops (a button, or a stop that finishes on its own), the top bar's "Workers stopped" is
  // read again, so it never contradicts this page.
  function states(r) { return r.workers.map(function (w) { return w.id + ":" + w.state; }).join(); }
  function refresh() { return api.listWorkers().then(function (r) { var moved = last && states(last) !== states(r); last = r; draw(r); if (moved && S.working) S.working(); }, fail); }
  // Poll every 3 s; while a worker is stopping, every 0.5 s, so "Stopping..." goes as soon as the server says stopped
  // (a Stop now answers "stopping" and is stopped about 0.2 s later; QA saw the badge linger for the 3 s poll).
  var timer = null;
  function next() { var stopping = last && last.workers.some(function (w) { return w.state === "stopping"; }); clearTimeout(timer); timer = setTimeout(tick, stopping ? 500 : 3000); }
  function tick() { (document.hidden ? Promise.resolve() : refresh()).then(null, function () {}).then(next); }

  function init() {
    $("got-it").onclick = function () { api.dismissWorkerNotice().then(function () { $("update-note").hidden = true; seen = ""; $("worker-list").querySelector("button, select") && $("worker-list").querySelector("button, select").focus(); }, fail); };
    tick();
  }
  S.ready.then(init);
})();
