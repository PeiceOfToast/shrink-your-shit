// W11 (FITS): a touch press on a step's handle lifts it only after the press has been held for the hold time. A move beyond
// a few px or a release before that cancels the hold, so a finger that is only scrolling never lifts a step. Mouse and pen don't use it.
export const TOUCH_HOLD_MS = 300;
// the most a held finger may drift before the hold is cancelled: a tap that wobbles a little still lifts (#703 review2 1)
export const TOUCH_SLOP_PX = 8;

export function touchHold(onLift: () => void, ms = TOUCH_HOLD_MS) {
  let t: ReturnType<typeof setTimeout> | null = null;
  const stop = () => { if (t !== null) { clearTimeout(t); t = null; } };
  return {
    pending: () => t !== null,
    start: () => { stop(); t = setTimeout(() => { t = null; onLift(); }, ms); },
    stop,
  };
}

// The stack's touch handle, as one unit: a press holds, a move or release before the hold ends cancels it, a new press
// replaces a held one, and dispose (unmount) drops a pending hold. Each method says whether the pending hold absorbed the event.
export function touchPress(ms = TOUCH_HOLD_MS) {
  let hold: ReturnType<typeof touchHold> | null = null, x = 0, y = 0;
  const end = () => { hold?.stop(); hold = null; };
  return {
    down(px: number, py: number, onLift: () => void) {
      end(); x = px; y = py;
      hold = touchHold(() => { hold = null; onLift(); }, ms); hold.start();
    },
    // true: the hold is still pending, so the move is not a drag (it cancels the hold only beyond the slop)
    move(px: number, py: number): boolean {
      if (!hold?.pending()) return false;
      if (Math.hypot(px - x, py - y) > TOUCH_SLOP_PX) end();
      return true;
    },
    // true: the release came before the hold, so it cancels the hold and is not a drop
    up(): boolean { if (!hold?.pending()) return false; end(); return true; },
    cancel() { end(); },
    dispose() { end(); },
  };
}
