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

test("a second press stops the first hold, so only the second can lift", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const lifted: string[] = [];
    // the stack's pattern: each touch press stops the held one before it starts its own
    let current: ReturnType<typeof touchHold> | null = null;
    const press = (name: string) => { current?.stop(); current = touchHold(() => lifted.push(name)); current.start(); };
    press("first");
    mock.timers.tick(200);
    press("second");
    mock.timers.tick(299);
    assert.deepEqual(lifted, []);
    mock.timers.tick(1);
    assert.deepEqual(lifted, ["second"]);
  } finally {
    mock.timers.reset();
  }
});

test("a hold still pending at unmount is stopped: no lift afterwards", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let lifted = 0;
    const h = touchHold(() => lifted++);
    h.start();
    mock.timers.tick(100);
    h.stop(); // what the stack's unmount cleanup does
    mock.timers.tick(1000);
    assert.equal(lifted, 0);
  } finally {
    mock.timers.reset();
  }
});
