// A refused publish of a new workflow leaves one draft, and the retry publishes that draft.
import { test } from "node:test";
import assert from "node:assert/strict";
import { publishFlowOnce } from "./flowPublish.ts";
import { answersToFlow, createDraft, DEFAULT_ANSWERS, publishFlow } from "../api/mockFlow.ts";
import { listWorkflows } from "../api/mockWorkflows.ts";
import type { Flow } from "../api/flowTypes.ts";

const NAME = "Retry check";
const META = { name: NAME, description: "" };
// A flow the check refuses: AV1 with variable bitrate is a problem, so the publish is refused.
const refused = (): Flow => {
  const flow = answersToFlow(DEFAULT_ANSWERS, META);
  flow.steps[0].settings = { ...flow.steps[0].settings, format: "av1", rateControl: "vbr" };
  return flow;
};
const fixed = (): Flow => answersToFlow(DEFAULT_ANSWERS, META);

test("a refused publish of a new workflow leaves one draft, and the retry publishes it", async () => {
  const state = { isNew: true, draftId: "new", rev: 0 };
  const api = { createDraft, publishFlow };
  const run = (flow: Flow) => publishFlowOnce(api, { isNew: state.isNew, draftId: state.draftId, flow }, (id, rev) => {
    state.draftId = id;
    state.rev = rev;
    state.isNew = false;
  });
  await assert.rejects(run(refused()), (e: { code?: string }) => e.code === "has_problems");
  await run(fixed());
  const drafts = (await listWorkflows()).filter((w) => w.name === NAME);
  assert.equal(drafts.length, 1);
  assert.equal(drafts[0].id, state.draftId);
  assert.equal(drafts[0].state, "published");
});
