// The gpuJobsSeparate switch's wording (dev: `npm test`, Node 22.6+ with --experimental-strip-types). The mock module
// itself imports a JSON registry and touches window globals, so the settings round-trip is checked on the real server
// instead (see the report), not here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { GPU_HINT, GPU_LABEL, gpuSaved } from "./gpuJobs.ts";

test("the switch says what it does, in the design's words", () => {
  assert.equal(GPU_LABEL, "Graphics jobs don't wait for processor jobs");
  assert.match(GPU_HINT, /^A file that converts on the graphics card can start while processor jobs fill Files at once\./);
});

test("saving says which way it went", () => {
  assert.equal(gpuSaved(true), "Saved: graphics jobs don't wait for processor jobs.");
  assert.equal(gpuSaved(false), "Saved: graphics jobs wait for processor jobs.");
});
