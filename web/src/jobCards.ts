// M3c: the pure rules behind the Jobs cards: the selection after a filter, tab or search change, and the running card's
// fields. No React here, so Node can test them.
import type { Progress } from "./types/jobs";

// "about 12 min" from seconds left; under a minute reads "under a minute"
export function etaWords(s: number): string {
  return s < 60 ? "under a minute" : "about " + Math.round(s / 60) + " min";
}

// The running card's five fields from the live progress. A value the worker has not sent reads "—", never a made-up one.
export function runFields(p: Progress): { step: string; fps: string; speed: string; timeLeft: string; bitrate: string } {
  return {
    step: p.stage || "—",
    fps: p.fps != null && p.fps > 0 ? p.fps.toFixed(1) : "—",
    speed: p.speed != null && p.speed > 0 ? p.speed.toFixed(1) + "×" : "—",
    timeLeft: p.etaS != null && p.etaS > 0 ? etaWords(p.etaS) : "—",
    bitrate: p.bitrateKbps != null ? Math.round(p.bitrateKbps).toLocaleString("en-US") + " kbps" : "—",
  };
}

// The running card's side note: how long it has been converting, "running 12 min" (whole minutes, rounded down)
export function runningFor(elapsedS: number): string {
  return "running " + (elapsedS < 60 ? "under a minute" : Math.floor(elapsedS / 60) + " min");
}
