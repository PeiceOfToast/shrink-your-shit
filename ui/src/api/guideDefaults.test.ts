// Drift test: the mock's guided defaults and starter answers against the server's goldens (contract testdata).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { DEFAULT_ANSWERS } from "./mockFlow.ts";
import { getStarter } from "./mockSetup.ts";

const golden = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../../internal/api/testdata/contract/${name}`, import.meta.url), "utf8")).body;

test("the new-workflow defaults match guide.json", () => {
  assert.deepEqual(DEFAULT_ANSWERS, golden("guide.json").defaults);
});

test("the shrink-videos starter answers match starter.json", async () => {
  const s = await getStarter("shrink-videos");
  assert.deepEqual(s.answers, golden("starter.json").answers);
});
