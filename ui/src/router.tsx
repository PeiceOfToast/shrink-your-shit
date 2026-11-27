import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent, type ReactNode } from "react";
import type { SettingsSection } from "./api/types.ts";
import { holdForLeave } from "./settings/leaveGuard.ts";

// A small router on the history API. Routes come from pages/00-screen-map.md; anything not built yet is a placeholder.

function go(to: string): void {
  if (to !== location.pathname + location.search) history.pushState(null, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

// A section with unsaved changes holds the move until the person answers "Leave without saving?".
export function navigate(to: string): void {
  if (holdForLeave(() => go(to))) return;
  go(to);
}

function subscribe(cb: () => void): () => void {
  window.addEventListener("popstate", cb);
  return () => window.removeEventListener("popstate", cb);
}

const snapshot = () => location.pathname + location.search;

export function useLocation(): { pathname: string; query: URLSearchParams } {
  const href = useSyncExternalStore(subscribe, snapshot, () => "/");
  const q = href.indexOf("?");
  return { pathname: q < 0 ? href : href.slice(0, q), query: new URLSearchParams(q < 0 ? "" : href.slice(q)) };
}

export type Route =
  | { name: "dashboard" }
  | { name: "jobs" }
  | { name: "job"; id: string }
  | { name: "jobLog"; id: string }
  | { name: "workflows" }
  | { name: "workflowPicker" }
  | { name: "workflowGuided"; id: string }
  | { name: "workflowEditor"; id: string }
  | { name: "libraries" }
  | { name: "workers" }
  | { name: "worker"; id: string }
  | { name: "library"; id: string }
  | { name: "systemLog" }
  | { name: "settings"; section: SettingsSection | null }
  | { name: "start" }
  | { name: "signin" }
  | { name: "placeholder"; title: string };

const TITLES: Record<string, string> = {
  workers: "Workers",
  settings: "Settings",
  start: "Start",
  signin: "Sign in",
  process: "Process one file",
  "system-log": "System log",
};

const SECTION_KEYS: SettingsSection[] = ["general", "appearance", "processing", "trash", "connections", "notifications", "backup", "health", "access"];

export function resolve(pathname: string): Route {
  const parts = pathname.split("/").filter(Boolean).map(decodeURIComponent);
  if (parts.length === 0) return { name: "dashboard" };
  if (parts[0] === "jobs") {
    if (parts.length === 1) return { name: "jobs" };
    if (parts.length === 2) return { name: "job", id: parts[1] };
    if (parts.length === 3 && parts[2] === "log") return { name: "jobLog", id: parts[1] };
  }
  if (parts[0] === "workflows") {
    if (parts.length === 1) return { name: "workflows" };
    if (parts.length === 2 && parts[1] === "new") return { name: "workflowPicker" };
    if (parts.length === 3 && parts[1] === "new" && parts[2] === "guided") return { name: "workflowGuided", id: "new" };
    if (parts.length === 3 && parts[1] === "new" && parts[2] === "editor") return { name: "workflowEditor", id: "new" };
    if (parts.length === 3 && parts[2] === "guided") return { name: "workflowGuided", id: parts[1] };
    if (parts.length === 2) return { name: "workflowEditor", id: parts[1] };
  }
  if (parts[0] === "workers") {
    if (parts.length === 1) return { name: "workers" };
    if (parts.length === 2) return { name: "worker", id: parts[1] };
  }
  if (parts[0] === "libraries") {
    if (parts.length === 1) return { name: "libraries" };
    if (parts.length === 2) return { name: "library", id: parts[1] };
  }
  if (parts[0] === "start" && parts.length === 1) return { name: "start" };
  if (parts[0] === "signin" && parts.length === 1) return { name: "signin" };
  if (parts[0] === "settings" && parts[1] === "log") return { name: "systemLog" };
  if (parts[0] === "settings") {
    if (parts.length === 1) return { name: "settings", section: null };
    if (parts.length === 2 && SECTION_KEYS.includes(parts[1] as SettingsSection)) return { name: "settings", section: parts[1] as SettingsSection };
  }
  return { name: "placeholder", title: TITLES[parts[0]] ?? "Not found" };
}

export function Link({
  to,
  children,
  ...rest
}: { to: string; children: ReactNode } & Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "href" | "onClick">) {
  const onClick = (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    navigate(to);
  };
  return (
    <a href={to} onClick={onClick} {...rest}>
      {children}
    </a>
  );
}
