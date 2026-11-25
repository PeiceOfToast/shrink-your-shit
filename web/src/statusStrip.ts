// The Dashboard's Status strip (dashboard cards proposal §1): the state word and the facts a person checks.
// Pure, so the rules are tested without the page. A field left out (undefined) is not shown; a field that is
// null is said as such, where the proposal says so (no file done yet).
export interface StripWorker { name: string; status: string }
export type StateTone = "ok" | "warn" | "bad" | "muted";
// A fact is plain words, or words with its value (the number or time, which the page shows in bold).
export type Fact = string | { pre: string; value: string; post: string };

// Not responding beats offline beats converting; workers on with files waiting is Waiting; workers on with nothing
// waiting is Idle; no worker on at all is Off.
export function stateWord(workers: StripWorker[], filesWaiting: number): { word: string; tone: StateTone } {
  const has = (s: string) => workers.some((w) => w.status === s);
  if (has("stalled")) return { word: "Not responding", tone: "bad" };
  if (has("offline")) return { word: "Some workers offline", tone: "warn" };
  if (has("busy")) return { word: "Converting", tone: "ok" };
  if (workers.some((w) => w.status === "online" || w.status === "draining")) {
    return filesWaiting > 0 ? { word: "Waiting", tone: "muted" } : { word: "Idle", tone: "muted" };
  }
  return { word: "Off", tone: "muted" };
}

// "2 of 3 workers busy", and the first worker that is not ready (named, linked to Workers by the page).
export function workersFact(workers: StripWorker[]): { text: Fact; notReady: string | null } | null {
  if (workers.length === 0) return null;
  const busy = workers.filter((w) => w.status === "busy").length;
  const down = workers.filter((w) => w.status === "offline" || w.status === "stalled");
  const first = down[0];
  const notReady = first ? `${first.name} is ${first.status === "stalled" ? "not responding" : "offline"}${down.length > 1 ? ` and ${down.length - 1} more` : ""}` : null;
  const of = workers.length === 1 ? "worker" : "workers";
  return { text: { pre: "", value: `${busy} of ${workers.length}`, post: ` ${of} busy` }, notReady };
}

// the same words and digits as format.ts plural() and count(); no import, so this module's tests run as they are
export function filesFact(n: number): Fact {
  return { pre: "", value: n.toLocaleString("en-US"), post: n === 1 ? " file waiting" : " files waiting" };
}

// undefined: leave the fact out. null: "no file done yet". Otherwise "4 min ago" within the hour, else the day and
// time from `when` (the page passes dayClock).
export function lastDoneFact(t: string | null | undefined, now: Date, when: (t: string, now: Date) => string): Fact | undefined {
  if (t === undefined) return undefined;
  if (t === null) return "no file done yet";
  const mins = Math.floor((now.getTime() - new Date(t).getTime()) / 6e4);
  if (mins < 1) return "last file done just now";
  if (mins < 60) return { pre: "last file done ", value: `${mins} min ago`, post: "" };
  return { pre: "last file done ", value: when(t, now), post: "" };
}

// undefined (absent or null): leave the fact out. `at` is the page's clock (format.ts clock).
export function nextScanFact(t: string | null | undefined, at: (t: string) => string): Fact | undefined {
  return t ? { pre: "next scan ", value: at(t), post: "" } : undefined;
}

// The strip's last fact when the Needs attention card has nothing to show (the page works that out from its notes).
export function nothingFact(needsNone: boolean): Fact | undefined {
  return needsNone ? "Nothing needs you" : undefined;
}

// Until the Get started steps are done, the strip says what is missing instead of the usual facts (null: they are done).
export function freshFact(libraries: number, stepsDone: boolean): string | null {
  if (stepsDone) return null;
  return libraries === 0 ? "Add a library to start" : "nothing converts until you start a worker";
}

// A library that can't run (paused, no workflow, share not connected) is a Needs attention row, so Nothing needs you waits.
export function allLibrariesRunning(states: string[]): boolean {
  return states.every((s) => s === "active");
}
