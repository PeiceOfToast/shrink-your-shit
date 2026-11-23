import type { KeyboardEvent } from "react";

// The ⋯ menus (a details.menu): when an item is chosen, the menu closes and focus goes back to its summary, the ⋯
// button, so a keyboard user is not dropped onto the page. Used by every ⋯ menu; the outside-click and Tab-out closes
// stay as they were, because focus has already moved to whatever was clicked or tabbed to.
export function closeMenuToSummary(from: Element | null) {
  const menu = from?.closest("details");
  if (!menu) return;
  menu.removeAttribute("open");
  menu.querySelector("summary")?.focus();
}

// On a menu's details: Escape anywhere inside it closes it and returns focus to its ⋯ button.
export function menuEscape(e: KeyboardEvent<HTMLDetailsElement>) {
  if (e.key !== "Escape" || !e.currentTarget.open) return;
  e.stopPropagation();
  closeMenuToSummary(e.currentTarget);
}

// One document listener for every ⋯ menu (fix 1): Escape closes an open menu even when focus is not inside it, which
// is the case after a mouse click in Safari and Firefox on macOS (they don't focus the summary). It stops the event
// there, so a drawer or dialog behind the menu stays open; with no menu open it does nothing. It listens in the capture
// phase, so a drawer open at page load, e.g. /jobs?job=…, does not also close behind the menu.
export function installMenuEscape(): () => void {
  const esc = (e: globalThis.KeyboardEvent) => {
    if (e.key !== "Escape") return;
    const open = document.querySelectorAll<HTMLDetailsElement>("details.menu[open]");
    if (!open.length) return;
    e.stopImmediatePropagation();
    open.forEach((m) => closeMenuToSummary(m));
  };
  document.addEventListener("keydown", esc, true);
  return () => document.removeEventListener("keydown", esc, true);
}
