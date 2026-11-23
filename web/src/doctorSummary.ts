// The Health banner's words and tone, from the checks the server returned. Pure, so the node test can check it.
// absent (a hardware check that isn't there) counts nowhere.
export type HealthTone = "ok" | "warn" | "bad";

export function healthSummary(checks: { status: string }[]): { tone: HealthTone; text: string } {
  const fails = checks.filter((c) => c.status === "bad").length, warns = checks.filter((c) => c.status === "warn").length;
  if (!fails && !warns) return { tone: "ok", text: "Everything checks out." };
  const parts = [fails && (fails === 1 ? "1 check failed" : `${fails} checks failed`), warns && (warns === 1 ? "1 warning" : `${warns} warnings`)].filter(Boolean);
  return { tone: fails ? "bad" : "warn", text: parts.join(", ") + "." };
}
