// The stack's step list is the main path of the document (order): the Next links, and a question's Yes (FB1d).
// Moving, deleting and adding a step rewire that path. A question's No holds one end (Leave the file alone or Stop
// as failed), which the row's "Otherwise" control sets. Pure functions: the caller saves.
import type { Edge, ID, Node, StepDefinition, WorkflowDoc } from "./types/v2";

// A question answers Yes or No (its outputs are match and else); an end has no outputs.
export const isQuestion = (n: Node) => n.type === "filter.video" || n.type.startsWith("check.");
export const isEnd = (n: Node) => n.type === "flow.end" || n.type === "flow.fail" || n.type === "flow.requeue" || n.type === "flow.goto";
// The port the main path leaves a step by.
export const mainPort = (n: Node) => (isQuestion(n) ? "match" : "success");

// The add menu's groups in order (W11 spec). A plugin's steps come after them, one group per plugin.
export const GROUPS = ["Start", "Filters and conditions", "Video", "Audio", "Subtitles", "File", "Check and save"];
const CAT_GROUP: Record<string, number> = { input: 0, filter: 1, flow: 1, video: 2, audio: 3, subtitles: 4, container: 5, verify: 6, output: 6 };
export function groupOf(d: StepDefinition): { index: number; title: string } {
  if (d.source.kind === "plugin") return { index: GROUPS.length, title: d.source.pluginId };
  const i = CAT_GROUP[d.category];
  return i === undefined ? { index: GROUPS.length, title: d.category } : { index: i, title: GROUPS[i] };
}

// The steps in running order: the main path from the input (contract "Stack ⇄ graph"): Next links, and Yes at a question.
export function order(d: WorkflowDoc): Node[] {
  const out: Node[] = [];
  let n = d.nodes.find((x) => x.type === "input.library");
  while (n && !out.includes(n)) {
    out.push(n);
    const port = mainPort(n), e = d.edges.find((x) => x.from === n!.id && x.fromPort === port);
    n = e && d.nodes.find((x) => x.id === e.to);
  }
  return out;
}

// What a question's No leads to: one of the ends (leave, fail, or a hand-over to another workflow: FB4b), or
// something else (the graph shows it).
export type Otherwise = "leave" | "fail" | "goto" | "other" | "none";
export function otherwiseOf(d: WorkflowDoc, q: Node): { kind: Otherwise; node?: Node } {
  const e = d.edges.find((x) => x.from === q.id && x.fromPort === "else"), n = e && d.nodes.find((x) => x.id === e.to);
  if (!e) return { kind: "none" };
  if (!n) return { kind: "other" };
  return { kind: n.type === "flow.end" ? "leave" : n.type === "flow.fail" ? "fail" : n.type === "flow.goto" ? "goto" : "other", node: n };
}

// Set a question's No to an end of the given kind: the end there already is replaced (its words kept when the kind
// is the same), another target is left alone (the graph owns it).
export function setOtherwise(d: WorkflowDoc, q: Node, def: StepDefinition): WorkflowDoc {
  const cur = otherwiseOf(d, q);
  if (cur.kind === "other") return d;
  if (cur.node && cur.node.type === def.type) return d;
  const nodes = d.nodes.filter((n) => n.id !== cur.node?.id), edges = d.edges.filter((e) => !(e.from === q.id && e.fromPort === "else") && e.to !== cur.node?.id);
  const end = newNode(def, "n" + uid());
  return { ...d, nodes: [...nodes, end], edges: [...edges, { id: "e" + uid(), from: q.id, fromPort: "else", to: end.id, toPort: "in" }] };
}

const uid = () => Math.random().toString(36).slice(2, 10);

// The main path through ids, in that order; links that already join two neighbours keep their id. A question's
// link out is its Yes; its No is left as it is.
function relink(d: WorkflowDoc, ids: ID[]): WorkflowDoc {
  const byId = (id: ID) => d.nodes.find((n) => n.id === id);
  const isMain = (e: Edge) => { const n = byId(e.from); return !!n && e.fromPort === mainPort(n) && ids.includes(e.from); };
  const other = d.edges.filter((e) => !isMain(e)), was = d.edges.filter(isMain);
  const chain: Edge[] = [];
  ids.slice(1).forEach((to, i) => {
    const from = ids[i], n = byId(from), port = n ? mainPort(n) : "success";
    chain.push(was.find((e) => e.from === from && e.to === to) ?? { id: "e" + uid(), from, fromPort: port, to, toPort: "in" });
  });
  return { ...d, edges: [...other, ...chain] };
}

// Move one step up or down the chain. The input and the last step never move (the page doesn't offer it).
export function moveNode(d: WorkflowDoc, id: ID, dir: -1 | 1): WorkflowDoc {
  const ids = order(d).map((n) => n.id), i = ids.indexOf(id), j = i + dir;
  if (i < 0 || j < 0 || j >= ids.length) return d;
  [ids[i], ids[j]] = [ids[j], ids[i]];
  return relink(d, ids);
}

// Delete a step and join the steps either side of it. Its own links go with it, and so does an end that only it led to
// (a question's No).
export function deleteNode(d: WorkflowDoc, id: ID): WorkflowDoc {
  const ids = order(d).map((n) => n.id).filter((x) => x !== id);
  const orphans = d.edges.filter((e) => e.from === id).map((e) => e.to).filter((t) => { const n = d.nodes.find((x) => x.id === t); return n && isEnd(n) && d.edges.filter((e) => e.to === t).length === 1; });
  const gone = new Set([id, ...orphans]);
  const cut = { ...d, nodes: d.nodes.filter((n) => !gone.has(n.id)), edges: d.edges.filter((e) => !gone.has(e.from) && !gone.has(e.to)) };
  return relink(cut, ids);
}

// Add a question after a step: it joins the main path by its Yes, and its No gets "Leave the file alone" at once, so
// every file has somewhere to go (the compile refuses an unconnected No).
export function insertQuestion(d: WorkflowDoc, afterId: ID | undefined, node: Node, endDef: StepDefinition): WorkflowDoc {
  const next = insertAfter(d, afterId, node);
  return setOtherwise(next, node, endDef);
}

// FB4b: a question after the save ("Compare the result") goes behind the save, or behind the last card there, with
// nothing on its No: after the save a port left alone means nothing more happens.
export function insertAfterSave(d: WorkflowDoc, afterId: ID | undefined, node: Node): WorkflowDoc {
  const ids = order(d).map((n) => n.id), save = ids.findIndex((id) => d.nodes.find((n) => n.id === id)?.type === "output.commit");
  let at = afterId == null ? -1 : ids.indexOf(afterId);
  if (at < 0 || at < save) at = save >= 0 ? save : ids.length - 1;
  ids.splice(Math.max(0, at) + 1, 0, node.id);
  return relink({ ...d, nodes: [...d.nodes, node] }, ids);
}

// A step whose kind sits only after the save (an after action, or a question about the result: FB4b).
export const afterOnly = (def?: StepDefinition) => !!def?.phase && !def.phase.includes("before") && def.phase.includes("after");

// Add a step after the given step (or after the input when none is named) and link it into the chain.
// An add never lands after the save step: a missing or save step as the anchor puts the new step just before it.
export function insertAfter(d: WorkflowDoc, afterId: ID | undefined, node: Node): WorkflowDoc {
  const ids = order(d).map((n) => n.id), save = ids.findIndex((id) => d.nodes.find((n) => n.id === id)?.type === "output.commit");
  let at = afterId == null ? 0 : ids.indexOf(afterId);
  if (afterId != null && (at < 0 || ids[at] === ids[save])) at = (save >= 0 ? save : ids.length) - 1;
  ids.splice(Math.max(0, at) + 1, 0, node.id);
  return relink({ ...d, nodes: [...d.nodes, node] }, ids);
}

// A new step with its configSchema defaults (the server's own defaults, read from the step definition).
export function newNode(def: StepDefinition, id: ID): Node {
  const props = (def.configSchema.properties ?? {}) as Record<string, { default?: unknown }>;
  const config = Object.fromEntries(Object.entries(props).filter(([, s]) => "default" in s).map(([k, s]) => [k, s.default]));
  return { id, type: def.type, typeVersion: def.typeVersion, enabled: true, config };
}
