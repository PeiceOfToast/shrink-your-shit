import type { BitrateLadder } from "../api/flowTypes.ts";

// The graphics card's family, as the ladder names its offsets. null is the processor (no card, or None chosen).
export type CardFamily = "qsv" | "nvenc" | "vaapi" | "amf";

// The Quality slider's number for one position, format, card family and picture size. All the numbers come from the
// ladder the server sends: slider crf, plus the format offset, plus the size's row shift, plus the card's offset for
// its family for HEVC on a card. A family the ladder has no offsets for, and the processor, get no card offset.
// Sizes the ladder does not name (custom, keep) use the 1080p row.
export function qualityCrf(ladder: BitrateLadder, position: number, format: string, family: CardFamily | null, size: string | null): number {
  const row = ladder.resolutionRow[size ?? "1080"] ?? "1080";
  const base = (ladder.slider.find((s) => s.position === position)?.crf ?? 0) + (ladder.crfOffset[format] ?? 0) + (ladder.rowCrfShift[row] ?? 0);
  if (family && format === "hevc") return base + (ladder.cardOffsets[family]?.[String(position)] ?? 0);
  return base;
}
