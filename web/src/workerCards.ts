// M4: the pure rules behind the Workers cards: the System line, the Graphics card line, the usage bars and the encoder
// chips. No React and no imports here, so Node can test them. Only what the server reports; a missing value adds no part.
// Callers pass memory as text (already formatted); the helpers only join the parts.

// The one OS map: the list card's System row and the Jobs join card both read it.
export const OS_NAMES: Record<string, string> = { linux: "Linux", darwin: "macOS", windows: "Windows" };

// "amd64 host · Linux · x86_64 · 32 GB": the host, the OS, the architecture and the memory the worker reports. No core count: the API has none.
export function systemLine(host: string | undefined, os: string | undefined, arch: string | undefined, memory: string): string {
  return [host, OS_NAMES[os ?? ""], arch, memory].filter(Boolean).join(" · ");
}

// "NVIDIA · 8.0 GB". An empty list is not "no card": the server sends [] when it cannot read the GPUs and for a remote worker,
// so it reads "Not reported". A card with no name and no memory adds nothing, so no empty part or doubled label ever
// reaches the screen (the old live line printed "GPU · · GPU 34%" for a card with an empty model).
export function graphicsLine(gpus: { model: string; vram: string }[]): string {
  const named = gpus.map((g) => [g.model.trim(), g.vram].filter(Boolean).join(" · ")).filter(Boolean);
  return named.length ? named.join(", ") : gpus.length ? "Unnamed" : "Not reported";
}

// The thin bars under the list: Processor always when the server reads it, Graphics card only when it reads the busiest card.
export function usageBars(cpuPercent: number | null | undefined, gpuPercent: number | null | undefined): { label: string; pct: number }[] {
  const out: { label: string; pct: number }[] = [];
  if (cpuPercent != null) out.push({ label: "Processor", pct: Math.round(cpuPercent) });
  if (gpuPercent != null) out.push({ label: "Graphics card", pct: Math.round(gpuPercent) });
  return out;
}

// The usage bars from the live reading. The Graphics card bar needs a number, and it shows when the worker lists a card
// (w.gpus, empty until V4) or the reading is above 0. A 0 with no listed card is no GPU reading, so no bar.
export function workerBars(cpuPercent: number | null | undefined, listedGpus: unknown[], liveGpu: { utilPercent: number | null }[]): { label: string; pct: number }[] {
  const util = liveGpu[0]?.utilPercent ?? null;
  return usageBars(cpuPercent, util != null && (listedGpus.length > 0 || util > 0) ? util : null);
}

// One encoder type's chip: "Processor · works · used first" (accent), "Processor · works" (green), "NVIDIA · not found" (muted).
export function encoderChip(name: string, lit: boolean, primary: boolean): { text: string; tone: "accent" | "ok" | "muted" } {
  if (!lit) return { text: name + " · not found", tone: "muted" };
  if (primary) return { text: name + " · works · used first", tone: "accent" };
  return { text: name + " · works", tone: "ok" };
}

// The formats chip: only the formats a passed test proved; empty when there is no such data.
export function formatsChip(formats: [string, boolean][]): string {
  return formats.filter(([, on]) => on).map(([f]) => f).join(" · ");
}

// M5f fix 2: the wizard's graphics chips. A vendor found gets its chip; when none is found, one "none found" chip stands for them all.
export function graphicsChips(gpus: { name: string; lit: boolean; primary: boolean }[]): { text: string; tone: "accent" | "ok" | "muted" }[] {
  const found = gpus.filter((g) => g.lit);
  return found.length ? found.map((g) => encoderChip(g.name, true, g.primary)) : [{ text: "Graphics card · none found", tone: "muted" }];
}
