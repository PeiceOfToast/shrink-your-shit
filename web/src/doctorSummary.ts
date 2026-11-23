// The Health banner's words and tone, from the checks the server returned. Pure, so the node test can check it.
// absent (a hardware check that isn't there) counts nowhere.
export type HealthTone = "ok" | "warn" | "bad";

export function healthSummary(checks: { status: string }[]): { tone: HealthTone; text: string } {
  const fails = checks.filter((c) => c.status === "bad").length, warns = checks.filter((c) => c.status === "warn").length;
  if (!fails && !warns) return { tone: "ok", text: "Everything checks out." };
  const parts = [fails && (fails === 1 ? "1 check failed" : `${fails} checks failed`), warns && (warns === 1 ? "1 warning" : `${warns} warnings`)].filter(Boolean);
  return { tone: fails ? "bad" : "warn", text: parts.join(", ") + "." };
}

// Libraries: a library's ok checks always become one row, joined from the server's own ok details
// ("<name>: readable and writable." and so on). A failing or warning check keeps its own row, after the joined one,
// with its Fix. If the names don't line up, the ok checks keep one row each.
type Check = { id: string; label: string; status: "ok" | "warn" | "bad" | "absent"; detail: string; hint: string | null };
const libOf = (c: Check) => c.id.split(":").slice(0, 2).join(":");
export function libraryRows(cs: Check[]): Check[] {
  const out: Check[] = [], done = new Set<string>();
  for (const c of cs) {
    const lib = libOf(c);
    if (!lib.startsWith("library:")) { out.push(c); continue; }
    if (done.has(lib)) continue;
    done.add(lib);
    const mine = cs.filter((x) => libOf(x) === lib);
    const oks = mine.filter((x) => x.status === "ok"), rest = mine.filter((x) => x.status !== "ok");
    const name = oks.length ? oks[0].detail.slice(0, Math.max(0, oks[0].detail.indexOf(": "))) : "";
    if (oks.length && oks.every((x) => x.detail.startsWith(name + ": "))) {
      const clauses = oks.map((x) => x.detail.slice(name.length + 2).replace(/\.$/, "")).join(", ");
      out.push({ id: lib + ":ok", label: oks[0].label, status: "ok", detail: name + ": " + clauses + ".", hint: null });
    } else out.push(...oks); // one ok check, or names that don't line up: one row per check
    out.push(...rest); // a warning or failure keeps its own row, with its Fix
  }
  return out;
}

// Encoders: the encoder in use first, then the rest in the server's order, the not-found rows last. Sorts only.
const rank = (c: Check) => (c.id === "encoder:in-use" ? 0 : c.status === "absent" ? 2 : 1);
export const encoderRows = (cs: Check[]): Check[] => [...cs].sort((a, b) => rank(a) - rank(b));
