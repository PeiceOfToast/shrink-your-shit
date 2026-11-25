// The words for the Overview's Converting now empty line and a library row's right side. Pure: the formatters come in as
// arguments, so the Node test runs these without the app's modules.

// Empty Converting now: workers off says how to start; workers on with nothing waiting says when the next scan is.
export function convertingEmpty(workersOn: boolean, filesWaiting: number, nextScanAt: string | null | undefined, at: (t: string) => string): string {
  if (!workersOn) return "Nothing is converting. Turn workers on to start.";
  if (filesWaiting === 0 && nextScanAt) return "Nothing is converting. Nothing is waiting; next scan " + at(nextScanAt);
  return "Nothing is converting.";
}

// A library's right side, one line: what is saved (and its share of the total), else what waits to free, else nothing.
export function libraryRight(l: { saved: number; waitingToFree: number; bytesNow: number }, nextFreeDay: string | null, bytes: (n: number) => string): string {
  if (l.saved > 0) {
    const total = l.saved + l.bytesNow, sp = Math.round((100 * l.saved) / total);
    return bytes(l.saved) + " saved · " + sp + "% of " + bytes(total);
  }
  if (l.waitingToFree > 0) return bytes(l.waitingToFree) + " smaller" + (nextFreeDay ? " · frees on " + nextFreeDay : "");
  return "Nothing saved yet";
}
