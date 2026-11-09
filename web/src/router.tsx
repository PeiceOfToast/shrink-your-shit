// The app's own routes under /next/ with real URLs: Back/Forward and reloads work (the Go server sends index.html for
// any /next/ path). A page with unsaved changes can ask before the app leaves it.
import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";

export const BASE = "/next";
const listeners = new Set<() => void>();
let beforeLeave: (() => void) | null = null;

const path = () => location.pathname.slice(BASE.length) || "/";
const notify = () => listeners.forEach((f) => f());
window.addEventListener("popstate", () => {
  beforeLeave?.();
  notify();
});

// The page shown now registers what to do before it is left (e.g. Appearance asks "Keep these colours?").
export function onLeave(f: (() => void) | null) {
  beforeLeave = f;
}

export function navigate(to: string) {
  beforeLeave?.();
  history.pushState(null, "", BASE + to);
  window.scrollTo(0, 0);
  notify();
}

// The path inside the app ("/", "/jobs", "/settings/appearance").
export function usePath(): string {
  return useSyncExternalStore((f) => (listeners.add(f), () => listeners.delete(f)), path);
}

export function Link({ to, onClick, ...rest }: { to: string } & AnchorHTMLAttributes<HTMLAnchorElement>) {
  const go = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return; // new tab etc.
    e.preventDefault();
    navigate(to);
  };
  return <a href={BASE + to} onClick={go} {...rest} />;
}
