// M3c: the pure rules behind the Jobs cards: the selection after a filter, tab or search change, and the running card's
// fields. No React here, so Node can test them.
import type { Progress } from "./types/jobs";

// "about 12 min" from seconds left; under a minute reads "under a minute"
export function etaWords(s: number): string {
  return s < 60 ? "under a minute" : "about " + Math.round(s / 60) + " min";
}

// The running card's four fields from the live progress. A value the worker has not sent reads "—", never a made-up one.
export function runFields(p: Progress): { step: string; speed: string; timeLeft: string; bitrate: string } {
  return {
    step: p.stage || "—",
    speed: p.speed != null && p.speed > 0 ? p.speed.toFixed(1) + "×" : "—",
    timeLeft: p.etaS != null && p.etaS > 0 ? etaWords(p.etaS) : "—",
    bitrate: p.bitrateKbps != null ? Math.round(p.bitrateKbps).toLocaleString("en-US") + " kbps" : "—",
  };
}
