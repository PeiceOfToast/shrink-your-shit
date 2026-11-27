// The workflow mock no longer carries "if a step fails" or flow variables: not in the flow, not in the guide, not in the summary.
import assert from "node:assert/strict";
import { test } from "node:test";
import { answersToFlow, DEFAULT_ANSWERS, getGuide, guidePreview } from "./mockFlow.ts";

test("a flow from the guided answers has no failure rule or variables", () => {
  const flow = answersToFlow(DEFAULT_ANSWERS, { name: "Shrink videos", description: "" });
  assert.equal("ifStepFails" in flow, false);
  assert.equal("variables" in flow, false);
});

test("the guide has no if-it-fails part", async () => {
  const guide = await getGuide();
  assert.equal(guide.parts.some((p) => p.id === "if_fails"), false);
});

test("the summary never says what happens when a step fails", async () => {
  const preview = await guidePreview(DEFAULT_ANSWERS);
  const text = JSON.stringify(preview);
  assert.equal(/If a step fails/.test(text), false);
});
