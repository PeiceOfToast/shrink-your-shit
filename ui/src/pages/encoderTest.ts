// Waiting for a worker's encoder test. The test answers a run id; the worker then carries that run in its test state
// (test: {runId, running, finishedAt}, workshop #904). The page reads the worker again until this run has finished.
// The shape is typed here until #904's golden is on origin; then take the type from the golden.
export interface WorkerTestState {
  runId: string;
  running: boolean;
  finishedAt: string | null;
}

export interface PollOptions {
  tries: number;
  delayMs: number;
  sleep: (ms: number) => Promise<void>;
}

// True when the worker's test is this run and has finished.
export function testFinished(worker: unknown, runId: string): boolean {
  const t = (worker as { test?: WorkerTestState | null } | null)?.test;
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
