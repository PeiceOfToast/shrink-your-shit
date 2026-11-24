// The "Not linked" words on a graph step's free ports. Plain functions, no React, so the tests can read them.
import type { StepDefinition } from "./types/v2";

// item 7: a port that isn't a success port and has no connection says so on the step
// each unlinked port says its own name in words; the error port keeps the spec's "Didn't work" line
// W9: after the save (fileOn "saved") the original is already gone for good, so the words say what really happens: the
// warning and the next steps (error, skipped), or nothing more on a question's No (branch)
// nodeOn is the revision's fileOn for this step (port -> state). A step that has an entry is read from it alone; the
// definition's fileOn is the fallback only for a step the revision has no entry for
const portOn = (p: { name: string }, def?: StepDefinition, nodeOn?: Record<string, string>) => nodeOn !== undefined ? nodeOn[p.name] : def?.fileOn?.[p.name];
export const afterSave = (p: { name: string }, def?: StepDefinition, nodeOn?: Record<string, string>) => portOn(p, def, nodeOn) === "saved";
export const notLinked = (p: { name: string; label: string; outcome: string }, def?: StepDefinition, nodeOn?: Record<string, string>) => {
  if (afterSave(p, def, nodeOn)) return p.outcome === "branch"
    ? p.label + ": not linked. The new file is already saved; nothing more happens on this path."
    : (p.outcome === "error" ? "Didn't work" : p.label) + ": not linked. The new file is already saved; a warning is shown and the next steps still run.";
  return (p.outcome === "error" ? "Didn't work" : p.label) + ": not linked. The original file is kept.";
};
// the wide pill's words (the tight pill always says "Not linked"); pillWidth measures all three
export const pillWords = (p: { name: string; outcome: string }, def?: StepDefinition, nodeOn?: Record<string, string>) => !afterSave(p, def, nodeOn) ? "Not linked: keeps the original" : p.outcome === "branch" ? "Not linked: nothing more" : "Not linked: carries on";
export const PILL_WORDS = ["Not linked: keeps the original", "Not linked: carries on", "Not linked: nothing more"];
// a step's aria-label: its parts joined with ". ", but a part that already ends in a stop, ! or ? gets no second one (#694)
export const joinSentences = (parts: string[]) => parts.filter(Boolean).map((p) => p.trim()).reduce((a, p) => (a === "" ? p : /[.!?]$/.test(a) ? a + " " + p : a + ". " + p), "");
