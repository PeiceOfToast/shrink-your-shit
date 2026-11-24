// W11 (FITS): a touch press on a step's handle lifts it only after the press has been held for the hold time. A move or
// a release before that cancels the hold, so a finger that is only scrolling never lifts a step. Mouse and pen don't use it.
export const TOUCH_HOLD_MS = 300;

export function touchHold(onLift: () => void, ms = TOUCH_HOLD_MS) {
  let t: ReturnType<typeof setTimeout> | null = null;
  const stop = () => { if (t !== null) { clearTimeout(t); t = null; } };
  return {
    pending: () => t !== null,
    start: () => { stop(); t = setTimeout(() => { t = null; onLift(); }, ms); },
    stop,
  };
}
