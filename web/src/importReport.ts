// The lines the page shows after an import: what to fix before the imported workflow can run. Pure, so node --test
// can check the wording without a DOM.
import type { ValidationIssue } from "./types/v2";

export type ReportLine = { kind: "ok" | "warn" | "error"; text: string };
export type ImportInfo = { missingGlobals: string[]; unknownSteps: string[]; issues: ValidationIssue[] };

// secrets: the names of secret variables with no value, or null when the draft could not be read.
export function importLines(info: ImportInfo, secrets: string[] | null, where: string): ReportLine[] {
  const lines: ReportLine[] = [];
  if (info.missingGlobals.length) lines.push({ kind: "warn", text: "It uses variables this server doesn't have: " + info.missingGlobals.join(", ") + ". Add them in " + where + "." });
  if (info.unknownSteps.length) lines.push({ kind: "error", text: "It has steps this version doesn't know: " + info.unknownSteps.join(", ") + ". Replace them before it can run." });
  for (const i of info.issues) lines.push({ kind: i.severity === "error" ? "error" : "warn", text: i.message });
  if (secrets === null) lines.push({ kind: "warn", text: "Could not check its secrets." });
  else if (secrets.length) lines.push({ kind: "warn", text: "Fill in these secrets before it runs; export files never carry them: " + secrets.join(", ") + "." });
  return lines.length ? lines : [{ kind: "ok", text: "Nothing to fix." }];
}
