import { useEffect, type KeyboardEvent, type RefObject } from "react";

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

// A menu that stays open until something outside it is clicked (the phone's More actions menu). A press outside closes
// it without moving focus: the press has already put focus where the user clicked.
export function useCloseOnOutside(ref: RefObject<HTMLDetailsElement | null>) {
  useEffect(() => {
    const outside = (e: MouseEvent) => {
      const d = ref.current;
      if (d?.open && !d.contains(e.target as Node)) d.removeAttribute("open");
    };
    document.addEventListener("mousedown", outside);
    return () => document.removeEventListener("mousedown", outside);
  }, [ref]);
}

// One document listener for every ⋯ menu (fix 1): Escape closes an open menu even when focus is not inside it, which
// is the case after a mouse click in Safari and Firefox on macOS (they don't focus the summary). It stops the event
// there, so a drawer or dialog behind the menu stays open; with no menu open it does nothing. It listens in the capture
// phase because the drawer's handler does not stop propagation: without capture, Escape would close the drawer instead
// of the menu. A modal with focus inside it (any aria-modal dialog: palette, folder picker, confirm, drawer) that does
// not hold the open menu gets Escape first, so this listener leaves it alone.
export function installMenuEscape(): () => void {
  const esc = (e: globalThis.KeyboardEvent) => {
    if (e.key !== "Escape") return;
    const open = document.querySelectorAll<HTMLDetailsElement>("details.menu[open]");
    if (!open.length) return;
    const modal = (document.activeElement as HTMLElement | null)?.closest('[aria-modal="true"]');
    if (modal && !Array.from(open).some((m) => modal.contains(m))) return;
    e.stopImmediatePropagation();
    open.forEach((m) => closeMenuToSummary(m));
  };
  document.addEventListener("keydown", esc, true);
  return () => document.removeEventListener("keydown", esc, true);
}
