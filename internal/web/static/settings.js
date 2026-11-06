/* Settings > General: control level and per-library choices. */
(function () {
  "use strict";
  var S = window.SYP, api = S.api;
  function $(id) { return document.getElementById(id); }
  function text(el, s) { el.textContent = s; }
  function init(s) {
    var f = $("general-form"); if (!f) return;
    f.hidden = false;
    f.elements.controlLevel.value = s.controlLevel;
    f.elements.perLibraryFlow.checked = !!s.perLibraryFlow;
    f.addEventListener("submit", function (ev) {
      ev.preventDefault();
      var body = { controlLevel: f.elements.controlLevel.value, perLibraryFlow: f.elements.perLibraryFlow.checked };
      api.patchSettings({}, body).then(function (ns) { text($("saved"), "Saved."); return S.menu(ns); },
        function (e) { text($("saved"), e.message); });
    });
  }

  S.ready.then(function () { return api.getSettings(); }).then(init).catch(function () {});
})();
