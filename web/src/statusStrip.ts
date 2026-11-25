// The Dashboard's Status strip (dashboard cards proposal §1): the state word and the facts a person checks.
// Pure, so the rules are tested without the page. A field left out (undefined) is not shown; a field that is
// null is said as such, where the proposal says so (no file done yet).
export interface StripWorker { name: string; status: string }
export type StateTone = "ok" | "warn" | "bad" | "muted";

// Not responding beats offline beats converting beats idle; no worker on at all is Off.
export function stateWord(workers: StripWorker[]): { word: string; tone: StateTone } {
  const has = (s: string) => workers.some((w) => w.status === s);
  if (has("stalled")) return { word: "Not responding", tone: "bad" };
  if (has("offline")) return { word: "Some workers offline", tone: "warn" };
  if (has("busy")) return { word: "Converting", tone: "ok" };
  if (workers.some((w) => w.status === "online" || w.status === "draining")) return { word: "Idle", tone: "muted" };
  return { word: "Off", tone: "muted" };
}

// "2 of 3 workers busy", and the first worker that is not ready (named, linked to Workers by the page).
export function workersFact(workers: StripWorker[]): { text: string; notReady: string | null } | null {
  if (workers.length === 0) return null;
  const busy = workers.filter((w) => w.status === "busy").length;
  const down = workers.filter((w) => w.status === "offline" || w.status === "stalled");
  const first = down[0];
  const notReady = first ? `${first.name} is ${first.status === "stalled" ? "not responding" : "offline"}${down.length > 1 ? ` and ${down.length - 1} more` : ""}` : null;
  return { text: `${busy} of ${workers.length} ${workers.length === 1 ? "worker" : "workers"} busy`, notReady };
}

// the same words and digits as format.ts plural() and count(); no import, so this module's tests run as they are
export function filesFact(n: number): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? "file waiting" : "files waiting"}`;
}

// undefined: leave the fact out. null: "no file done yet". Otherwise "4 min ago" within the hour, else the day and
// time from `when` (the page passes dayClock).
export function lastDoneFact(t: string | null | undefined, now: Date, when: (t: string, now: Date) => string): string | undefined {
  if (t === undefined) return undefined;
  if (t === null) return "no file done yet";
  const mins = Math.floor((now.getTime() - new Date(t).getTime()) / 6e4);
  if (mins < 1) return "last file done just now";
  if (mins < 60) return `last file done ${mins} min ago`;
  return `last file done ${when(t, now)}`;
}

// undefined (absent or null): leave the fact out. `at` is the page's clock (format.ts clock).
export function nextScanFact(t: string | null | undefined, at: (t: string) => string): string | undefined {
  return t ? "next scan " + at(t) : undefined;
}

// The strip's last fact when the Needs attention card has nothing to show (the page works that out from its notes).
export function nothingFact(needsNone: boolean): string | undefined {
  return needsNone ? "Nothing needs you" : undefined;
}
