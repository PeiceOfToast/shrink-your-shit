// The words for the Overview's Converting now empty line and a library row's right side. Pure: the formatters come in as
// arguments, so the Node test runs these without the app's modules.

// Empty Converting now: workers off says how to start; workers on with nothing waiting says when the next scan is.
export function convertingEmpty(workersOn: boolean, filesWaiting: number, nextScanAt: string | null | undefined, at: (t: string) => string): string {
  if (!workersOn) return "Nothing is converting. Turn workers on to start.";
  if (filesWaiting === 0 && nextScanAt) return "Nothing is converting. Nothing is waiting; next scan " + at(nextScanAt);
  return "Nothing is converting.";
}

// The share of the total, in words: whole percent from 10, one decimal from 0.1, else "under 0.1%". The decimal is capped at
// 9.9, so 9.95 reads "9.9%" and never "10.0%".
export function shareText(share: number): string {
  if (share >= 10) return Math.round(share) + "%";
  if (share >= 0.1) return Math.min(Math.round(share * 10) / 10, 9.9).toFixed(1) + "%";
  return share > 0 ? "under 0.1%" : "0%";
}

// A library's right side as two parts: the green figure (the saved bytes, empty when nothing is saved) and the rest of the
// line. What is saved (with its share of the total), else what waits to free, else nothing.
export function libraryRight(l: { saved: number; waitingToFree: number; bytesNow: number }, nextFreeDay: string | null, bytes: (n: number) => string): { gain: string; rest: string } {
  if (l.saved > 0) {
    const total = l.saved + l.bytesNow;
    return { gain: bytes(l.saved), rest: " saved · " + shareText((100 * l.saved) / total) + " of " + bytes(total) };
  }
  if (l.waitingToFree > 0) return { gain: "", rest: bytes(l.waitingToFree) + " smaller" + (nextFreeDay ? " · frees on " + nextFreeDay : "") };
  return { gain: "", rest: "Nothing saved yet" };
}
