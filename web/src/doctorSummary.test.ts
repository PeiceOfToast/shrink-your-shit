import { test } from "node:test";
import assert from "node:assert/strict";
import { healthSummary } from "./doctorSummary.ts";

test("all checks ok, or only absent, is green", () => {
  assert.deepEqual(healthSummary([{ status: "ok" }, { status: "absent" }]), { tone: "ok", text: "Everything checks out." });
  assert.deepEqual(healthSummary([]), { tone: "ok", text: "Everything checks out." });
});

test("a warning alone is warn, counted singular and plural", () => {
  assert.deepEqual(healthSummary([{ status: "ok" }, { status: "warn" }]), { tone: "warn", text: "1 warning." });
  assert.deepEqual(healthSummary([{ status: "warn" }, { status: "warn" }]), { tone: "warn", text: "2 warnings." });
});

test("any failure is bad, and says both counts", () => {
  assert.deepEqual(healthSummary([{ status: "bad" }, { status: "warn" }, { status: "warn" }]), { tone: "bad", text: "1 check failed, 2 warnings." });
  assert.deepEqual(healthSummary([{ status: "bad" }, { status: "bad" }]), { tone: "bad", text: "2 checks failed." });
});
