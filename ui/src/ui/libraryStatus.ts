import type { Library } from "../api/types.ts";

// The status pill text and tone for a library (the same words as draft B's table). Shared by Dashboard and Libraries.
export function libraryStatus(l: Library): { kind: string; label: string } {
  switch (l.status) {
    case "watching":
      return { kind: "ok", label: "Watching" };
    case "scanning":
      return { kind: "run", label: `Scanning · ${l.scanPct ?? 0}%` };
    case "paused":
      return { kind: "warn", label: "Paused" };
    case "share_down":
      return { kind: "warn", label: "Share not connected" };
    case "needs_workflow":
      return { kind: "warn", label: "Needs a workflow" };
  }
}
