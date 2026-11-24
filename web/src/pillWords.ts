// The "Not linked" words on a graph step's free ports. Plain functions, no React, so the tests can read them.
import type { StepDefinition } from "./types/v2";

// item 7: a port that isn't a success port and has no connection says so on the step
// each unlinked port says its own name in words; the error port keeps the spec's "Didn't work" line
// W9: after the save (fileOn "saved") the original is already gone for good, so the words say what really happens: the
// warning and the next steps (error, skipped), or nothing more on a question's No (branch)
export const afterSave = (p: { name: string }, def?: StepDefinition) => def?.fileOn?.[p.name] === "saved";
export const notLinked = (p: { name: string; label: string; outcome: string }, def?: StepDefinition) => {
  if (afterSave(p, def)) return p.outcome === "branch"
    ? p.label + ": not linked. The new file is already saved; nothing more happens on this path."
    : (p.outcome === "error" ? "Didn't work" : p.label) + ": not linked. The new file is already saved; a warning is shown and the next steps still run.";
  return (p.outcome === "error" ? "Didn't work" : p.label) + ": not linked. The original file is kept.";
};
// the wide pill's words (the tight pill always says "Not linked"); pillWidth measures all three
export const pillWords = (p: { name: string; outcome: string }, def?: StepDefinition) => !afterSave(p, def) ? "Not linked: keeps the original" : p.outcome === "branch" ? "Not linked: nothing more" : "Not linked: carries on";
export const PILL_WORDS = ["Not linked: keeps the original", "Not linked: carries on", "Not linked: nothing more"];
