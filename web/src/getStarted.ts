// M5c fix 1: the Dashboard's Get started steps, ticked from real data: a library exists, a workflow exists, a worker is
// on. No React here, so Node can test them.
export type Steps = [boolean, boolean, boolean];

export function getStartedSteps(libraries: number, workflows: number, workersOn: number): Steps {
  return [libraries > 0, workflows > 0, workersOn > 0];
}

// the section shows until all three steps are done
export const showGetStarted = (s: Steps): boolean => !s.every(Boolean);

// the step whose button is live: the first one not done (0-based), or null when all three are done
export function nextStep(s: Steps): number | null {
  const i = s.findIndex((x) => !x);
  return i < 0 ? null : i;
}
