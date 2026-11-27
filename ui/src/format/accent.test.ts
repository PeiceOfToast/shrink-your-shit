import { test } from "node:test";
import assert from "node:assert/strict";
import { CARD, accentVars, contrast, isHex, readableAccent, textOn } from "./accent.ts";

test("the default green reads on both cards without moving", () => {
  assert.equal(readableAccent("#22C55E", "dark").moved, false);
  assert.equal(readableAccent("#15803D", "light").moved, false);
});

test("a colour too pale for a white card is darkened until it reads at 4.5:1", () => {
  const r = readableAccent("#FACC15", "light");
  assert.equal(r.moved, true);
  assert.ok(contrast(r.color, CARD.light) >= 4.5);
});

test("a colour too dark for the dark card is lightened until it reads at 4.5:1", () => {
  const r = readableAccent("#1E3A8A", "dark");
  assert.equal(r.moved, true);
  assert.ok(contrast(r.color, CARD.dark) >= 4.5);
});

test("text on an accent is white or near-black, whichever reads better", () => {
  assert.equal(textOn("#22C55E"), "#06130B");
  assert.equal(textOn("#1E3A8A"), "#FFFFFF");
});

test("no accent means no variables, so the stylesheet's default green applies", () => {
  assert.equal(accentVars(null), null);
  const v = accentVars("#3B82F6");
  assert.ok(v);
  assert.deepEqual(Object.keys(v.vars).sort(), ["--acc-dark", "--acc-light", "--on-dark", "--on-light"]);
});

test("only six-digit hex colours are accepted", () => {
  assert.equal(isHex("#3b82f6"), true);
  assert.equal(isHex("#3b82"), false);
  assert.equal(isHex("blue"), false);
});
