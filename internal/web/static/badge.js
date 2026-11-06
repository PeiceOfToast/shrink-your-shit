/* Library status badge (Libraries and Library pages only, so first-run does not carry it). */
(function (g) {
  "use strict";
  var ui = g.SYP.ui;
  // Status badge for a library, from its state. Words as well as colour.
  ui.stateBadge = function (l) {
    var cls = "idle", txt = "Idle";
    if (l.state === "working") { cls = "ok"; txt = "Working: " + l.workingFiles + (l.workingFiles === 1 ? " file" : " files"); }
    else if (l.state === "needs_attention") { cls = "warn"; txt = "Needs attention"; }
    else if (l.state === "paused") txt = "Paused";
    else if (l.state === "not_started") { cls = "info"; txt = "Not started"; }
    else if (l.nextScan) txt = "Idle - next scan " + new Date(l.nextScan).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    return ui.el("span", "badge " + cls, txt);
  };
})(window);
