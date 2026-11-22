// F7: the encoder speed lines under a worker on the Workers page. Pure, so the tests can run under node.
import type { EncoderSpeed } from "./types/v2";

// One worker's encoders, fastest first (the server does not order them).
export function speedsOf(all: EncoderSpeed[], workerId: string): EncoderSpeed[] {
  return all.filter((s) => s.worker === workerId).sort((a, b) => b.speed - a.speed);
}

// "hevc_qsv · 3.25x · 118 fps · 42 runs". fps is left out when the server has none (or it is not a positive number): never "0 fps".
export function speedLine(s: EncoderSpeed): string {
  const fps = typeof s.fps === "number" && s.fps > 0 ? [Math.round(s.fps) + " fps"] : [];
  return [s.encoder, s.speed.toFixed(2) + "x", ...fps, s.samples === 1 ? "1 run" : s.samples + " runs"].join(" · ");
}
