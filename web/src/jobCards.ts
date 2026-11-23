// M3c: the pure rules behind the Jobs cards: the selection after a filter, tab or search change, and the running card's
// fields. No React here, so Node can test them.
import type { Progress } from "./types/jobs";

// Select mode keeps only the rows still shown: the selection after a tab, library or search change is its intersection
// with the rows on screen (the order of the selection is kept).
export function keepShownIds(sel: string[], shownIds: string[]): string[] {
  const shown = new Set(shownIds);
  return sel.filter((id) => shown.has(id));
}

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
