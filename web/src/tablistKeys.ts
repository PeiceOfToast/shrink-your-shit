import type { KeyboardEvent } from "react";

// Which tab a key moves to, from the tab at index `at`. Left and Right wrap around; Home and End go to the first and last
// tab. A tab with aria-disabled is skipped. Null when the key is not one of these, or no other tab can be reached.
export function nextTab(key: string, at: number, disabled: boolean[]): number | null {
  const n = disabled.length;
  if (key === "Home" || key === "End") {
    const order = Array.from({ length: n }, (_, i) => (key === "Home" ? i : n - 1 - i));
    return order.find((i) => !disabled[i]) ?? null;
  }
  const step = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
  if (!step) return null;
  for (let s = 1; s < n; s++) {
    const i = (((at + step * s) % n) + n) % n;
    if (!disabled[i]) return i;
  }
  return null;
}

// onKeyDown for a role=tablist: the keys move focus to a tab and select it, as a click does. Roving tabindex (the
// selected tab is 0, the others -1) is set where the tabs are rendered.
export function tablistKey(e: KeyboardEvent<HTMLElement>) {
  const tabs = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]'));
  const at = tabs.indexOf(e.target as HTMLElement);
  if (at < 0) return;
  const to = nextTab(e.key, at, tabs.map((t) => t.getAttribute("aria-disabled") === "true"));
  if (to === null) return;
  e.preventDefault();
  tabs[to].focus();
  tabs[to].click();
}
