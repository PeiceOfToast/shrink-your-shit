// The contract check itself (npm run contract): a golden that matches passes; one with an extra key fails.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { test } from "node:test";

const run = (dir: string) => spawnSync(process.execPath, ["scripts/contract.mjs", dir], { encoding: "utf8" });

test("a golden that matches types.ts passes", () => {
  const r = run("src/api/contract-fixtures/good");
  assert.equal(r.status, 0, r.stdout + r.stderr);
});

test("a golden with an extra key fails, and the key is named", () => {
  const r = run("src/api/contract-fixtures/bad");
  assert.notEqual(r.status, 0);
  assert.match(r.stdout + r.stderr, /bogusExtraKey/);
});
