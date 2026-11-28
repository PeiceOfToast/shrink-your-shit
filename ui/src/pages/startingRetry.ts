// Health while the server is starting: it answers 503 "starting" until its first background run finishes. Ask again after a
// short wait, quietly, until it answers. Returns the effect's cleanup, or nothing when there is nothing to wait for.
export function retryWhileStarting(code: string | undefined, reload: () => void, delayMs = 2000): (() => void) | undefined {
  if (code !== "starting") return undefined;
  const timer = setTimeout(reload, delayMs);
  return () => clearTimeout(timer);
}
