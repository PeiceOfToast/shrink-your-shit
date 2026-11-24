// W11 (FITS): a touch press lifts a step only after the 300 ms hold; a move or an early release cancels it.
import { test, mock } from "node:test";
import assert from "node:assert/strict";
import { touchHold, TOUCH_HOLD_MS } from "./touchHold.ts";

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
