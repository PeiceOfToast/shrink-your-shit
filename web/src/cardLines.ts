// The Dashboard's card lines that are more than a format call (D3). Pure, so the words are tested without the page; the
// formatters come in as arguments (format.ts's bytes and count) so this file imports nothing.
type Fmt = (n: number) => string;

// Space saved's big figure: the freed bytes, or "Nothing saved yet" at zero.
export const spaceHeadline = (freed: number, bytes: Fmt) => (freed > 0 ? bytes(freed) + " freed" : "Nothing saved yet");

// Space saved's foot: the 30-day total, the bytes the bars add up to.
export const spaceFoot = (total: number, bytes: Fmt) => bytes(total) + " in 30 days";

// The Space line for the average new file: smaller when the average shrank, bigger when it grew, and no line at zero.
// Rounded first, so a -0.3 that rounds to 0 leaves the line out.
export const shrinkLine = (pct: number | null | undefined) => {
  const r = Math.round(pct ?? 0);
  if (r === 0) return null;
  return r > 0 ? `New files are ${r}% smaller on average` : `New files are ${-r}% bigger on average`;
};

// Files converted's foot: the 30-day outcomes.
export const filesFoot = (done: number, skipped: number, failed: number, count: Fmt) =>
  `${count(done)} done · ${count(skipped)} skipped · ${count(failed)} failed in 30 days`;
