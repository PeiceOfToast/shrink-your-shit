import type { BitrateLadder } from "../api/flowTypes.ts";
import type { GpuFamily } from "../api/types.ts";

// The order the families are named in, when more than one card is found.
const FAMILY_ORDER: GpuFamily[] = ["qsv", "nvenc", "vaapi", "amf"];
const FAMILY_NAMES: Record<GpuFamily, string> = { qsv: "Intel (QSV)", nvenc: "NVIDIA (NVENC)", vaapi: "VA-API", amf: "AMD (AMF)" };

// The Quality slider's number for one position, format, card family and picture size. All the numbers come from the
// ladder the server sends: slider crf, plus the format offset, plus the size's row shift, plus the card's offset for
// its family for HEVC on a card. A family the ladder has no offsets for, and the processor, get no card offset.
// Sizes the ladder does not name (custom, keep) use the 1080p row.
export function qualityCrf(ladder: BitrateLadder, position: number, format: string, family: GpuFamily | null, size: string | null): number {
  const row = ladder.resolutionRow[size ?? "1080"] ?? "1080";
  const base = (ladder.slider.find((s) => s.position === position)?.crf ?? 0) + (ladder.crfOffset[format] ?? 0) + (ladder.rowCrfShift[row] ?? 0);
  if (family && format === "hevc") return base + (ladder.cardOffsets[family]?.[String(position)] ?? 0);
  return base;
}

// The distinct graphics-card families the workers have, in a fixed order. Workers without a card add nothing.
export function cardFamilies(workers: { gpu: { family: GpuFamily } | null }[]): GpuFamily[] {
  const found = new Set(workers.flatMap((w) => (w.gpu ? [w.gpu.family] : [])));
  return FAMILY_ORDER.filter((f) => found.has(f));
}

// The one-line meaning of a Quality setting. `families` are the card families to show a number for: pass none when
// the card is not used. Each family gets its own number (HEVC only; other formats use the processor CRF).
// `size` is the picture size the number is for (null = 1080p, the default).
export function qualityMeaning(ladder: BitrateLadder, value: number, format: string, families: GpuFamily[], size: string | null): string {
  const where = size == null ? "at 1080p (one lower at 720p and below, one higher at 2160p)" : size === "custom" ? "at the custom size" : `for ${size}p`;
  const crf = qualityCrf(ladder, value, format, null, size);
  if (format === "hevc" && families.length > 0) {
    const numbers = families.map((f) => `${FAMILY_NAMES[f]} ${qualityCrf(ladder, value, format, f, size)}`).join(", ");
    return `Uses the card's number when it can: ${numbers}; otherwise the processor CRF ${crf} ${where}. Lower means a better picture and a bigger file.`;
  }
  return `Uses the processor CRF ${crf} ${where}. Lower means a better picture and a bigger file.`;
}
