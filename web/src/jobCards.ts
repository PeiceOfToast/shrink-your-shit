// M3c: the pure rules behind the Jobs cards: the selection after a filter, tab or search change, and the running card's
// fields. No React here, so Node can test them.
import type { Progress } from "./types/jobs";

// "about 12 min" from seconds left; under a minute reads "under a minute". From 90 min up it reads in hours: "about 1.5 h"
// (nearest half hour) under 10 h, "about 14 h" (whole hours) from 10 h.
export function etaWords(s: number): string {
  if (s < 60) return "under a minute";
  if (s < 5400) return "about " + Math.round(s / 60) + " min";
  if (s < 36000) { const h = Math.round(s / 1800) / 2; return "about " + (Number.isInteger(h) ? h : h.toFixed(1)) + " h"; }
  return "about " + Math.round(s / 3600) + " h";
}

// The running card's five fields from the live progress. A value the worker has not sent reads "—", never a made-up one.
export function runFields(p: Progress): { step: string; fps: string; speed: string; timeLeft: string; bitrate: string } {
  return {
    step: p.stage || "—",
    fps: p.fps != null && p.fps > 0 ? p.fps.toFixed(1) : "—",
    speed: p.speed != null && p.speed > 0 ? (p.speed < 0.05 ? "<0.1×" : p.speed.toFixed(1) + "×") : "—",
    timeLeft: p.etaS != null && p.etaS > 0 ? etaWords(p.etaS) : "—",
    bitrate: p.bitrateKbps != null ? Math.round(p.bitrateKbps).toLocaleString("en-US") + " kbps" : "—",
  };
}

// The running card's side note: how long it has been converting, "running 12 min" (whole minutes, rounded down)
export function runningFor(elapsedS: number): string {
  return "running " + (elapsedS < 60 ? "under a minute" : Math.floor(elapsedS / 60) + " min");
}

// The converting card's start time: now minus the running list's elapsed seconds (the screen formats it). Null when the worker sent no elapsed time.
export function startedAt(elapsedS: number | null | undefined, now: Date): Date | null {
  return elapsedS == null ? null : new Date(now.getTime() - elapsedS * 1000);
}
