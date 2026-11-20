/* Variant switching for the designs. ?theme=dark|light sets data-theme on <html>; without it the system setting wins.
   ?state=<name> shows the matching [data-state] block of a screen and hides the others (the first one is the default).
   Nothing here changes the look; the stylesheet does that. */
(function () {
  var q = new URLSearchParams(location.search), theme = q.get('theme');
  var root = document.documentElement;
  if (theme === 'light' || theme === 'dark') root.setAttribute('data-theme', theme);
  else if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) root.setAttribute('data-light', '');
  function pick() {
    var blocks = document.querySelectorAll('[data-state]'); if (!blocks.length) return;
    var want = q.get('state') || blocks[0].getAttribute('data-state'), hit = false;
    blocks.forEach(function (b) { var on = b.getAttribute('data-state') === want; b.hidden = !on; if (on) hit = true; });
    if (!hit) blocks[0].hidden = false;
    document.querySelectorAll('[data-state-link]').forEach(function (a) { a.setAttribute('aria-pressed', a.getAttribute('data-state-link') === want ? 'true' : 'false'); });
    if (want === 'empty' || want === 'loading') document.querySelectorAll('.activity').forEach(function (a) { a.remove(); });
    var t = document.querySelector('title'); if (t && want) t.textContent = t.textContent.replace(/ · .*$/, '') + ' · ' + want;
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', pick); else pick();
})();
