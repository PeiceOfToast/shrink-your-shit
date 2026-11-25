// The menu (DESIGN/v2/IA.md, "Navigation"): only Phase 1 pages, grouped. Badges come with their screens' data.
export interface NavItem {
  to: string;
  label: string;
  icon: string;
  planned?: boolean; // not available yet: the page says so, and the menu marks it
}
export const NAV: [string, NavItem[]][] = [
  ["Overview", [{ to: "/", label: "Dashboard", icon: "i-dashboard" }, { to: "/jobs", label: "Jobs", icon: "i-jobs" }]],
  ["Automation", [
    { to: "/workflows", label: "Workflows", icon: "i-workflow" },
    { to: "/libraries", label: "Libraries", icon: "i-library" },
  ]],
  ["Infrastructure", [{ to: "/workers", label: "Workers", icon: "i-worker" }]],
  ["System", [
    { to: "/settings/general", label: "Settings", icon: "i-settings" },
    { to: "/plugins", label: "Plugins", icon: "i-plugin", planned: true },
  ]],
];
export const PAGES = NAV.flatMap(([, items]) => items);

// The menu item a path belongs to ("/workflows/42/graph" -> Workflows); the Dashboard only for "/".
export function itemFor(path: string): NavItem | undefined {
  return PAGES.find((n) => (n.to === "/" ? path === "/" : path === n.to || path.startsWith(n.to + "/")));
}
