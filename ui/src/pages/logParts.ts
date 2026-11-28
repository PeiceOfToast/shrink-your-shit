// Shared by the Job log (page 03) and the System log (page 12): the level words, copying and saving text.
import type { JobStep, LogEvent, LogLine, LogStep } from "../api/types.ts";

export type LogLevelName = "info" | "warn" | "error" | "detail";

export const LEVEL_LABEL: Record<LogLevelName, string> = { info: "INFO", warn: "WARN", error: "ERROR", detail: "DETAIL" };

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// Saves text as a .txt file; both logs' downloads use it.
export function saveText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

// The Job log's steps, built from the job's events (GET /jobs/{id}/events). Real events carry no step index, so the
// nth step_start..step_end block is steps[n-1]. Lines before the first step go at the top of step 1; lines after the
// last reached step. step_start and step_end only mark the blocks, and empty messages are skipped. Only the steps that
// were reached come back: no step_start gives none (the log says "Not started yet"), and a run that failed in step 1
// gives step 1 alone.
export function logsFromEvents(steps: JobStep[], events: LogEvent[]): LogStep[] {
  if (steps.length === 0 || !events.some((e) => e.kind === "step_start")) return [];
  const out: LogStep[] = steps.map((s) => ({ ...s, decision: "", command: null, values: [], lines: [] }));
  let block = -1; // becomes 0 at the first step_start
  for (const e of events) {
    if (e.kind === "step_start") {
      block += 1;
      continue;
    }
    if (e.kind === "step_end") continue;
    const step = out[Math.min(Math.max(block, 0), out.length - 1)];
    if (e.kind === "decision") {
      if (e.message) step.decision = e.message;
      continue;
    }
    if (e.kind === "command") {
      if (e.message) step.command = e.message;
      continue;
    }
    if (!e.message) continue;
    const level: LogLine["level"] = e.level === "warn" || e.level === "error" ? e.level : "info";
    step.lines.push({ time: e.ts, level, text: e.message });
  }
  return out.slice(0, Math.min(block + 1, out.length));
}

// The live lines the page adds. The server folds a repeated line into the newest row (its seq moves up, and its repeat says
// how many times and from when). A folded line that matches a line already shown replaces that line, so it shows once.
export function addLiveLine(lines: LogEvent[], e: LogEvent): LogEvent[] {
  if (e.repeat) {
    const at = lines.findIndex(
      (l) => l.seq !== e.seq && l.message === e.message && l.source === e.source && (l.ts === e.repeat!.firstAt || l.repeat?.firstAt === e.repeat!.firstAt),
    );
    if (at >= 0) return lines.map((l, i) => (i === at ? e : l));
  }
  return [...lines, e];
}

// What the page shows: the loaded lines, with the live lines folded in (a fold of a loaded line replaces it).
export function showLines(loaded: LogEvent[], live: LogEvent[]): LogEvent[] {
  return live.reduce(addLiveLine, loaded);
}
