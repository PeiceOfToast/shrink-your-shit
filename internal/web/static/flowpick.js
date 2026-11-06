/* The Flow choice on a library page. Loaded only when "Let me choose per library" is on. */
(function (g) {
  "use strict";
  var api = g.SYP.api, el = g.SYP.ui.el;
  g.SYP.flowpick = function (host, lib, say) {
    api.listFlows().then(function (r) {
      var box = el("div", "field"), lb = el("label", null, "Flow"), sel = el("select"), h = el("span", "hint");
      lb.htmlFor = sel.id = "flowsel"; h.id = "flowsel-h"; sel.setAttribute("aria-describedby", h.id);
      r.items.forEach(function (f) { var o = el("option", null, f.name); o.value = f.id; sel.appendChild(o); });
      sel.value = lib.flowId;
      h.appendChild(document.createTextNode("Pick one that already exists. New flows are made on the ")); var a = el("a", null, "Flows page"); a.href = "/flows/"; h.appendChild(a); h.appendChild(document.createTextNode("."));
      sel.onchange = function () {
        api.putLibraryFlow({ id: lib.id }, { flowId: sel.value }).then(function () { say("Now using " + sel.options[sel.selectedIndex].text + "."); location.reload(); },
          function (e) { say(e.message); sel.value = lib.flowId; });
      };
      box.appendChild(lb); box.appendChild(sel); box.appendChild(h); host.appendChild(box);
    }, function (e) { say(e.message); });
  };
})(window);
