// Which bulk actions a Jobs tab offers. Done (completed) files are finished: nothing to cancel, nothing to select.
import type { Tab } from "./types/jobs.ts";

// Cancel only stops work that hasn't finished: waiting and running files
export const cancelOnTab = (tab: Tab) => tab === "waiting" || tab === "running";
// Select and the selection toolbar are not offered on the done tab
export const selectOnTab = (tab: Tab) => tab !== "completed";
