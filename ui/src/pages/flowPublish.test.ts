// A refused publish of a new workflow leaves one draft, and the retry publishes that draft. The editor makes its draft from
// the flow; the guided page makes it from the answers. Both go through publishFlowOnce.
import { test } from "node:test";
import assert from "node:assert/strict";
import { publishFlowOnce } from "./flowPublish.ts";
import { answersToFlow, createDraft, DEFAULT_ANSWERS, publishFlow } from "../api/mockFlow.ts";
import { listWorkflows } from "../api/mockWorkflows.ts";
import type { Flow } from "../api/flowTypes.ts";

// A flow the check refuses: AV1 with variable bitrate is a problem, so the publish is refused.
const refused = (name: string): Flow => {
  const flow = answersToFlow(DEFAULT_ANSWERS, { name, description: "" });
  flow.steps[0].settings = { ...flow.steps[0].settings, format: "av1", rateControl: "vbr" };
  return flow;
};
const fixed = (name: string): Flow => answersToFlow(DEFAULT_ANSWERS, { name, description: "" });

const variants: [string, (name: string, flow: Flow) => () => Promise<{ id: string; rev: number }>][] = [
  ["editor (from the flow)", (name, flow) => () => createDraft({ name, description: "", flow })],
  ["guided (from the answers)", (name) => () => createDraft({ name, answers: DEFAULT_ANSWERS })],
];

for (const [label, makeDraftFor] of variants) {
  test(`a refused publish of a new workflow leaves one draft, and the retry publishes it: ${label}`, async () => {
    const name = `Retry check ${label.split(" ")[0]}`;
    const state = { isNew: true, draftId: "new", rev: 0 };
    const run = (flow: Flow) =>
      publishFlowOnce({ publishFlow }, { isNew: state.isNew, draftId: state.draftId, flow, makeDraft: makeDraftFor(name, flow) }, (id, rev) => {
        state.draftId = id;
        state.rev = rev;
        state.isNew = false;
      });
    await assert.rejects(run(refused(name)), (e: { code?: string }) => e.code === "has_problems");
    const usedId = await run(fixed(name));
    const drafts = (await listWorkflows()).filter((w) => w.name === name);
    assert.equal(drafts.length, 1);
    assert.equal(drafts[0].id, usedId);
    assert.equal(drafts[0].state, "published");
  });
}
