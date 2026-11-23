// The pages a person can open before the first run is done (App.tsx's guard sends every other page to /start).
// Kept apart from App.tsx so the test runs under `node --test` without a DOM.
// /libraries/<id> and /workflows/<id> are the Change links on the wizard's done cards; /workflows/new is not a route, so it is not open.
export const openBeforeFirstRun = (p: string): boolean =>
  p === "/start" || p === "/signin" || p === "/settings/about" || p === "/settings/health" || /^\/libraries\/[^/]+$/.test(p) || /^\/workflows\/(?!new$)[^/]+$/.test(p);
