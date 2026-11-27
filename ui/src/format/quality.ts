import type { BitrateLadder } from "../api/flowTypes.ts";

// The Quality slider's number for one position, format, card and picture size. All the numbers come from the
// ladder the server sends: slider crf, plus the format offset, plus the size's row shift, plus the card offset
// for HEVC on a card. Sizes the ladder does not name (custom, keep) use the 1080p row.
export function qualityCrf(ladder: BitrateLadder, position: number, format: string, card: boolean, size: string | null): number {
  const row = ladder.resolutionRow[size ?? "1080"] ?? "1080";
  const base = (ladder.slider.find((s) => s.position === position)?.crf ?? 0) + (ladder.crfOffset[format] ?? 0) + (ladder.rowCrfShift[row] ?? 0);
  if (card && format === "hevc") return base + (ladder.cardOffsets.qsv?.[String(position)] ?? 0);
  return base;
}
