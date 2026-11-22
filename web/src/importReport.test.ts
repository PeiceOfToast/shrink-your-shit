// The import panel's wording (dev: `npm test`, Node 22.6+ with --experimental-strip-types).
import { test } from "node:test";
import assert from "node:assert/strict";
import { importLines } from "./importReport.ts";

const none = { missingGlobals: [], unknownSteps: [], issues: [] };

test("nothing to fix says so", () => {
  assert.deepEqual(importLines(none, [], "Settings > Variables"), [{ kind: "ok", text: "Nothing to fix." }]);
});

test("missing variables name the place to add them", () => {
  const l = importLines({ ...none, missingGlobals: ["A", "B"] }, [], "Settings > Variables");
  assert.deepEqual(l, [{ kind: "warn", text: "It uses variables this server doesn't have: A, B. Add them in Settings > Variables." }]);
});

test("unknown steps are errors and must be replaced", () => {
  const l = importLines({ ...none, unknownSteps: ["x_step"] }, [], "Settings > Variables");
  assert.deepEqual(l, [{ kind: "error", text: "It has steps this version doesn't know: x_step. Replace them before it can run." }]);
});

test("issues keep their message and severity", () => {
  const l = importLines({ ...none, issues: [{ code: "no_trigger", severity: "error", message: "No trigger." }, { code: "x", severity: "warning", message: "Careful." }] } as never, [], "S");
  assert.deepEqual(l, [{ kind: "error", text: "No trigger." }, { kind: "warn", text: "Careful." }]);
});

test("secrets without a value are listed; an unreadable draft says so", () => {
  assert.deepEqual(importLines(none, ["token"], "S"), [{ kind: "warn", text: "Fill in these secrets before it runs; export files never carry them: token." }]);
  assert.deepEqual(importLines(none, null, "S"), [{ kind: "warn", text: "Could not check its secrets." }]);
});
