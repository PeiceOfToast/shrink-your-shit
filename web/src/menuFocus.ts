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
