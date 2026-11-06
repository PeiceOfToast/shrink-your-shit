/* Page shell: sign-in from ?token=, menu, theme, working-files link, settings form. */
(function () {
  "use strict";
  var api = window.SYP.api, doc = document;
  function $(id) { return doc.getElementById(id); }
  function text(el, s) { el.textContent = s; }

  // The installer's address carries ?token=...; swap it for the session cookie and
  // drop it from the address. The token is never stored by the page.
  function signIn() {
    var u = new URL(location.href), t = u.searchParams.get("token");
    if (!t) return Promise.resolve();
    u.searchParams.delete("token");
    history.replaceState(null, "", u.pathname + u.search + u.hash);
    return api.login({}, { token: t }).catch(function () {});
  }

  function theme() {
    var btn = $("theme"), root = doc.documentElement, names = ["auto", "light", "dark"], cur = "auto";
    try { cur = localStorage.getItem("syp-theme") || "auto"; } catch (e) {}
    function apply() {
      if (cur === "auto") { root.removeAttribute("data-theme"); root.style.colorScheme = ""; }
      else { root.setAttribute("data-theme", cur); root.style.colorScheme = cur; }
      text(btn, "Theme: " + cur.charAt(0).toUpperCase() + cur.slice(1));
    }
    btn.hidden = false;
    btn.addEventListener("click", function () {
      cur = names[(names.indexOf(cur) + 1) % names.length];
      try { localStorage.setItem("syp-theme", cur); } catch (e) {}
      apply();
    });
    apply();
  }

  // The server sends the Basic menu. Entries that depend on settings are added here (nav.json says when).
  function menu(s) {
    var ul = $("main-nav"), section = doc.body.getAttribute("data-section");
    if (!ul) return Promise.resolve(); // the wizard has no menu
    return fetch("/assets/nav.json").then(function (r) { return r.json(); }).then(function (items) {
      ul.textContent = "";
      items.forEach(function (n) {
        var show = !n.showAt || n.showAt.indexOf(s.controlLevel) >= 0 || (n.showWithPerLibraryChoice && s.perLibraryFlow);
        if (!show) return;
        var li = doc.createElement("li"), a = doc.createElement("a");
        a.setAttribute("data-id", n.id); a.href = n.href; a.textContent = n.label;
        if (n.id === section) a.setAttribute("aria-current", "page");
        li.appendChild(a); ul.appendChild(li);
      });
    });
  }

  function working() {
    if (!$("working")) return;
    api.activitySummary().then(function (s) {
      var el = $("working"), n = s.workingFiles;
      if (!n) { el.hidden = true; return; }
      text(el, n + (n === 1 ? " file working" : " files working")); el.hidden = false;
    }, function () {});
  }

  function generalForm(s) {
    var f = $("general-form"); if (!f) return;
    f.hidden = false;
    f.elements.controlLevel.value = s.controlLevel;
    f.elements.perLibraryFlow.checked = !!s.perLibraryFlow;
    f.addEventListener("submit", function (ev) {
      ev.preventDefault();
      var body = { controlLevel: f.elements.controlLevel.value, perLibraryFlow: f.elements.perLibraryFlow.checked };
      api.patchSettings({}, body).then(function (ns) { text($("saved"), "Saved."); return menu(ns); },
        function (e) { text($("saved"), e.message); });
    });
  }

  // First visit with nothing set up lands in the wizard.
  function firstRun(s) {
    if (s.firstRunDone || doc.body.getAttribute("data-section") !== "libraries") return Promise.resolve();
    return api.listLibraries().then(function (l) { if (!l.total) location.replace("/welcome/"); });
  }

  window.SYP.ready = signIn();
  window.SYP.ready.then(function () {
    theme();
    if (api.mocked) $("mock-note").hidden = false;
    working();
    return api.getSettings().then(function (s) {
      return firstRun(s).then(function () { return menu(s); }).then(function () { generalForm(s); });
    });
  }).catch(function () {});
})();
