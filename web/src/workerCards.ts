// M4: the pure rules behind the Workers cards: the System line, the Graphics card line, the usage bars and the encoder
// chips. No React and no imports here, so Node can test them. Only what the server reports; a missing value adds no part.
// Callers pass memory as text (already formatted); the helpers only join the parts.

// "Linux · x86_64 · 32 GB": the OS, the architecture and the memory the worker reports. No core count: the API has none.
export function systemLine(os: string | undefined, arch: string | undefined, memory: string): string {
  const OS: Record<string, string> = { linux: "Linux", darwin: "macOS", windows: "Windows" };
  return [OS[os ?? ""], arch, memory].filter(Boolean).join(" · ");
}

// "NVIDIA · 8.0 GB", "None found". A card with no name and no memory adds nothing, so no empty part or doubled label ever
// reaches the screen (the old live line printed "GPU · · GPU 34%" for a card with an empty model).
export function graphicsLine(gpus: { model: string; vram: string }[]): string {
  const named = gpus.map((g) => [g.model.trim(), g.vram].filter(Boolean).join(" · ")).filter(Boolean);
  return named.length ? named.join(", ") : gpus.length ? "Unnamed" : "None found";
}

// The thin bars under the list: Processor always when the server reads it, Graphics card only when it reads the busiest card.
export function usageBars(cpuPercent: number | null | undefined, gpuPercent: number | null | undefined): { label: string; pct: number }[] {
  const out: { label: string; pct: number }[] = [];
  if (cpuPercent != null) out.push({ label: "Processor", pct: Math.round(cpuPercent) });
  if (gpuPercent != null) out.push({ label: "Graphics card", pct: Math.round(gpuPercent) });
  return out;
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
