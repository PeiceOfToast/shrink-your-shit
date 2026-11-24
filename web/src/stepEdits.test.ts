// W11 (#703 review): the stack's step edits. The order is the main path from the input; moves never touch the input or
// the commit, and deleting joins the steps either side.
import { test } from "node:test";
import assert from "node:assert/strict";
import { deleteNode, moveNode, order } from "./stepEdits.ts";
import type { Edge, Node, WorkflowDoc } from "./types/v2.ts";

const node = (id: string, type: string): Node => ({ id, type, typeVersion: 1, config: {}, enabled: true });
// input > skip > convert > audio > commit, joined on each step's success port
const chain = (types: [string, string][]): WorkflowDoc => {
  const nodes = types.map(([id, type]) => node(id, type));
  const edges: Edge[] = nodes.slice(1).map((n, i) => ({ id: "e" + i, from: nodes[i].id, fromPort: "success", to: n.id, toPort: "in" }));
  return { schemaVersion: 1, nodes, edges, variables: [] };
};
const base = () => chain([["in", "input.library"], ["sk", "filter.video_skips"], ["cv", "video.convert"], ["au", "audio.tracks"], ["cm", "output.commit"]]);
const ids = (d: WorkflowDoc) => order(d).map((n) => n.id).join(",");
const links = (d: WorkflowDoc) => d.edges.map((e) => `${e.from}>${e.to}`).sort().join(" ");

test("order follows the main path from the input", () => {
  assert.equal(ids(base()), "in,sk,cv,au,cm");
});

test("a middle step moves up and down, and the links follow it", () => {
  assert.equal(ids(moveNode(base(), "au", -1)), "in,sk,au,cv,cm");
  assert.equal(ids(moveNode(base(), "cv", 1)), "in,sk,au,cv,cm");
  assert.equal(links(moveNode(base(), "au", -1)), "au>cv cv>cm in>sk sk>au");
});

test("the first movable step cannot move up onto the input", () => {
  assert.deepEqual(moveNode(base(), "sk", -1), base());
});

test("the last movable step cannot move down onto the commit", () => {
  assert.deepEqual(moveNode(base(), "au", 1), base());
});

test("the input never moves", () => {
  assert.deepEqual(moveNode(base(), "in", 1), base());
  assert.deepEqual(moveNode(base(), "in", -1), base());
});

test("the commit never moves", () => {
  assert.deepEqual(moveNode(base(), "cm", -1), base());
  assert.deepEqual(moveNode(base(), "cm", 1), base());
});

test("a step that is not in the chain does not move", () => {
  assert.deepEqual(moveNode(base(), "nope", -1), base());
});

test("deleting a middle step joins the steps either side of it", () => {
  const d = deleteNode(base(), "cv");
  assert.equal(ids(d), "in,sk,au,cm");
  assert.ok(d.edges.some((e) => e.from === "sk" && e.to === "au" && e.fromPort === "success"));
});

test("deleting the last movable step joins the commit to the step before it", () => {
  const d = deleteNode(base(), "au");
  assert.equal(ids(d), "in,sk,cv,cm");
  assert.ok(d.edges.some((e) => e.from === "cv" && e.to === "cm"));
});

test("deleting the first movable step joins the input to the next step", () => {
  const d = deleteNode(base(), "sk");
  assert.equal(ids(d), "in,cv,au,cm");
  assert.ok(d.edges.some((e) => e.from === "in" && e.to === "cv"));
});
