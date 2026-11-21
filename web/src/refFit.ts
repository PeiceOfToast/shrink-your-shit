// A typed setting may hold one whole {{name}} (W1, W2): the workflow's own variable, a server-wide one, or one
// of the file's own values. Whether the variable fits the setting is the server's call (variable_wrong_type on
// save, with its words); this is the picker's hint, mirroring internal/workflow's check: numberFor (text that
// reads as a number fills a number setting or a numeric choice), typeMatches (an integer setting takes a whole
// number) and the no-value refusal (ui-shop review #596). Pure, so it can be read and tested alone.
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

// What the picker knows of a variable: its type, its value as text (null: no value, or not known here), and
// whether it is one of the file's own values (whose value is known only when the job has the file: the server
// checks its kind alone at save, so the picker does too).
export type RefVar = { type?: string; value: string | null; file?: boolean };

// Why a variable doesn't fit setting s, in words ("" when it fits). The server's rules, in its order:
// - a secret never sets a typed setting;
// - a variable with no value yet is refused (a file value's is simply not known here, and passes on kind);
// - a number setting, or a choice of numbers, takes a number or text that reads as one (whole, for integer);
// - a yes/no setting takes yes/no; a text setting takes text (a number or yes/no won't do);
// - a choice setting takes a value that is one of its choices, when the value is known;
// - a list or group setting (array, object) never takes a variable.
export function refProblem(s: JSONSchema, v: RefVar): string {
  const kind = kindOf(v.type);
  if (!kind) return "a secret can't set this";
  if (s.type === "array" || s.type === "object") return "a list setting can't take a variable"; // typeMatches refuses a scalar there
  if (v.value == null && !v.file) return "has no value yet";
  const choices = s.enum as unknown[] | undefined;
  const numeric = s.type === "integer" || s.type === "number" || (!!choices?.length && choices.some((c) => typeof c === "number"));
  const whole = s.type === "integer";
  let value: unknown = v.value;
  if (numeric && kind !== "boolean" && v.value != null) { // numberFor: text that reads as a number is a number here
    const n = Number(v.value.trim());
    if (v.value.trim() === "" || !Number.isFinite(n)) return kind === "number" ? "isn't a number" : "text won't fit here";
    if (whole && n !== Math.trunc(n)) return "isn't a whole number";
    value = n;
  } else if (numeric && kind === "boolean") return "yes or no won't fit here";
  else if (numeric && kind === "string" && v.value == null) return ""; // a file value of text kind on a number setting: the kind check below
  if (!numeric) {
    if (s.type === "boolean" && kind !== "boolean") return kindWords(v.type) + " won't fit here";
    if (s.type === "string" && kind !== "string") return kindWords(v.type) + " won't fit here";
    // a choice of text takes text: the server compares a number with a text choice as different values (sameValue)
    if (!s.type && choices?.length && kind !== "string" && !choices.some((c) => typeof c === (kind === "boolean" ? "boolean" : "number"))) return kindWords(v.type) + " won't fit here";
  }
  if (choices?.length && v.value != null && !choices.some((c) => (typeof c === "number" ? c === value : typeof c === "boolean" ? String(c) === v.value : c === v.value))) return "isn't one of the choices";
  return "";
}

export const refFits = (s: JSONSchema, v: RefVar) => refProblem(s, v) === "";

// The words for a variable's kind.
export function kindWords(varType: string | undefined): string {
  switch (kindOf(varType)) {
    case "number": return "a number";
    case "boolean": return "yes or no";
    case "string": return "text";
    default: return "a secret";
  }
}
