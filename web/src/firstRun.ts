// The pages a person can open before the first run is done (App.tsx's guard sends every other page to /start).
// Kept apart from App.tsx so the test runs under `node --test` without a DOM.
// Settings > System (Health, Backup, About) is open, and its old About and Health addresses too. /libraries/shares is not a library.
// /libraries/<id> and /workflows/<id> are the Change links on the wizard's done cards; /workflows/new is not a route, so it is not open.
export const openBeforeFirstRun = (p: string): boolean =>
  p === "/start" || p === "/signin" || p === "/settings/system" || p === "/settings/about" || p === "/settings/health" || /^\/libraries\/(?!shares$)[^/]+$/.test(p) || /^\/workflows\/(?!new$)[^/]+$/.test(p);
