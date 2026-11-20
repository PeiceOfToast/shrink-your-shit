// The stack's step list is the Next chain of the document (order): moving, deleting and adding a step rewire the
// chain's success links. Other links (a condition's branches) are left as they are. Pure functions: the caller saves.
import type { Edge, ID, Node, StepDefinition, WorkflowDoc } from "./types/v2";

// The add menu's groups in order (W11 spec). A plugin's steps come after them, one group per plugin.
export const GROUPS = ["Start", "Filters and conditions", "Video", "Audio", "Subtitles", "File", "Check and save"];
const CAT_GROUP: Record<string, number> = { input: 0, filter: 1, flow: 1, video: 2, audio: 3, subtitles: 4, container: 5, verify: 6, output: 6 };
export function groupOf(d: StepDefinition): { index: number; title: string } {
  if (d.source.kind === "plugin") return { index: GROUPS.length, title: d.source.pluginId };
  const i = CAT_GROUP[d.category];
  return i === undefined ? { index: GROUPS.length, title: d.category } : { index: i, title: GROUPS[i] };
}

// The steps in running order: the chain of success links from the input (contract "Stack ⇄ graph").
export function order(d: WorkflowDoc): Node[] {
  const out: Node[] = [];
  let n = d.nodes.find((x) => x.type === "input.library");
  while (n && !out.includes(n)) {
    out.push(n);
    const e = d.edges.find((x) => x.from === n!.id && x.fromPort === "success");
    n = e && d.nodes.find((x) => x.id === e.to);
  }
  return out;
}

const uid = () => Math.random().toString(36).slice(2, 10);

// The success chain through ids, in that order; links that already join two neighbours keep their id.
function relink(d: WorkflowDoc, ids: ID[]): WorkflowDoc {
  const other = d.edges.filter((e) => e.fromPort !== "success"), was = d.edges.filter((e) => e.fromPort === "success");
  const chain: Edge[] = [];
  ids.slice(1).forEach((to, i) => {
    const from = ids[i];
    chain.push(was.find((e) => e.from === from && e.to === to) ?? { id: "e" + uid(), from, fromPort: "success", to, toPort: "in" });
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

// Delete a step and join the steps either side of it. Its own links go with it.
export function deleteNode(d: WorkflowDoc, id: ID): WorkflowDoc {
  const ids = order(d).map((n) => n.id).filter((x) => x !== id);
  const cut = { ...d, nodes: d.nodes.filter((n) => n.id !== id), edges: d.edges.filter((e) => e.from !== id && e.to !== id) };
  return relink(cut, ids);
}

// Add a step after the given step (or after the input when none is named) and link it into the chain.
export function insertAfter(d: WorkflowDoc, afterId: ID | undefined, node: Node): WorkflowDoc {
  const ids = order(d).map((n) => n.id), at = Math.max(0, afterId == null ? 0 : ids.indexOf(afterId));
  ids.splice(at + 1, 0, node.id);
  return relink({ ...d, nodes: [...d.nodes, node] }, ids);
}

// A new step with its configSchema defaults (the server's own defaults, read from the step definition).
export function newNode(def: StepDefinition, id: ID): Node {
  const props = (def.configSchema.properties ?? {}) as Record<string, { default?: unknown }>;
  const config = Object.fromEntries(Object.entries(props).filter(([, s]) => "default" in s).map(([k, s]) => [k, s.default]));
  return { id, type: def.type, typeVersion: def.typeVersion, enabled: true, config };
}
