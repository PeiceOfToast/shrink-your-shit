// V4 Graph editor, step 1 (SCREENS V4, mockups/workflow-graph.html; spec 5.3-5.4): the workflow's draft as a read-only
// canvas on the real /api/v2 (draft, step definitions with port labels, the server's problems), with pan, zoom, fit and
// a minimap. Positions come from the draft's layout (kept apart from the logic, spec 13); a node without one is placed
// by dagre (left to right). Selecting a node shows what it is. Editing, the inspector form, palette, connect/delete and
// undo/redo come in steps 2-3; Test run stays hidden until its endpoint lands. Phones: view, pan, zoom, inspect (M5).
import "@xyflow/react/dist/style.css";
import { Background, Handle, MiniMap, Position, ReactFlow, ReactFlowProvider, useReactFlow, useViewport, type Edge as RFEdge, type Node as RFNode, type NodeChange, type NodeProps } from "@xyflow/react";
import dagre from "@dagrejs/dagre";
import { Fragment, useMemo, useState } from "react";
import { Icon } from "../Icon";
import { PageHead } from "../PageHead";
import { Link } from "../router";
import { useData } from "../useData";
import { getDraft, getWorkflow, stepDefinitions } from "../workflowApi";
import { ICON } from "./WorkflowStack";
import type { Node, StepDefinition, ValidationIssue, WorkflowRevision } from "../types/v2";

// The step categories' colour stripes (tokens.css --cat-*).
const CAT: Record<string, string> = { input: "var(--cat-input)", filter: "var(--cat-condition)", video: "var(--cat-video)", audio: "var(--cat-audio)", subtitles: "var(--cat-subtitle)",
  container: "var(--cat-files)", verify: "var(--cat-inspect)", output: "var(--cat-output)", flow: "var(--cat-logic)" };
const W = 188, H = (outs: number) => 34 + 24 + Math.max(outs, 1) * 22 + 8; // node size for layout (matches .gnode)

type Data = { node: Node; def?: StepDefinition; summary: string; issues: ValidationIssue[] };
type GNodeT = RFNode<Data, "step">;

function GNode({ data, selected }: NodeProps<GNodeT>) {
  const { node, def, summary, issues } = data, errs = issues.filter((i) => i.severity === "error").length;
  return (
    <div className={"gnode" + (node.enabled ? "" : " off") + (selected ? " sel" : "")} style={{ ["--cat" as string]: CAT[def?.category ?? ""] ?? "var(--accent)" }}
      aria-label={(node.name ?? def?.name ?? node.type) + (issues.length ? ", " + issues.length + (issues.length === 1 ? " problem" : " problems") : "")}>
      {issues.length > 0 && <span className={"gprob" + (errs ? " bad" : "")} title={issues.map((i) => i.message).join("\n")}><Icon id="i-alert" />{issues.length}</span>}
      {(def ? (def.inputs ?? []).length : 1) > 0 && <Handle type="target" position={Position.Left} id={def?.inputs?.[0]?.name ?? "in"} isConnectable={false} />}
      <div className="gh"><Icon id={ICON[def?.icon ?? ""] ?? "i-workflow"} /><span className="gt">{node.name ?? def?.name ?? node.type}</span>{!node.enabled && <span className="goff">Off</span>}</div>
      <div className="gsum" title={summary}>{summary || def?.typeLabel || ""}</div>
      {(def?.outputs ?? []).map((p) => (
        <div key={p.name} className={"gport " + p.outcome}><span>{p.label}</span>
          <Handle type="source" position={Position.Right} id={p.name} isConnectable={false} /></div>))}
    </div>);
}
const nodeTypes = { step: GNode };

// Positions: the layout's, else dagre for the nodes it doesn't place (left to right, like the step list reads).
function place(doc: WorkflowRevision, defs: StepDefinition[]) {
  const pos = doc.layout?.nodes ?? {}, missing = doc.workflow.nodes.filter((n) => !pos[n.id]);
  if (!missing.length) return pos;
  const g = new dagre.graphlib.Graph(); g.setGraph({ rankdir: "LR", nodesep: 28, ranksep: 70 }); g.setDefaultEdgeLabel(() => ({}));
  for (const n of doc.workflow.nodes) g.setNode(n.id, { width: W, height: H(defs.find((d) => d.type === n.type)?.outputs?.length ?? 1) });
  for (const e of doc.workflow.edges) g.setEdge(e.from, e.to);
  dagre.layout(g);
  const out = { ...pos };
  for (const n of missing) { const p = g.node(n.id); out[n.id] = { x: Math.round(p.x - W / 2), y: Math.round(p.y - p.height / 2) }; }
  return out;
}

export function WorkflowGraph({ id }: { id: string }) {
  const w = useData(() => getWorkflow(id), [id]), defs = useData(stepDefinitions, []), d = useData(() => getDraft(id), [id]);
  const problem = w.error ?? defs.error ?? d.error;
  if (w.error?.code === "not_found") return <div className="card"><div className="bd">There is no such workflow. It may have been deleted.</div></div>;
  if (!w.data || !defs.data || !d.data) return <div className="empty">{problem?.message ?? "Loading..."}</div>;
  return <ReactFlowProvider><Canvas id={id} name={w.data.name} published={w.data.version} doc={d.data} defs={defs.data} /></ReactFlowProvider>;
}

function Canvas({ id, name, published, doc, defs }: { id: string; name: string; published: number | null; doc: WorkflowRevision; defs: StepDefinition[] }) {
  const [sel, setSel] = useState<string | null>(null);
  const { nodes, edges } = useMemo(() => {
    const pos = place(doc, defs), issues = doc.validation ?? [];
    const title = (n?: Node) => (n ? n.name ?? defs.find((x) => x.type === n.type)?.name ?? n.type : "");
    // review bf13684 P2: every step and connection has a name in words for a screen reader
    const nodes: GNodeT[] = doc.workflow.nodes.map((n) => {
      const mine = issues.filter((i) => i.nodeId === n.id), summary = doc.stepSummaries?.[n.id] ?? "";
      return { id: n.id, type: "step", position: pos[n.id] ?? { x: 0, y: 0 }, draggable: false, connectable: false, selected: n.id === sel,
        ariaLabel: [title(n) + (n.enabled ? "" : " (off)"), summary, ...mine.map((i) => (i.severity === "error" ? "Problem: " : "Warning: ") + i.message)].filter(Boolean).join(". "),
        data: { node: n, def: defs.find((x) => x.type === n.type), summary, issues: mine } };
    });
    const edges: RFEdge[] = doc.workflow.edges.map((e) => {
      const from = doc.workflow.nodes.find((n) => n.id === e.from), outs = defs.find((x) => x.type === from?.type)?.outputs ?? [], port = outs.find((p) => p.name === e.fromPort);
      const label = outs.length > 1 ? port?.label ?? e.fromPort : undefined;
      return { id: e.id, source: e.from, sourceHandle: e.fromPort, target: e.to, targetHandle: e.toPort, label,
        ariaLabel: "From " + title(from) + " to " + title(doc.workflow.nodes.find((n) => n.id === e.to)) + (label ? ", " + label : ""),
        className: port?.outcome === "error" ? "gedge bad" : "gedge", focusable: true };
    });
    return { nodes, edges };
  }, [doc, defs, sel]);
  // a click, or Enter / Space on a focused step (React Flow selects it), opens its panel (review bf13684 P2)
  const onNodesChange = (ch: NodeChange<GNodeT>[]) => { for (const c of ch) if (c.type === "select") { if (c.selected) setSel(c.id); else setSel((s) => (s === c.id ? null : s)); } };
  const issues = doc.validation ?? [], errors = issues.filter((i) => i.severity === "error").length;
  const picked = doc.workflow.nodes.find((n) => n.id === sel), pickedDef = picked && defs.find((x) => x.type === picked.type);
  return (
    <div className="stack graphpage">
      <PageHead icon="i-workflow" crumbs={[["Workflows", "/workflows"], [name, "/workflows/" + id]]} title="Graph"
        after={<span className="pill">{published != null ? "v" + published + " published" + (doc.number == null ? " · draft" : "") : "Not published yet"}</span>}>
        {doc.stackable && <Link className="btn sm ghost" to={"/workflows/" + id}>Step list</Link>}
      </PageHead>
      <p className="muted sm phone" role="note">Editing works best on a larger screen. Here you can look around, zoom and see each step.</p>
      <div className="graphwrap">
        <div className="gcanvas" aria-label={"The steps of " + name + " as a graph"}>
          <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} fitView minZoom={0.2} maxZoom={2} nodesDraggable={false} nodesConnectable={false}
            onNodesChange={onNodesChange} onNodeClick={(_, n) => setSel(n.id)} onPaneClick={() => setSel(null)} onlyRenderVisibleElements={false}>
            <Background gap={20} />
            <MiniMap pannable zoomable ariaLabel="Overview of the graph" nodeColor="var(--border-strong)" nodeStrokeColor="var(--text-2)" nodeBorderRadius={4} />
            <Zoom />
          </ReactFlow>
        </div>
        <aside className="card ginspect" aria-label="Selected step">
          {!picked ? <div className="bd"><p className="muted">{issues.length ? (errors ? errors + (errors === 1 ? " problem" : " problems") : issues.length + (issues.length === 1 ? " warning" : " warnings")) + " in this draft. Select a step to see it." : "Select a step to see what it does."}</p></div> : (
            <div className="bd stack">
              <h2 className="h2">{picked.name ?? pickedDef?.name ?? picked.type}</h2>
              <p className="muted sm">{pickedDef?.typeLabel}{!picked.enabled && " · Off"}</p>
              {doc.stepSummaries?.[picked.id] && <p>{doc.stepSummaries[picked.id]}</p>}
              {pickedDef?.description && <p className="sm">{pickedDef.description}</p>}
              {(pickedDef?.outputs?.length ?? 0) > 0 && <dl className="kv sm">{pickedDef!.outputs.map((p) => <Fragment key={p.name}><dt>{p.label}</dt><dd>{doc.workflow.edges.filter((e) => e.from === picked.id && e.fromPort === p.name).map((e) => { const t = doc.workflow.nodes.find((n) => n.id === e.to); return t?.name ?? defs.find((x) => x.type === t?.type)?.name ?? e.to; }).join(", ") || "Nothing connected"}</dd></Fragment>)}</dl>}
              {issues.filter((i) => i.nodeId === picked.id).map((i, k) => <p key={k} className={"st " + (i.severity === "error" ? "bad" : "warn")}><Icon id="i-alert" /><span>{i.message}</span></p>)}
            </div>)}
        </aside>
      </div>
    </div>);
}

// Toolbar zoom (SCREENS V4: Fit, Zoom -, %, +).
function Zoom() {
  const rf = useReactFlow(), { zoom } = useViewport();
  return (
    <div className="gzoom" role="toolbar" aria-label="Zoom">
      <button className="btn sm icon ghost" type="button" aria-label="Zoom out" onClick={() => rf.zoomOut()}><Icon id="i-minus" /></button>
      <span className="sm" aria-live="polite">{Math.round(zoom * 100)}%</span>
      <button className="btn sm icon ghost" type="button" aria-label="Zoom in" onClick={() => rf.zoomIn()}><Icon id="i-plus" /></button>
      <button className="btn sm ghost" type="button" onClick={() => rf.fitView({ padding: 0.15 })}>Fit</button>
    </div>);
}
