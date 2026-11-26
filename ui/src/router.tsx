import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent, type ReactNode } from "react";

// A small router on the history API. Routes come from pages/00-screen-map.md; anything not built yet is a placeholder.

export function navigate(to: string): void {
  if (to !== location.pathname + location.search) history.pushState(null, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
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
  | { name: "workflowEditor"; id: string }
  | { name: "libraries" }
  | { name: "workers" }
  | { name: "worker"; id: string }
  | { name: "library"; id: string }
  | { name: "systemLog" }
  | { name: "placeholder"; title: string };

const TITLES: Record<string, string> = {
  workers: "Workers",
  settings: "Settings",
  start: "Start",
  signin: "Sign in",
  process: "Process one file",
  "system-log": "System log",
};

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
