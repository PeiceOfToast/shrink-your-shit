// Client rule for the Trash screen (no route): after one of these refusals a Restore on the same row can't work again.
// name_taken can be retried once the other file is renamed.
const NO_RETRY = new Set(["original_gone", "converted_changed"]);

export function restoreCanRetry(code: string): boolean {
  return !NO_RETRY.has(code);
}
