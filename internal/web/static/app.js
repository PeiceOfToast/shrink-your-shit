/* Page shell: sign-in from ?token=, menu, theme, working-files link, update notice. */
(function () {
  "use strict";
  var api = window.SYP.api, doc = document;
  function $(id) { return doc.getElementById(id); }
  function text(el, s) { el.textContent = s; }

  // The installer's address carries ?token=...; swap it for the session cookie and
  // drop it from the address first. The token is never stored by the page.
  function signIn() {
    var u = new URL(location.href), t = u.searchParams.get("token");
    if (!t) return Promise.resolve();
    u.searchParams.delete("token");
    history.replaceState(null, "", u.pathname + u.search + u.hash);
    return api.login({}, { token: t }).catch(function (e) { screen(e && e.status === 401 ? "bad_token" : "offline"); return never; });
  }

  // The sign-in screen (FIRST-RUN.md#sign-in), in place of the page and without the menu. The address stays,
  // so signing in reloads the page the person asked for.
  var never = new Promise(function () {}), shown = false;
  function screen(code) {
    if (shown) return; shown = true;
    var main = $("main"), el = function (tag, txt, cls) { var n = doc.createElement(tag); if (txt) n.textContent = txt; if (cls) n.className = cls; return n; };
    Array.prototype.forEach.call(main.children, function (c) { c.hidden = true; }); // left in place, hidden, so page scripts still find their parts
    ["main-nav", "working"].forEach(function (id) { if ($(id)) $(id).hidden = true; });
    var box = el("div", null, "stack"), f = el("form", null, "stack"), fld = el("div", null, "field"), row = el("div", null, "row"),
      lb = el("label", "Access token"), inp = el("input"), show = el("button", "Show", "btn"), err = el("p", null, "hint"), go = el("button", "Sign in", "btn primary"), help = el("ul");
    box.style.cssText = "max-width:32rem;margin:0 auto";
    inp.id = "signin-token"; inp.type = "password"; inp.autocomplete = "current-password"; inp.spellcheck = false; inp.size = 30; lb.htmlFor = inp.id;
    show.type = "button"; show.setAttribute("aria-controls", inp.id);
    show.onclick = function () { var on = inp.type === "password"; inp.type = on ? "text" : "password"; show.textContent = on ? "Hide" : "Show"; };
    err.id = "signin-err"; err.setAttribute("role", "alert"); err.style.color = "var(--bad)"; inp.setAttribute("aria-describedby", err.id);
    function say(c) { err.textContent = !c ? "" : c === "empty" ? "Paste the access token first." : c === "bad_token" ? "That token doesn't match this server. Check you copied all of it." : "We couldn't reach the server. Check it is running, then try again."; }
    say(code);
    row.appendChild(inp); row.appendChild(show); fld.appendChild(lb); fld.appendChild(row); fld.appendChild(err);
    f.appendChild(fld); f.appendChild(el("div", null, "row")).appendChild(go);
    [["Linux", "sudo shrink-your-shit token", ""], ["Mac", "shrink-your-shit token", "in Terminal"], ["Windows", "shrink-your-shit token", "in Command Prompt or PowerShell"],
     ["Docker", "docker exec <container name> shrink-your-shit token", "use your container's name"]].forEach(function (c) {
      var li = el("li", c[0] + ": "), code = el("code", c[1]); code.style.userSelect = "all"; // one click selects the whole line to copy
      li.appendChild(code); if (c[2]) li.appendChild(doc.createTextNode(" (" + c[2] + ")")); help.appendChild(li);
    });
    box.appendChild(el("h1", "Sign in")); box.appendChild(el("p", "Paste the access token for this shrink-your-shit server.")); box.appendChild(f);
    box.appendChild(el("h2", "Where do I find it?"));
    box.appendChild(el("p", "It was shown at the end of the install, as part of the web address (the part after ?token=)."));
    box.appendChild(el("p", "Or see it again on the computer running shrink-your-shit. Run the line for that computer:")); box.appendChild(help);
    box.appendChild(el("p", "It prints the token and a sign-in link you can open directly."));
    main.insertBefore(box, main.firstChild); doc.title = "Sign in - shrink-your-shit"; inp.focus();
    f.onsubmit = function (ev) {
      ev.preventDefault();
      var t = inp.value.trim(); if (!t) { say("empty"); inp.focus(); return; }
      api.login({}, { token: t }).then(function () { inp.value = ""; location.reload(); },
        function (e) { say(e && e.status === 401 ? "bad_token" : "offline"); inp.focus(); inp.select(); });
    };
  }
  window.SYP.needSignIn = function () { screen(""); };

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
      // Files waiting and no worker running (redesign D4): say so, with the way to start one.
      if (!n && s.workersRunning === 0 && s.waitingFiles > 0) { text(el, "Workers stopped"); el.href = "/workers/"; el.hidden = false; return; }
      if (!n) { el.hidden = true; return; }
      text(el, n + (n === 1 ? " file working" : " files working")); el.href = "/activity/"; el.hidden = false;
    }, function () {});
  }

  // Until first-run is finished, every page opens it, with or without libraries (FIRST-RUN.md "Entry").
  // Settings > System stays reachable, because a step 3 blocker links to its health report: there it
  // has no menu, only "Back to setup". Returns false when the page is being left.
  function firstRun(s) {
    if (s.firstRunDone || location.pathname === "/welcome/") return true;
    if (location.pathname === "/settings/system/") { setupOnly(); return true; }
    location.replace(welcome());
    return false;
  }
  function welcome() { var m = /[?&](mock=[01])/.exec(location.search); return "/welcome/" + (m ? "?" + m[1] : ""); }
  function setupOnly() {
    var nav = $("main-nav"), tabs = doc.querySelector("nav.tabs"), p = doc.createElement("p"), a = doc.createElement("a");
    if (nav) nav.parentNode.hidden = true;
    if (tabs) tabs.hidden = true;
    if ($("working")) $("working").remove();
    doc.querySelector(".brand").href = a.href = welcome(); a.textContent = "Back to setup"; p.appendChild(a);
    $("main").insertBefore(p, $("mock-note").nextSibling);
  }

  // A new version: one line on every page that links to Settings > System, which says how to update.
  function update(s) {
    if (!s.updateAvailable || !$("main-nav") || location.pathname === "/settings/system/") return;
    var n = doc.createElement("div"), a = doc.createElement("a");
    n.className = "notice info"; n.setAttribute("role", "note"); a.href = "/settings/system/"; a.textContent = "See how to update";
    n.appendChild(doc.createTextNode("Version " + s.updateAvailable.version + " is available. ")); n.appendChild(a);
    $("main").insertBefore(n, $("mock-note").nextSibling);
  }

  window.SYP.menu = menu;
  // Pages start once the session is known: a 401 here shows the sign-in screen and the page never starts.
  theme();
  window.SYP.ready = signIn().then(function () {
    return api.getSettings().then(function (s) {
      if (!firstRun(s)) return never; // on its way to first-run: this page never starts
      if (api.mocked) $("mock-note").hidden = false;
      if (!s.firstRunDone) return; // Settings > System during setup: no menu, no notices
      working(); update(s); menu(s).catch(function () {});
    }, function (e) { return e && e.status === 401 ? never : null; });
  });
})();
