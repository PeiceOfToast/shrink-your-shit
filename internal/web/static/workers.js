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
  function running(w) { return w.state === "running" || w.state === "paused"; }

  function status(w) {
    var n = (w.files || []).length;
    if (w.state === "running") return n ? ["ok", "Converting " + ui.count(n, "file", "files")] : ["ok", "Running, waiting for files"];
    if (w.state === "paused") return ["info", "Paused for quiet hours" + (w.pausedUntil ? " until " + at(w.pausedUntil) : "")];
    if (w.state === "stopping") return ["info", "Stopping after the current file..."];
    if (w.state === "unreachable") return ["bad", "Can't reach this worker"];
    return ["idle", "Stopped"];
  }
  function act(p, words) { return p.then(function () { $("err").hidden = true; if (words) say(words); asking = null; seen = ""; return refresh(); }, fail); }
  function stop(w, when) { return act(api.stopWorker({ id: w.id }, { when: when }), when === "now" ? "Stopped. The files go back to wait for their turn." : "Stopping after these files."); }

  function card(w) {
    var c = el("div", "card"), top = el("div", "row between"), left = el("div"), h = el("h2", null, w.name), st = status(w), n = (w.files || []).length;
    h.style.margin = "0"; left.appendChild(h); if (w.host) left.appendChild(el("span", "mono muted", w.host));
    top.appendChild(left); top.appendChild(el("span", "badge " + st[0], st[1])); c.appendChild(top);
    if (w.state === "running" && n) {
      var ul = el("ul", "small");
      w.files.forEach(function (f) { var li = el("li"), a = el("a", null, f.path.split(/[\\/]/).pop()); a.href = "/activity/"; li.appendChild(a); li.appendChild(document.createTextNode(" " + f.percent + "%")); ul.appendChild(li); });
      c.appendChild(ul);
    }
    if (w.state === "paused") { var q = el("p", "small"), qa = el("a", null, "Quiet hours are set in Settings > Performance."); qa.href = "/settings/performance/"; q.appendChild(qa); c.appendChild(q); }
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
      [1, 2, 3, 4].forEach(function (k) { var o = el("option", null, String(k)); o.value = k; sel.appendChild(o); });
      sel.value = w.filesAtOnce || 1;
      sel.onchange = function () { act(api.setWorker({ id: w.id }, { filesAtOnce: +sel.value }), "Files at once: " + sel.value + "."); };
      fld.style.marginTop = "12px"; fld.appendChild(lb); fld.appendChild(sel); c.appendChild(fld);
    }
    c.appendChild(el("p", "small muted", "Graphics card: " + (w.gpu ? w.gpu + ": used for converting" : "None found. The processor is used.")));
    return c;
  }

  function draw(r) {
    var j = JSON.stringify([r, asking]); if (j === seen) return; seen = j;
    var a = document.activeElement, k = a && a.getAttribute && a.getAttribute("data-key");
    $("update-note").hidden = !r.stoppedByUpdate;
    var list = $("worker-list"); list.textContent = "";
    r.items.forEach(function (w) { list.appendChild(card(w)); });
    // Start all / Stop all only with more than one worker (D3).
    var all = $("all"); all.textContent = ""; all.hidden = r.items.length < 2;
    if (r.items.length > 1) {
      all.appendChild(btn("Start all", "start-all", function () { act(Promise.all(r.items.filter(function (w) { return w.state === "stopped"; }).map(function (w) { return api.startWorker({ id: w.id }); })), "Started."); }));
      all.appendChild(btn("Stop all", "stop-all", function () { act(Promise.all(r.items.filter(running).map(function (w) { return api.stopWorker({ id: w.id }, { when: "after_current" }); })), "Stopping after the current files."); }));
    }
    var n = k ? document.querySelector('[data-key="' + k + '"]') : null; if (n) n.focus();
  }
  function refresh() { return api.listWorkers().then(function (r) { last = r; draw(r); }, fail); }

  function init() {
    $("got-it").onclick = function () { api.dismissWorkerNotice().then(function () { $("update-note").hidden = true; seen = ""; $("worker-list").querySelector("button, select") && $("worker-list").querySelector("button, select").focus(); }, fail); };
    refresh(); setInterval(function () { if (!document.hidden) refresh(); }, 3000);
  }
  S.ready.then(init);
})();
