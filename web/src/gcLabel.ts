// Convert's graphics card choices (design-direction-w7-f4-convert) and the words its summary shows for a saved value.
// A value that is none of the choices reads "not set", not Automatic: the engine would not know what to do with it.
export const GC_CHOICES: [string, string, string][] = [
  ["auto", "Automatic", "Uses the graphics card when it can convert the file, and the processor when it can't."],
  ["only", "Graphics card only", "Never uses the processor. If the graphics card can't convert a file, its job fails and the original is kept."],
  ["cpu", "Processor only", "Never uses the graphics card. Slower, but the same on every machine."]];

export function gcLabel(hardware: unknown): string {
  const v = String(hardware ?? "auto"); // a missing value is the default, auto
  return GC_CHOICES.find((x) => x[0] === v)?.[1] ?? "not set";
}
