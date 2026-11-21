// A typed setting may hold one whole {{name}} (W1, W2): the workflow's own variable, a server-wide one, or one
// of the file's own values. Whether a variable's kind fits the setting is the server's call (variable_wrong_type
// on save, with its words); this is the picker's hint, mirroring internal/workflow's typeMatches and
// reservedProblem so a mismatch is greyed before the save says so. Pure, so it can be read and tested alone.
import type { JSONSchema } from "./types/v2";

// The kind a variable's value has, in the schema's words: number | boolean | string (a path, an enum choice and
// text are all text to a setting); "" for a secret or an unknown type (never fits a typed setting).
export function kindOf(varType: string | undefined): "number" | "boolean" | "string" | "" {
  switch (varType) {
    case "number": return "number";
    case "bool": case "boolean": return "boolean";
    case "string": case "path": case "enum": return "string";
    default: return "";
  }
}

// Whether a variable of varType can fill setting s: a choice setting takes a kind one of its choices has; a typed
// setting takes its own type (integer and number both take a number). No type on the schema: anything but a secret.
export function refFits(s: JSONSchema, varType: string | undefined): boolean {
  const kind = kindOf(varType);
  if (!kind) return false;
  const choices = s.enum as unknown[] | undefined;
  if (choices && choices.length) return choices.some((c) => (typeof c === "number" ? "number" : typeof c === "boolean" ? "boolean" : "string") === kind);
  switch (s.type) {
    case "integer": case "number": return kind === "number";
    case "boolean": return kind === "boolean";
    case "string": return kind === "string";
    default: return true;
  }
}

// The words beside a greyed entry.
export function kindWords(varType: string | undefined): string {
  switch (kindOf(varType)) {
    case "number": return "a number";
    case "boolean": return "yes or no";
    case "string": return "text";
    default: return "a secret";
  }
}
