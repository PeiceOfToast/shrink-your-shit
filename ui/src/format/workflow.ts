// The "Delete refused" sentence on the workflows list (page 04): names the libraries that use the workflow.
export function refusedText(names: string[]): string {
  const list = names.length <= 1 ? names.join("") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
  if (names.length === 1) return `${list} uses this. Pick another workflow for it first.`;
  return `${list} use this. Pick another workflow for them first.`;
}
