import { LANGUAGES } from "./languages.ts";

// Language codes: stored and sent as the three-letter bibliographic code (eng, ger, fre, jpn, chi).
// A person or an older setting may type the two-letter code or the other three-letter form; those are synonyms.
const SYNONYM: Record<string, string> = {
  en: "eng",
  de: "ger", deu: "ger",
  fr: "fre", fra: "fre",
  es: "spa",
  it: "ita",
  pt: "por",
  nl: "dut", nld: "dut",
  sv: "swe",
  no: "nor", nb: "nor",
  da: "dan",
  fi: "fin",
  is: "ice", isl: "ice",
  pl: "pol",
  cs: "cze", ces: "cze",
  sk: "slo", slk: "slo",
  hu: "hun",
  ro: "rum", ron: "rum",
  bg: "bul",
  ja: "jpn",
  zh: "chi", zho: "chi",
};

// The canonical code for what was typed ("en" and "eng" both give "eng"; unknown input is returned trimmed and lower-cased).
export function normalizeLanguage(input: string): string {
  const c = input.trim().toLowerCase();
  return SYNONYM[c] ?? c;
}

// The name a person reads. Unknown codes fall back to what was typed.
// orig and und are audio-only choices: the film's own language, and an unlabelled track.
export function languageName(input: string): string {
  const code = normalizeLanguage(input);
  if (code === "orig") return "Original language";
  if (code === "und") return "Unknown";
  return LANGUAGES.find((l) => l.code === code)?.name ?? input.trim();
}

export function sameLanguage(a: string, b: string): boolean {
  return normalizeLanguage(a) === normalizeLanguage(b);
}
