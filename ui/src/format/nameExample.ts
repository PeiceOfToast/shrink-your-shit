// The example under "Add to the file name" (page 07): empty keeps the name and shows only that; typed text shows
// what the file becomes. Keep the same keeps the source's own extension, so the example uses .mov for it.
export function nameExample(container: string, suffix: string): string {
  if (suffix === "") return "Empty keeps the name.";
  if (container === "keep") return `Keeps the file's own extension: media.mov becomes media-${suffix}.mov.`;
  return `media.${container} becomes media-${suffix}.${container}.`;
}
