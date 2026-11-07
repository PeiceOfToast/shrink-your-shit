/* The preview card under "Let me choose per library": what a library gains. Loaded only when that
   setting is on, so the page says nothing about flows otherwise. */
(function (g) {
  "use strict";
  var api = g.SYP.api, el = g.SYP.ui.el;
  g.SYP.settingsFlow = function (host, on) {
    host.textContent = "";
    if (!on) return;
    api.listFlows().then(function (r) {
      var c = el("div", "card"), f = el("div", "field"), lb = el("label", null, "Flow"), sel = el("select");
      c.setAttribute("aria-label", "Preview of a library with this on");
      c.appendChild(el("p", "small muted", "Preview: what a library gains")).style.margin = "0 0 8px";
      c.appendChild(el("strong", null, "Movies"));
      lb.htmlFor = sel.id = "pl-sample"; r.items.slice(0, 2).forEach(function (x) { sel.appendChild(el("option", null, x.name)); });
      f.appendChild(lb); f.appendChild(sel); f.style.margin = "8px 0 0"; c.appendChild(f);
      host.appendChild(c);
    }, function () {});
  };
})(window);
