// What a format edit on a Convert step also changes (#697, pulled out of Options' put). Plain function, no React, so the tests can read it.
// AV1 takes Quality only (engine #211 refuses the rest); H.264 and VP9 can't make 10-bit, so switching to either while
// 10-bit is chosen resets it to Keep as is, in the same update. `said` is the status line: undefined = leave it alone.
export const formatEdit = (c: Record<string, unknown>, k: string, v: unknown): { x: Record<string, unknown>; said?: string } => {
  const x: Record<string, unknown> = {}, rc = String(c.rateControl ?? "quality");
  // decide from the format this edit leaves (not the saved one)
  const fmt = k === "format" ? String(v) : String(c.format ?? "hevc");
  const drop10 = k === "format" && (fmt === "h264" || fmt === "vp9") && String(c.bitDepth ?? "keep") === "10";
  if (drop10) x.bitDepth = undefined;
  if (rc.endsWith("_legacy") && k !== "rateControl") x.rateControl = fmt === "av1" || k === "quality" ? "quality" : "level";
  if (k !== "quality" && k !== "kind" && c.kind !== "selectors") x.kind = "selectors"; // a custom edit turns the slider off
  let next = k === "rateControl" ? String(v) : String(x.rateControl ?? rc);
  const av1 = fmt === "av1" && next !== "quality";
  if (av1) { x.rateControl = "quality"; next = "quality"; }
  // Quality only has no bitrate cap. A cap left in the draft is refused on Publish, and the card says nothing about it, so it goes too.
  const toQuality = k === "rateControl" ? next === "quality" : k === "format" && fmt === "av1";
  const cap = String(c.maxBitrate ?? "none");
  if (toQuality && cap !== "none" && cap !== "") x.maxBitrate = "none";
  if (av1) return { x, said: k === "format" ? "Switched to Quality only: AV1 can't use a bitrate." : "" };
  if (k === "format") return { x, said: drop10 ? "Bit depth set to Keep as is: " + (fmt === "h264" ? "H.264" : "VP9") + " can't make 10-bit." : "" };
  return { x };
};
