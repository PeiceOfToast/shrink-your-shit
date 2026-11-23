// The Libraries form's rules, kept apart from Libraries.tsx so the tests run under `node --test` without a DOM.

// A new library, or a saved one whose folder was moved, must pass the folder check before it can be saved.
export const folderNeedsPass = (isNew: boolean, moved: boolean): boolean => isNew || moved;

// The Save / Add library button: idle, something changed, a name and a workflow, and the folder passed when it must.
export const saveEnabled = (x: { busy: boolean; changed: boolean; name: string; pick: string; needsPass: boolean; folderOk: boolean }): boolean =>
  !x.busy && x.changed && x.name.trim() !== "" && x.pick !== "" && (!x.needsPass || x.folderOk);

// The Files in this folder header: "the first 5 of 1,600" once the folder holds more than the five shown; nothing when it doesn't.
export const filesHead = (shown: number, total: number): string | null => (total > shown && shown > 0 ? "the first " + shown + " of " + total.toLocaleString("en-US") : null);
