// The job page's step names: stepLabel() titles each run step, the failure sentence and "Slowest step". Its own module so
// the node tests can import it (Jobs.tsx is JSX).
// map server step labels to person-facing names (workshop #217: encode, check; #646: preflight, the check before converting)
// The check step is named by what it checked (its found Check line); an older log without one is "Checking the new file".
export const stepLabel = (s: string, found?: string[], scored = false) => s === "check" ? checkLabel(found, scored) : ({ preflight: "Check before converting", encode: "Converting video", imported: "Older log, not split into steps", replace: "Save the result", audio: "Audio", filter: "Filter" } as Record<string, string>)[s] ?? s;
export const checkLabel = (found?: string[], scored = false) => {
  const c = (found ?? []).find((l) => l.startsWith("Check: ")) ?? "";
  return /^Check: picture/.test(c) ? "Checking the picture" : /^Check: playback/.test(c) ? "Checking playback" : /^Check: damage/.test(c) ? "Checking the original for damage" : scored ? "Checking the picture" : "Checking the new file"; // a remote worker records the picture score with no Check: line
};
