// Waiting for a worker's encoder test. The test answers a run id, and the worker then carries that run in its test state
// (Worker.test in types.ts, workshop #904). The page reads the worker again until this run has finished.
import type { Worker } from "../api/types.ts";

export interface PollOptions {
  tries: number;
  delayMs: number;
  sleep: (ms: number) => Promise<void>;
}

// True when the worker's test is this run and has finished. Null (never run, always for a remote worker) is not finished.
export function testFinished(worker: { test: Worker["test"] } | null | undefined, runId: string): boolean {
  const t = worker?.test;
  return Boolean(t && t.runId === runId && !t.running);
}

// Checks until done() is true, once per delay, for at most `tries` checks.
export async function waitUntil(done: () => Promise<boolean>, opts: PollOptions): Promise<boolean> {
  for (let i = 0; i < opts.tries; i++) {
    await opts.sleep(opts.delayMs);
    if (await done()) return true;
  }
  return false;
}

// A refused test. encoder_test_unavailable means this machine has no encoder detector: the button stays off until the page
// reloads. worker_busy (the startup pass, no run to join) leaves it on to try again. Anything else shows its sentence.
export function disablesEncoderTest(code: string | undefined): boolean {
  return code === "encoder_test_unavailable";
}
