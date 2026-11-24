// W11: the words of a refused order, for the bar and the status region. The server's sentence keeps its meaning; the
// lead-in says why, and the draft note is said once, here: "This order can't run yet: 'Audio' must come after
// 'Convert video'. It's saved as a draft until it can."
export function refusalText(m: string): string {
  const parts = m.split(/(?<=\.)\s+/), cut = (parts.length > 1 ? parts.slice(0, -1).join(" ") : m).replace(/\.?$/, ".");
  return `This order can't run yet: ${cut} It's saved as a draft until it can.`;
}
