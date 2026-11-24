// W11 (FITS): a touch press lifts a step only after the 300 ms hold; a move or an early release cancels it.
import { test, mock } from "node:test";
import assert from "node:assert/strict";
import { touchHold, touchPress, TOUCH_HOLD_MS, TOUCH_SLOP_PX } from "./touchHold.ts";

test("the hold is 300 ms", () => {
  assert.equal(TOUCH_HOLD_MS, 300);
});

test("no lift before the hold time, a lift after it", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const h = touchHold(() => lifted++);
    h.start();
    mock.timers.tick(299);
    assert.equal(lifted, 0);
    assert.equal(h.pending(), true);
    mock.timers.tick(1);
    assert.equal(lifted, 1);
    assert.equal(h.pending(), false);
  } finally {
    mock.timers.reset();
  }
});

test("a move before the hold ends cancels it: no lift", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const h = touchHold(() => lifted++);
    h.start();
    mock.timers.tick(100);
    h.stop(); // the move
    mock.timers.tick(500);
    assert.equal(lifted, 0);
    assert.equal(h.pending(), false);
  } finally {
    mock.timers.reset();
  }
});

test("a release before the hold ends cancels it: no lift", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const h = touchHold(() => lifted++);
    h.start();
    mock.timers.tick(200);
    h.stop(); // the release
    mock.timers.tick(500);
    assert.equal(lifted, 0);
  } finally {
    mock.timers.reset();
  }
});

test("a new press restarts the hold, so the old one cannot lift twice", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const h = touchHold(() => lifted++);
    h.start();
    mock.timers.tick(200);
    h.start();
    mock.timers.tick(200);
    assert.equal(lifted, 0);
    mock.timers.tick(100);
    assert.equal(lifted, 1);
  } finally {
    mock.timers.reset();
  }
});

// touchPress is what the stack's touch handle calls, so these fail when its wiring is reverted

test("a second press replaces the first: only the second can lift, and only once", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const lifted: string[] = [];
    const p = touchPress();
    p.down(0, 0, () => lifted.push("first"));
    mock.timers.tick(200);
    p.down(0, 0, () => lifted.push("second"));
    mock.timers.tick(299);
    assert.deepEqual(lifted, []);
    mock.timers.tick(1);
    mock.timers.tick(1000);
    assert.deepEqual(lifted, ["second"]);
  } finally {
    mock.timers.reset();
  }
});

test("dispose at unmount drops a pending hold: no lift afterwards", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const p = touchPress();
    p.down(0, 0, () => lifted++);
    mock.timers.tick(100);
    p.dispose();
    mock.timers.tick(1000);
    assert.equal(lifted, 0);
  } finally {
    mock.timers.reset();
  }
});

test("a move within the slop keeps the hold, so the step still lifts", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const p = touchPress();
    p.down(100, 100, () => lifted++);
    assert.equal(p.move(100 + TOUCH_SLOP_PX, 100), true); // at the edge of the slop, still held
    assert.equal(p.move(100, 100 + TOUCH_SLOP_PX - 1), true);
    mock.timers.tick(TOUCH_HOLD_MS);
    assert.equal(lifted, 1);
  } finally {
    mock.timers.reset();
  }
});

test("a move beyond the slop cancels the hold: no lift, and the move is not a drag", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const p = touchPress();
    p.down(100, 100, () => lifted++);
    assert.equal(p.move(100, 100 + TOUCH_SLOP_PX + 1), true); // absorbed by the hold, which it cancels
    mock.timers.tick(1000);
    assert.equal(lifted, 0);
    assert.equal(p.move(100, 300), false); // with no hold pending the move is a drag as usual
  } finally {
    mock.timers.reset();
  }
});

test("a release before the hold cancels it; a release after the lift is a drop as usual", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const p = touchPress();
    p.down(0, 0, () => lifted++);
    mock.timers.tick(100);
    assert.equal(p.up(), true);
    mock.timers.tick(1000);
    assert.equal(lifted, 0);
    p.down(0, 0, () => lifted++);
    mock.timers.tick(TOUCH_HOLD_MS);
    assert.equal(lifted, 1);
    assert.equal(p.up(), false);
    assert.equal(p.move(0, 500), false);
  } finally {
    mock.timers.reset();
  }
});

test("cancel (pointercancel) drops a pending hold", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const p = touchPress();
    p.down(0, 0, () => lifted++);
    p.cancel();
    mock.timers.tick(1000);
    assert.equal(lifted, 0);
  } finally {
    mock.timers.reset();
  }
});
