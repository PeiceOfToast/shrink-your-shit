// The Dashboard's card lines that are more than a format call (D3). Pure, so the words are tested without the page; the
// formatters come in as arguments (format.ts's bytes and count) so this file imports nothing.
type Fmt = (n: number) => string;

// Space saved's big figure: the freed bytes, or "Nothing saved yet" at zero.
export const spaceHeadline = (freed: number, bytes: Fmt) => (freed > 0 ? bytes(freed) + " freed" : "Nothing saved yet");

// Space saved's foot: the 30-day total, the bytes the bars add up to.
export const spaceFoot = (total: number, bytes: Fmt) => bytes(total) + " in 30 days";

// Files converted's foot: the 30-day outcomes.
export const filesFoot = (done: number, skipped: number, failed: number, count: Fmt) =>
  `${count(done)} done · ${count(skipped)} skipped · ${count(failed)} failed in 30 days`;
