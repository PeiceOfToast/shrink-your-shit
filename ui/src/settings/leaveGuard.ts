// Unsaved settings. A section with changes registers here; navigate() asks before it leaves, and the Settings page answers.
const changed = new Set<string>();
let asker: ((go: () => void) => void) | null = null;

export function setDirty(key: string, on: boolean): void {
  if (on) changed.add(key);
  else changed.delete(key);
}

export function hasChanges(): boolean {
  return changed.size > 0;
}

export function setAsker(fn: ((go: () => void) => void) | null): void {
  asker = fn;
}

// True when the move is held for the question. The callback runs only if the person chooses to leave.
export function holdForLeave(go: () => void): boolean {
  if (!asker || changed.size === 0) return false;
  asker(() => {
    changed.clear();
    go();
  });
  return true;
}
