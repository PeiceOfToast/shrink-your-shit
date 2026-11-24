// V4 Graph editor, steps 1-2a (SCREENS V4, mockups/workflow-graph.html; spec 5.3-5.4): the workflow's draft as a read-only
// canvas on the real /api/v2 (draft, step definitions with port labels, the server's problems), with pan, zoom, fit and
// a minimap; on a desktop, connections are made and deleted, steps dragged, the server's problems listed with design's
// quick fix for an unconnected port, undo/redo, Save draft and Publish. Positions come from the draft's layout (kept apart from the logic, spec 13); a node without one is placed
// by dagre (left to right). Selecting a node shows what it is. The step form, adding and deleting steps come next; Test run stays hidden until its endpoint lands. Phones: view, pan, zoom, inspect (M5).
import "@xyflow/react/dist/style.css";
import { Background, Handle, MiniMap, Position, ReactFlow, ReactFlowProvider, useReactFlow, useViewport, type Connection, type Edge as RFEdge, type Node as RFNode, type NodeChange, type NodeProps } from "@xyflow/react";
import dagre from "@dagrejs/dagre";
import { Fragment, useEffect, useMemo, useRef, useState, type DragEvent, type RefObject } from "react";
import { SignedOut, failed, type Answer } from "../api";
import { Icon } from "../Icon";
import { notLinked, pillWords, PILL_WORDS } from "../pillWords";
import { Link, onLeave } from "../router";
import { useData } from "../useData";
import { getDraft, getWorkflow, publish, publishPreview, saveDraft, stepDefinitions, validateDoc, type Checked } from "../workflowApi";
import { OwnVars, takesVars, varListFor, varProblems, withOwn } from "../VarInput";
import { ICON, Options, Reg } from "./WorkflowStack";
import type { Layout, Node, PublishPreview, StepDefinition, ValidationIssue, WorkflowDoc, WorkflowRevision } from "../types/v2";

// The step categories' colour stripes (tokens.css --cat-*).
const CAT: Record<string, string> = { input: "var(--cat-input)", filter: "var(--cat-condition)", video: "var(--cat-video)", audio: "var(--cat-audio)", subtitles: "var(--cat-subtitle)",
  container: "var(--cat-files)", verify: "var(--cat-inspect)", output: "var(--cat-output)", flow: "var(--cat-logic)" };
const PALETTE_TYPE = "application/x-syp-step"; // the drag data of a palette step (its type)
const W = 200, H = (outs: number) => 34 + 24 + Math.max(outs, 1) * 22 + 8; // node size for layout (matches .gnode; FB6: 200 wide)

type Data = { node: Node; def?: StepDefinition; summary: string; issues: ValidationIssue[]; edit?: boolean; linked: string[]; tight?: boolean; pickPort?: (id: string, port: string, label: string, step: string) => void };
type GNodeT = RFNode<Data, "step">;

function GNode({ data, selected }: NodeProps<GNodeT>) {
  const { node, def, summary, issues, linked, tight, pickPort } = data, errs = issues.filter((i) => i.severity === "error").length;
  return (
    <div className={"gnode" + (node.enabled ? "" : " off") + (selected ? " sel" : "")} style={{ ["--cat" as string]: CAT[def?.category ?? ""] ?? "var(--accent)" }}
      aria-label={(node.name ?? def?.name ?? node.type) + (issues.length ? ", " + issues.length + (issues.length === 1 ? " problem" : " problems") : "")}>
      {issues.length > 0 && <span className={"gprob" + (errs ? " bad" : "")} title={issues.map((i) => i.message).join("\n")}><Icon id="i-alert" />{issues.length}</span>}
      {(def ? (def.inputs ?? []).length : 1) > 0 && <Handle type="target" position={Position.Left} id={def?.inputs?.[0]?.name ?? "in"} isConnectable={!!data.edit} />}
      <div className="gh"><Icon id={ICON[def?.icon ?? ""] ?? "i-workflow"} /><span className="gt">{node.name ?? def?.name ?? node.type}</span>{!node.enabled && <span className="goff">Off</span>}</div>
      <div className="gsum" title={summary}>{summary || def?.typeLabel || ""}</div>
      {(def?.outputs ?? []).map((p) => (
        <div key={p.name} className={"gport " + p.outcome}><span>{p.label}</span>
          {p.outcome !== "success" && !linked.includes(p.name) && !issues.some((i) => i.code === "unconnected_port" && i.port === p.name) &&
            (data.edit
              ? <button type="button" className={"gpill" + (tight ? " inrow" : "")} aria-label={notLinked(p, def)} title={notLinked(p, def)} onClick={(e) => (e.stopPropagation(), pickPort?.(node.id, p.name, p.label, node.name ?? def?.name ?? node.type))}>{tight ? "Not linked" : pillWords(p, def)}</button>
              : <span className={"gpill" + (tight ? " inrow" : "")} title={notLinked(p, def)}>{tight ? "Not linked" : pillWords(p, def)}</span>)}
          <Handle type="source" position={Position.Right} id={p.name} isConnectable={!!data.edit} /></div>))}
    </div>);
}
const nodeTypes = { step: GNode };

// Positions: the layout's, else dagre for the nodes it doesn't place (left to right, like the step list reads).
let pillPx: number | undefined; // the "Not linked" pill's width, measured once with the page's own CSS (170 until it can be)
const pillWidth = () => {
  if (pillPx === undefined && typeof document !== "undefined") {
    const host = document.createElement("div"), b = document.createElement("button");
    host.className = "gnode"; host.style.cssText = "position:absolute;visibility:hidden;left:-9999px;top:0";
    b.type = "button"; b.className = "gpill";
    host.appendChild(b); document.body.appendChild(host);
    // the longest of the wide texts, so no pill covers its neighbour
    let w = 0;
    for (const t of PILL_WORDS) { b.textContent = t; w = Math.max(w, b.getBoundingClientRect().width); }
    host.remove();
    if (w > 0) pillPx = w;
  }
  return pillPx ?? 170;
};

// Auto-placed steps only (a saved position stays where the person put it). dagre's gap between columns is RANKSEP; a
// column with a step that shows a "Not linked" pill also needs room for the pill, its 28px connector, the 7px inset and
// 16px of air. The extra goes after that column, so every later column moves right by it.
const RANKSEP = 70;
function place(doc: WorkflowRevision, defs: StepDefinition[]) {
  const pos = doc.layout?.nodes ?? {}, missing = doc.workflow.nodes.filter((n) => !pos[n.id]);
  if (!missing.length) return pos;
  const g = new dagre.graphlib.Graph(); g.setGraph({ rankdir: "LR", nodesep: 28, ranksep: RANKSEP }); g.setDefaultEdgeLabel(() => ({}));
  for (const n of doc.workflow.nodes) g.setNode(n.id, { width: W, height: H(defs.find((d) => d.type === n.type)?.outputs?.length ?? 1) });
  for (const e of doc.workflow.edges) g.setEdge(e.from, e.to);
  dagre.layout(g);
  const hasPill = (n: Node) => (defs.find((d) => d.type === n.type)?.outputs ?? []).some((p) => p.outcome !== "success" && !doc.workflow.edges.some((e) => e.from === n.id && e.fromPort === p.name));
  const extra = new Map<number, number>(); // column x -> the extra gap after it
  const need = pillWidth() + 28 + 7 + 16 - RANKSEP;
  for (const n of doc.workflow.nodes) if (need > 0 && hasPill(n)) { const x = g.node(n.id).x; extra.set(x, Math.max(extra.get(x) ?? 0, need)); }
  const cols = [...new Set(doc.workflow.nodes.map((n) => g.node(n.id).x))];
  const shift = (x: number) => cols.filter((c) => c < x).reduce((sum, c) => sum + (extra.get(c) ?? 0), 0);
  const out = { ...pos };
  for (const n of missing) { const p = g.node(n.id); out[n.id] = { x: Math.round(p.x - W / 2 + shift(p.x)), y: Math.round(p.y - p.height / 2) }; }
  return out;
}

export function WorkflowGraph({ id }: { id: string }) {
  const w = useData(() => getWorkflow(id), [id]), defs = useData(stepDefinitions, []), d = useData(() => getDraft(id), [id]);
  const problem = w.error ?? defs.error ?? d.error;
  if (w.error?.code === "not_found") return <div className="card"><div className="bd">There is no such workflow. It may have been deleted.</div></div>;
  if (!w.data || !defs.data || !d.data) return <div className="empty">{problem?.message ?? "Loading…"}</div>;
  return <ReactFlowProvider><Canvas id={id} name={w.data.name} published={w.data.version} doc={d.data} defs={defs.data} /></ReactFlowProvider>;
}

function Canvas({ id, name, published, doc: first, defs }: { id: string; name: string; published: number | null; doc: WorkflowRevision; defs: StepDefinition[] }) {
  const phone = useMedia("(max-width: 767px)"), edit = !phone && first.workflow.nodes.every((n) => !defs.find((x) => x.type === n.type)?.readOnly); // M5: phones view only
  // the document (logic) and the layout (positions) are kept apart (spec 13); undo/redo walks both
  const [hist, setHist] = useState<{ at: number; list: { doc: WorkflowDoc; layout: Layout }[] }>(() => ({ at: 0, list: [{ doc: first.workflow, layout: { ...first.layout, nodes: place(first, defs) } }] }));
  const cur = hist.list[hist.at], doc = cur.doc, layout = cur.layout;
  const [saved, setSaved] = useState(cur), [etag, setEtag] = useState(first.etag), [checked, setChecked] = useState<Checked>({ issues: first.validation, stackable: first.stackable, stepSummaries: first.stepSummaries });
  const [dims, setDims] = useState<Record<string, { width: number; height: number }>>({});
  const [sel, setSel] = useState<string | null>(null), [selEdge, setSelEdge] = useState<string | null>(null), [said, setSaid] = useState(""), [panel, setPanel] = useState<PublishPreview | null>(null), [ver, setVer] = useState(published);
  const dirty = cur !== saved;
  // item 7 (#467 follow-up): a click on an unlinked port's pill makes the next added step link from that port
  const [pending, setPending] = useState<{ node: string; port: string; label: string; step: string } | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  // Esc drops a pending port from anywhere on the page, not only from the palette
  useEffect(() => { if (!pending) return; const k = (e: KeyboardEvent) => { if (e.key === "Escape") { setPending(null); setSaid("Dropped the link from that port."); } }; window.addEventListener("keydown", k); return () => window.removeEventListener("keydown", k); }, [pending]);
  const pendingVia = () => { const pn = pending && doc.nodes.find((n) => n.id === pending.node); return pn && !doc.edges.some((e) => e.from === pn.id && e.fromPort === pending.port) ? { after: pn, port: pending.port } : undefined; };
  const push = (next: { doc?: WorkflowDoc; layout?: Layout }) => { window.clearTimeout(nudgeTimer.current); setHist((h) => ({ at: h.at + 1, list: [...h.list.slice(0, h.at + 1), { doc: next.doc ?? h.list[h.at].doc, layout: next.layout ?? h.list[h.at].layout }] })); setPanel(null); setSaid(""); setBar(null); };
  // the Undo offered for a delete, for 8 s, only while the delete is still the latest change
  const [bar, setBar] = useState<{ back: number; fwd: number } | null>(null);
  useEffect(() => { if (!bar) return; const t = setTimeout(() => setBar(null), 8000); return () => clearTimeout(t); }, [bar]);
  const undoDelete = () => { window.clearTimeout(nudgeTimer.current); if (bar) setHist((h) => (h.at === bar.fwd ? { ...h, at: bar.back } : h)); setBar(null); setSaid("Delete undone."); };
  // the server's check and step words for the document as it is now: asked 300 ms after a change of the logic
  const seen = useRef(doc);
  useEffect(() => {
    if (doc === seen.current) return; seen.current = doc;
    const t = setTimeout(async () => { const r = await act(validateDoc(id, doc), setSaid); if (r) setChecked(r); }, 300);
    return () => clearTimeout(t);
  }, [doc]); // eslint-disable-line react-hooks/exhaustive-deps
  const now = useRef({ dirty, doc, layout, etag }); now.current = { dirty, doc, layout, etag };
  useEffect(() => { // leaving with unsaved changes offers to save (as the step list does)
    // review f03fcad: Cancel keeps the user here with the draft; OK saves it and leaves
    onLeave(() => { const n = now.current; if (!n.dirty) return; if (!window.confirm("You have unsaved changes in this draft.\n\nOK saves them and leaves. Cancel stays on this page.")) return false; void saveDraft(id, n.doc, n.layout, n.etag).catch(() => {}); });
    return () => onLeave(null);
  }, [id]);
  // the keyboard handler reads the page as it is NOW (filled in each render, below addNode)
  const keys = useRef<{ sel: string | null; doc: WorkflowDoc; layout: Layout; addNode: (d: StepDefinition, base?: Node, at?: { x: number; y: number }) => void; nudge: (id: string, dx: number, dy: number) => void }>({ sel, doc, layout, addNode: () => {}, nudge: () => {} });
  useEffect(() => { // review f03fcad P3: Ctrl+Z undo, Ctrl+Shift+Z or Ctrl+Y redo (not while typing in a field)
    if (!edit) return;
    const key = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || (e.target as HTMLElement)?.closest?.("input, textarea, select, [contenteditable]")) return;
      const k = e.key.toLowerCase(), redo = (k === "z" && e.shiftKey) || k === "y";
      if (k === "c" || k === "v") { // copy / paste a step
        const { sel: s0, doc: d0, layout: l0 } = keys.current;
        if (k === "c") { const n = d0.nodes.find((x) => x.id === s0); if (n && defs.find((x) => x.type === n.type)?.category !== "input") (clip.current = n), (pastes.current = 0), setSaid("Copied " + (n.name ?? defs.find((x) => x.type === n.type)?.name) + ". Ctrl+V pastes it."); return; }
        const c = clip.current, d = c && defs.find((x) => x.type === c.type), at = c && (l0.nodes[c.id] ?? { x: 0, y: 0 });
        if (c && d && at) { e.preventDefault(); const k = ++pastes.current; keys.current.addNode(d, c, { x: at.x + 40 * k, y: at.y + 40 * k }); } // re-review d4d78a9 P3: each paste a step further, never stacked // review fded51f P2: the add of NOW, not of page load
        return;
      }
      if (k !== "z" && k !== "y") return;
      e.preventDefault();
      setBar(null);
      setHist((h) => (redo ? (h.at < h.list.length - 1 ? { ...h, at: h.at + 1 } : h) : h.at > 0 ? { ...h, at: h.at - 1 } : h));
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [edit]);
  // item 5: the arrow keys move the selected step, 16 px (64 px with Shift); the window's capture phase keeps React Flow's own nudge from moving it too
  useEffect(() => {
    if (!edit) return;
    const arrow = (e: KeyboardEvent) => {
      const k = keys.current, t = e.target as HTMLElement | null;
      if (!k.sel || !t?.closest?.(".gcanvas") || t.closest("input, textarea, select, [contenteditable]")) return;
      const dx = e.key === "ArrowLeft" ? -1 : e.key === "ArrowRight" ? 1 : 0, dy = e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0;
      if (!dx && !dy) return;
      e.preventDefault(); e.stopPropagation(); const s = e.shiftKey ? 64 : 16; k.nudge(k.sel, dx * s, dy * s);
    };
    window.addEventListener("keydown", arrow, true);
    return () => window.removeEventListener("keydown", arrow, true);
  }, [edit]);
  useEffect(() => { if (!dirty) return; const warn = (e: BeforeUnloadEvent) => e.preventDefault(); window.addEventListener("beforeunload", warn); return () => window.removeEventListener("beforeunload", warn); }, [dirty]);

  const { nodes, edges } = useMemo(() => {
    const issues = checked.issues ?? [], title = (n?: Node) => (n ? n.name ?? defs.find((x) => x.type === n.type)?.name ?? n.type : "");
    const free = (n: Node, def?: StepDefinition) => (def?.outputs ?? []).filter((p) => p.outcome !== "success" && !doc.edges.some((e) => e.from === n.id && e.fromPort === p.name) && !issues.some((i) => i.nodeId === n.id && i.code === "unconnected_port" && i.port === p.name));
    // a "Not linked" pill sits to the right of its step; when another step's box would cover it, the pill moves into the port row
    const at = (id: string) => layout.nodes[id] ?? { x: 0, y: 0 }, outsOf = (n: Node) => defs.find((x) => x.type === n.type)?.outputs?.length ?? 1;
    const tightPill = (n: Node) => {
      if (!free(n, defs.find((x) => x.type === n.type)).length) return false;
      const p = at(n.id), left = p.x + W + 28, right = left + pillWidth() + 7;
      return doc.nodes.some((m) => { const q = at(m.id); return m.id !== n.id && q.x < right && q.x + W > left && q.y < p.y + H(outsOf(n)) && q.y + H(outsOf(m)) > p.y; });
    };
    // review bf13684 P2: every step and connection has a name in words for a screen reader
    const nodes: GNodeT[] = doc.nodes.map((n) => {
      const mine = issues.filter((i) => i.nodeId === n.id), summary = checked.stepSummaries?.[n.id] ?? "";
      const def = defs.find((x) => x.type === n.type);
      return { id: n.id, type: "step", position: layout.nodes[n.id] ?? { x: 0, y: 0 }, draggable: edit, connectable: edit, deletable: edit && def?.category !== "input", selected: n.id === sel, zIndex: free(n, def).length ? 2 : undefined,
        initialWidth: W, initialHeight: H(def?.outputs?.length ?? 1), measured: dims[n.id], // review f03fcad: the minimap only draws nodes with a size
        ariaLabel: [title(n) + (n.enabled ? "" : " (off)"), summary, ...mine.map((i) => (i.severity === "error" ? "Problem: " : "Warning: ") + i.message), ...free(n, def).map((p) => notLinked(p, def))].filter(Boolean).join(". "),
        data: { node: n, def, summary, issues: mine, edit, linked: doc.edges.filter((e) => e.from === n.id).map((e) => e.fromPort), tight: tightPill(n), pickPort: (id: string, port: string, label: string, step: string) => (window.clearTimeout(nudgeTimer.current), setSel(id), setSelEdge(null), setPending({ node: id, port, label, step }), setSaid("Pick a step to add after " + step + " › " + label + ". Esc to cancel."), searchRef.current?.focus()) } };
    });
    const edges: RFEdge[] = doc.edges.map((e) => {
      const from = doc.nodes.find((n) => n.id === e.from), outs = defs.find((x) => x.type === from?.type)?.outputs ?? [], port = outs.find((p) => p.name === e.fromPort);
      const label = outs.length > 1 ? port?.label ?? e.fromPort : undefined;
      return { id: e.id, source: e.from, sourceHandle: e.fromPort, target: e.to, targetHandle: e.toPort, label,
        ariaLabel: "From " + title(from) + " to " + title(doc.nodes.find((n) => n.id === e.to)) + (label ? ", " + label : ""),
        className: port?.outcome === "error" ? "gedge bad" : "gedge", focusable: true, deletable: edit, selected: e.id === selEdge };
    });
    return { nodes, edges };
  }, [doc, layout, defs, checked, edit, sel, selEdge, dims]);

  // a port holds one connection (the server's port_used_twice): connecting it again replaces the old one
  const connect = (c: Connection) => {
    if (!c.source || !c.target || c.source === c.target) return;
    const kept = doc.edges.filter((e) => !(e.from === c.source && e.fromPort === (c.sourceHandle ?? "success")));
    push({ doc: { ...doc, edges: [...kept, { id: newId(), from: c.source, fromPort: c.sourceHandle ?? "success", to: c.target, toPort: c.targetHandle ?? "in" }] } });
  };
  const valid = (c: Connection | RFEdge) => c.source !== c.target && !!c.target && (defs.find((x) => x.type === doc.nodes.find((n) => n.id === c.target)?.type)?.inputs ?? []).length > 0;
  const setNode = (n: Node) => push({ doc: { ...doc, nodes: doc.nodes.map((x) => (x.id === n.id ? n : x)) } });
  // step 3 (SCREENS V4 Palette): add a step at the centre of the view (its settings: the server's defaults), remove steps
  // with their connections, copy/paste a step (its settings, not its connections)
  const rf = useReactFlow();
  const centre = () => { const box = document.querySelector(".gcanvas")?.getBoundingClientRect(); return box ? rf.screenToFlowPosition({ x: box.left + box.width / 2 - W / 2, y: box.top + box.height / 2 - 40 }) : { x: 0, y: 0 }; };
  // a new Save step keeps the original when the new file is bigger (W8); a stored one without the field reads as replace
  const startConfig = (d: StepDefinition): Record<string, unknown> => (d.type === "output.commit" ? { ifBigger: "keep" } : {});
  const addNode = (d: StepDefinition, base?: Node, at?: { x: number; y: number }) => {
    const n: Node = base ? { ...structuredClone(base), id: newId() } : { id: newId(), type: d.type, typeVersion: d.typeVersion, config: startConfig(d), enabled: true };
    const p = at ?? centre();
    push({ doc: { ...doc, nodes: [...doc.nodes, n] }, layout: { ...layout, nodes: { ...layout.nodes, [n.id]: { x: Math.round(p.x), y: Math.round(p.y) } } } });
    setSel(n.id); setSelEdge(null); setSaid("Added " + (n.name ?? d.name) + ". Connect it from a port of another step.");
  };
  // item 4: a step dropped on a connection goes between its two ends; one added below the selected step links from its success port when that is free
  const insertStep = (d: StepDefinition, p: { x: number; y: number }, via?: { edge?: WorkflowDoc["edges"][number]; after?: Node; port?: string }) => {
    const n: Node = { id: newId(), type: d.type, typeVersion: d.typeVersion, config: startConfig(d), enabled: true };
    const inPort = d.inputs?.[0]?.name, canGoOn = !!inPort && (d.outputs ?? []).some((o) => o.name === "success");
    let edges = doc.edges, said = "Added " + d.name + ". Connect it from a port of another step.";
    if (via?.edge && canGoOn) {
      const e = via.edge;
      edges = [...edges.filter((x) => x.id !== e.id), { id: newId(), from: e.from, fromPort: e.fromPort, to: n.id, toPort: inPort! }, { id: newId(), from: n.id, fromPort: "success", to: e.to, toPort: e.toPort }];
      said = "Added " + d.name + " between " + nameOf(e.from) + " and " + nameOf(e.to) + ".";
    } else if (via?.after && canGoOn && !doc.edges.some((x) => x.from === via.after!.id && x.fromPort === (via.port ?? "success"))) {
      edges = [...edges, { id: newId(), from: via.after.id, fromPort: via.port ?? "success", to: n.id, toPort: inPort! }];
      said = "Added " + d.name + " after " + nameOf(via.after.id) + ".";
    }
    push({ doc: { ...doc, nodes: [...doc.nodes, n], edges }, layout: { ...layout, nodes: { ...layout.nodes, [n.id]: { x: Math.round(p.x), y: Math.round(p.y) } } } });
    setSel(n.id); setSelEdge(null); setSaid(said); setPending(null);
  };
  // the palette's add: below the selected step, else at the view's centre
  const addFromPalette = (d: StepDefinition) => {
    const via = pendingVia();
    if (via) { const p = layout.nodes[via.after.id] ?? { x: 0, y: 0 }; return insertStep(d, { x: p.x, y: p.y + H(defs.find((x) => x.type === via.after.type)?.outputs?.length ?? 1) + 40 }, via); }
    const a = doc.nodes.find((n) => n.id === sel);
    if (!a) return insertStep(d, centre());
    const p = layout.nodes[a.id] ?? { x: 0, y: 0 };
    insertStep(d, { x: p.x, y: p.y + H(defs.find((x) => x.type === a.type)?.outputs?.length ?? 1) + 40 }, { after: a });
  };
  const nudgeTimer = useRef<number>(0);
  const nudgeRun = useRef<{ id: string; at: number } | null>(null); // the history entry the current run of arrow presses writes into
  const nudge = (id: string, dx: number, dy: number) => {
    const p = layout.nodes[id] ?? { x: 0, y: 0 }, next = { ...layout, nodes: { ...layout.nodes, [id]: { x: p.x + dx, y: p.y + dy } } };
    const run = nudgeRun.current;
    if (run && run.id === id && run.at === hist.at) setHist((h) => ({ ...h, list: h.list.map((e, i) => (i === h.at ? { ...e, layout: next } : e)) }));
    else { push({ layout: next }); nudgeRun.current = { id, at: hist.at + 1 }; }
    window.clearTimeout(nudgeTimer.current); nudgeTimer.current = window.setTimeout(() => setSaid("Moved " + nameOf(id) + "."), 1000); // said once the arrows pause
  };
  // a palette step dragged onto the canvas: a dashed box follows the pointer (16 px grid, in screen pixels)
  const [ghost, setGhost] = useState<{ x: number; y: number } | null>(null);
  const [pointer, setPointer] = useState<{ x: number; y: number } | null>(null), [dragType, setDragType] = useState<string | null>(null);
  const dragging = (t: string | null) => { setDragType(t); if (!t) { setGhost(null); setPointer(null); } };
  const snap = (v: number) => Math.round(v / 16) * 16;
  const onDragOver = (e: DragEvent) => { if (!e.dataTransfer.types.includes(PALETTE_TYPE)) return; e.preventDefault(); e.dataTransfer.dropEffect = "copy"; const f = rf.screenToFlowPosition({ x: e.clientX, y: e.clientY }), at = rf.flowToScreenPosition({ x: snap(f.x - W / 2), y: snap(f.y - 40) }); setGhost(at); setPointer({ x: e.clientX, y: e.clientY }); };
  const onDrop = (e: DragEvent) => {
    setGhost(null); setPointer(null); const type = e.dataTransfer.getData(PALETTE_TYPE); if (!type) return;
    e.preventDefault(); const d = defs.find((x) => x.type === type); if (!d) return;
    const hit = (document.elementFromPoint(e.clientX, e.clientY) as Element | null)?.closest(".react-flow__edge"), edge = hit?.getAttribute("data-id") ? doc.edges.find((x) => x.id === hit.getAttribute("data-id")) : undefined;
    const f = rf.screenToFlowPosition({ x: e.clientX, y: e.clientY });
    insertStep(d, { x: snap(f.x - W / 2), y: snap(f.y - 40) }, edge ? { edge } : pendingVia());
  };
  keys.current = { sel, doc, layout, addNode, nudge };
  const removeNodes = (ids: string[]) => {
    const gone = ids.filter((i) => defs.find((x) => x.type === doc.nodes.find((n) => n.id === i)?.type)?.category !== "input");
    if (!gone.length) return;
    const nodes = { ...layout.nodes }; for (const i of gone) delete nodes[i];
    // item 6: one step with one link in and one success link out: the gap closes (the step before goes to the step after)
    const one = gone.length === 1 ? gone[0] : undefined, ins = one ? doc.edges.filter((e) => e.to === one) : [], outs = one ? doc.edges.filter((e) => e.from === one && e.fromPort === "success") : [];
    let edges = doc.edges.filter((e) => !gone.includes(e.from) && !gone.includes(e.to)), text = one ? "Deleted " + nameOf(one) + "." : "Deleted " + gone.length + " steps.";
    const joins = ins.length === 1 && outs.length === 1 && outs[0].to !== one && ins[0].from !== one;
    if (joins) { edges = [...edges, { id: newId(), from: ins[0].from, fromPort: ins[0].fromPort, to: outs[0].to, toPort: outs[0].toPort }]; text = "Deleted " + nameOf(one) + ". " + nameOf(ins[0].from) + " now goes to " + nameOf(outs[0].to) + "."; }
    const before = hist.at;
    push({ doc: { ...doc, nodes: doc.nodes.filter((n) => !gone.includes(n.id)), edges }, layout: { ...layout, nodes } });
    setBar({ back: before, fwd: before + 1 }); setSaid(text);
    setSel(null);
  };
  const clip = useRef<Node | null>(null), pastes = useRef(0);
  // selecting another step, an edge or the empty canvas drops a pending port (keeps it only for the same step)
  const unpend = (keep: string | null) => { if (pending && pending.node !== keep) { setPending(null); setSaid((s) => (s.startsWith("Pick a step to add after") ? "" : s)); } };
  const removeEdges = (ids: string[]) => { if (ids.length) push({ doc: { ...doc, edges: doc.edges.filter((e) => !ids.includes(e.id)) } }); setSelEdge(null); };
  const onNodesChange = (ch: NodeChange<GNodeT>[]) => {
    // a click, or Enter / Space on a focused step (React Flow selects it), opens its panel (review bf13684 P2)
    for (const c of ch) if (c.type === "select") { if (c.selected) (setSel(c.id), setSelEdge(null), unpend(c.id)); else setSel((s) => (s === c.id ? null : s)); }
    const sized = ch.filter((c) => c.type === "dimensions" && c.dimensions) as { id: string; dimensions: { width: number; height: number } }[];
    if (sized.length) setDims((d) => ({ ...d, ...Object.fromEntries(sized.map((c) => [c.id, c.dimensions])) }));
    const moved = ch.filter((c): c is NodeChange<GNodeT> & { type: "position" } => c.type === "position" && !!(c as { position?: unknown }).position && (c as { dragging?: boolean }).dragging === false);
    if (moved.length) push({ layout: { ...layout, nodes: { ...layout.nodes, ...Object.fromEntries(moved.map((c) => [c.id, { x: Math.round(c.position!.x), y: Math.round(c.position!.y) }])) } } });
    else for (const c of ch) if (c.type === "position" && c.position) setDrag((d) => ({ ...d, [c.id]: c.position! }));
  };
  const [drag, setDrag] = useState<Record<string, { x: number; y: number }>>({});
  useEffect(() => setDrag({}), [layout]);
  // design (SCREENS V4, spec 5.6): an unconnected branch port's one-click fix adds an explicit "leave the file alone" step there
  const endDef = defs.find((x) => x.type === "flow.end");
  const leaveAlone = (nodeId: string, port: string) => {
    if (!endDef) return;
    const at = layout.nodes[nodeId] ?? { x: 0, y: 0 }, n: Node = { id: newId(), type: "flow.end", typeVersion: endDef.typeVersion, config: {}, enabled: true };
    // review f03fcad P3: the new step goes to the first free spot to the right of the port's step, never on top of another
    const size = (id: string) => dims[id] ?? { width: W, height: H(defs.find((x) => x.type === doc.nodes.find((m) => m.id === id)?.type)?.outputs?.length ?? 1) };
    const clash = (x: number, y: number) => doc.nodes.some((m) => { const p = layout.nodes[m.id], z = size(m.id); return p && x < p.x + z.width + 20 && x + W + 20 > p.x && y < p.y + z.height + 20 && y + H(0) + 20 > p.y; });
    let spot = { x: at.x + W + 80, y: at.y + 60 };
    for (let k = 0; k < 40 && clash(spot.x, spot.y); k++) spot = { x: spot.x, y: spot.y + 90 };
    push({ doc: { ...doc, nodes: [...doc.nodes, n], edges: [...doc.edges, { id: newId(), from: nodeId, fromPort: port, to: n.id, toPort: endDef.inputs?.[0]?.name ?? "in" }] },
      layout: { ...layout, nodes: { ...layout.nodes, [n.id]: spot } } });
    setSaid("Added 'Leave the file alone' on that port. Save the draft to keep it.");
  };
  const save = async () => {
    const r = await act(saveDraft(id, doc, layout, etag), setSaid);
    if (!r) return null;
    setEtag(r.etag); setSaved(cur); setChecked({ issues: r.validation, stackable: r.stackable, stepSummaries: r.stepSummaries }); setSaid("Draft saved.");
    return r;
  };
  const openPublish = async () => { if (dirty && !(await save())) return; const p = await act(publishPreview(id), setSaid); if (p) setPanel(p); };
  const doPublish = async () => {
    const r = await act(publish(id, etag), setSaid); if (!r) return;
    const d = await act(getDraft(id), setSaid); if (d) setEtag(d.etag);
    setPanel(null); setVer(r.number); setSaid("Published v" + r.number + ".");
  };

  const issues = checked.issues ?? [], errors = issues.filter((i) => i.severity === "error");
  const badValues = issues.filter((i) => i.code === "invalid_config"); // #165: blocks Save draft and Publish until fixed
  // {{name}} fields with a name that isn't a variable (or an unclosed {{): Publish waits for them, once the list has loaded
  const tv = takesVars(defs), vl = useData(() => varListFor(id, tv), [id, tv]).data, varBad = vl ? varProblems(doc.nodes, defs, withOwn(vl.variables, doc.variables ?? [])) : [];
  const picked = doc.nodes.find((n) => n.id === sel), pickedDef = picked && defs.find((x) => x.type === picked.type);
  const nameOf = (nid?: string) => { const t = doc.nodes.find((n) => n.id === nid); return t ? t.name ?? defs.find((x) => x.type === t.type)?.name ?? t.type : ""; };
  const shown = nodes.map((n) => (drag[n.id] ? { ...n, position: drag[n.id] } : n));
  const probs = issues.length;
  return (
    <div className="editor graphpage">
      {/* FB6 (flow-builder.md 4.1, ui-review C10): the editor is a workspace: a 48 px toolbar, then a frame that fills
          the rest of the window with the palette, the canvas and the inspector inside it. One primary: Publish. */}
      <div className="edbar" role="toolbar" aria-label={"Editing " + name}>
        <span className="muted nowrap"><Link to="/workflows">Workflows</Link> /</span><h1 className="nm">{name}</h1>
        <span className="pill">{ver == null || dirty ? "Draft" : "Published"}</span>
        <span className="muted nowrap desk edver">{ver == null ? "Not published yet" : "v" + ver + " published" + (dirty ? " · draft" : "")}</span>
        {checked.issues && <span className={"pill " + (errors.length ? "bad" : probs ? "warn" : "ok")}><Icon id={probs ? "i-alert" : "i-check-c"} />{probs ? (probs === 1 ? "1 problem" : probs + " problems") : "No problems"}</span>}
        <div className="mid">
          <div className="edsegs" role="group" aria-label="View">
            {checked.stackable ? <Link to={"/workflows/" + id}>Steps</Link> : <span role="link" aria-disabled="true" tabIndex={0} aria-describedby="steps-why" title="This workflow has more than one path, so it is edited here">Steps</span>}
            <span aria-current="page">Graph</span>
          </div>
          {!checked.stackable && <span id="steps-why" className="sr">Steps isn't available: this workflow has more than one path, so it is edited here.</span>}
          {edit && <><span className="vr" aria-hidden="true" />
            <button className="btn sm icon ghost" type="button" title="Undo" aria-label="Undo" disabled={hist.at === 0} onClick={() => (setBar(null), setHist({ ...hist, at: hist.at - 1 }))}><Icon id="i-undo" /></button>
            <button className="btn sm icon ghost" type="button" title="Redo" aria-label="Redo" disabled={hist.at === hist.list.length - 1} onClick={() => (setBar(null), setHist({ ...hist, at: hist.at + 1 }))}><Icon id="i-redo" /></button></>}
        </div>
        {edit && <>
          <button className="btn sm" type="button" disabled={!dirty || badValues.length > 0} title={badValues.length ? "Fix the settings marked in red first" : undefined} onClick={save}><Icon id="i-save" />Save draft</button>
          <button className="btn sm primary" type="button" disabled={badValues.length > 0 || varBad.length > 0} title={badValues.length ? "Fix the settings marked in red first" : varBad.length ? "Fix the variable names first" : undefined} onClick={openPublish}>Publish</button>
        </>}
      </div>
      <div className="edstatus">
      <p className="muted sm phone" role="note">Editing works best on a larger screen. Here you can look around, zoom and see each step.</p>
      <p className="muted" role="status">{said}{bar && edit && <> <button className="linkbtn" type="button" onClick={undoDelete}>Undo</button></>}</p>
      {edit && badValues.length > 0 && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{badValues.length === 1 ? "A setting has a value it can't use" : badValues.length + " settings have values they can't use"} ({[...new Set(badValues.map((i) => nameOf(i.nodeId)))].join(", ")}). Fix {badValues.length === 1 ? "it" : "them"} to save the draft or publish.</span></p>}
      {edit && varBad.length > 0 && <p className="st bad" role="alert"><Icon id="i-alert" /><span>Fix {varBad.length === 1 ? "1 field" : varBad.length + " fields"} before publishing. <button className="linkbtn" type="button" onClick={() => (setSel(varBad[0].nodeId), setSelEdge(null))}>{varBad[0].title}</button></span></p>}
      </div>
      {panel && (
        <section className="card edpanel" aria-labelledby="pub-h"><div className="hd"><h2 id="pub-h">Publish v{panel.nextNumber}</h2></div><div className="bd stack">
          {panel.changes.length ? <ul style={{ margin: 0 }}>{panel.changes.map((c) => <li key={c.nodeId + c.kind}>{c.kind === "added" ? "Added: " : c.kind === "removed" ? "Removed: " : ""}{c.summary}</li>)}</ul> : <p className="muted">No changes since the last version.</p>}
          <p>{panel.libraries.length ? "Used by " + panel.libraries.map((l) => l.name).join(", ") + "." : "No library uses it yet."}{panel.nextNumber > 1 && " Files already converting finish with v" + (panel.nextNumber - 1) + ". Files still waiting use v" + panel.nextNumber + " when they start."}</p>
          {panel.changesOriginals && <span className="st warn"><Icon id="i-alert" /><span>This version replaces original files.</span></span>}
          {(panel.validation ?? []).filter((v) => v.severity === "error").map((v, k) => <span key={k} className="st bad"><Icon id="i-alert" /><span>{v.message}</span></span>)}
          <div className="row"><button className="btn primary" type="button" disabled={(panel.validation ?? []).some((v) => v.severity === "error")} onClick={doPublish}>Publish v{panel.nextNumber}</button>
            <button className="btn ghost" type="button" onClick={() => setPanel(null)}>Cancel</button></div>
        </div></section>)}
      <div className={"edframe" + (edit ? " withpal" : "")}>
        {edit && <Palette defs={defs} onAdd={addFromPalette} dragType={dragType} onDrag={dragging} searchRef={searchRef} />}
        <div className="gcanvas" aria-label={"The steps of " + name + " as a graph"} onDragOver={edit ? onDragOver : undefined} onDragLeave={edit ? (e: DragEvent) => { if (!e.currentTarget.contains(e.relatedTarget as HTMLElement | null)) (setGhost(null), setPointer(null)); } : undefined} onDrop={edit ? onDrop : undefined} onBlur={() => (nudgeRun.current = null)}>
          {ghost && <div className="gdrop" aria-hidden="true" style={{ left: ghost.x, top: ghost.y, width: W * rf.getZoom(), height: 72 * rf.getZoom() }} />}
          {pointer && dragType && <div className="gghost" aria-hidden="true" style={{ left: pointer.x + 12, top: pointer.y + 12 }}>{defs.find((x) => x.type === dragType)?.name}</div>}
          <ReactFlow nodes={shown} edges={edges} nodeTypes={nodeTypes} onInit={frame} minZoom={0.2} maxZoom={2} nodesDraggable={edit} nodesConnectable={edit} elementsSelectable
            onConnect={connect} isValidConnection={valid} onNodesChange={onNodesChange} onDelete={({ nodes: ns, edges: es }) => (ns.length ? removeNodes(ns.map((n) => n.id)) : removeEdges(es.map((e) => e.id)))} deleteKeyCode={edit ? ["Delete", "Backspace"] : null}
            onNodeClick={(_, n) => (setSel(n.id), setSelEdge(null), unpend(n.id))} onEdgeClick={(_, e) => (setSelEdge(e.id), setSel(null), unpend(null))} onPaneClick={() => (setSel(null), setSelEdge(null), unpend(null))}>
            <Background gap={20} />
            <MiniMap pannable zoomable ariaLabel="Overview of the graph" nodeColor="var(--border-strong)" nodeStrokeColor="var(--text-2)" nodeBorderRadius={4} />
            <Zoom />
          </ReactFlow>
        </div>
        <OwnVars.Provider value={doc.variables ?? []}><aside className="edinsp ginspect" aria-label="Selected step and problems">
          <div className="stack">
            {selEdge && edit ? <div className="stack"><p>{(() => { const e = doc.edges.find((x) => x.id === selEdge); return e ? nameOf(e.from) + " → " + nameOf(e.to) : ""; })()}</p>
              <div className="row"><button className="btn sm" type="button" onClick={() => removeEdges([selEdge])}><Icon id="i-trash" />Delete this connection</button></div></div>
            : picked ? <div className="stack">
              <h2 className="h2">{picked.name ?? pickedDef?.name ?? picked.type}</h2>
              <p className="muted sm">{pickedDef?.typeLabel}{!picked.enabled && " · Off"}</p>
              {checked.stepSummaries?.[picked.id] && <p>{checked.stepSummaries[picked.id]}</p>}
              {pickedDef?.description && <p className="sm">{pickedDef.description}</p>}
              {/* the same full sentence as the pill's title, readable without hover (touch has no title tooltip): the same rule as the canvas's free() */}
              {(pickedDef?.outputs ?? []).filter((p) => p.outcome !== "success" && !doc.edges.some((e) => e.from === picked.id && e.fromPort === p.name) && !issues.some((i) => i.nodeId === picked.id && i.code === "unconnected_port" && i.port === p.name)).map((p) => <p key={p.name} className="muted sm">{notLinked(p, pickedDef)}</p>)}
              {edit && pickedDef?.category !== "input" && <div className="row"><button className="btn sm" type="button" onClick={() => removeNodes([picked.id])}><Icon id="i-trash" />Delete this step</button></div>}
              {/* step 2b (SCREENS V4 Inspector): the step's own form, the same as the step list's (schema-driven), with the server's problems inline */}
              {edit && pickedDef && !pickedDef.readOnly && <form className="stack ginform" onSubmit={(e) => e.preventDefault()} aria-label={"Settings of " + (picked.name ?? pickedDef.name)}>
                <div className="field"><label htmlFor={"gn-" + picked.id}>Name</label>
                  <input id={"gn-" + picked.id} className="input" value={picked.name ?? ""} placeholder={pickedDef.name} onChange={(e) => setNode({ ...picked, name: e.target.value || undefined })} /></div>
                {!pickedDef.alwaysOn && <label className="row nw"><input type="checkbox" role="switch" className="switch" checked={picked.enabled} onChange={(e) => setNode({ ...picked, enabled: e.target.checked })} /><span>On</span></label>}
                <Reg.Provider value={{ defs, words: checked.stepSummaries ?? {}, libs: [], wf: id }}>
                  <Options n={picked} issues={issues} set={(k, v) => setNode({ ...picked, config: { ...picked.config, [k]: v } })} />
                </Reg.Provider>
              </form>}
              {(pickedDef?.outputs?.length ?? 0) > 0 && <dl className="kv sm">{pickedDef!.outputs.map((p) => <Fragment key={p.name}><dt>{p.label}</dt><dd>{doc.edges.filter((e) => e.from === picked.id && e.fromPort === p.name).map((e) => nameOf(e.to)).join(", ") || "Nothing connected"}</dd></Fragment>)}</dl>}
            </div> : <p className="muted">{edit ? "Select a step to see it. Drag from a port on the right of a step to another step to connect them; select a connection to delete it." : "Select a step to see what it does."}</p>}
            <h3 className="h3">Problems{issues.length ? " (" + issues.length + ")" : ""}</h3>
            {!issues.length ? <p className="st ok"><Icon id="i-check-c" /><span>No problems.</span></p> : <ul className="gprobs">{issues.map((i, k) => (
              <li key={k} className={"st " + (i.severity === "error" ? "bad" : "warn")}><Icon id="i-alert" /><span>
                {i.nodeId ? <button className="linkbtn" type="button" onClick={() => (setSel(i.nodeId!), setSelEdge(null))}>{/^In '/.test(i.message) ? null : <><strong>{nameOf(i.nodeId)}:</strong> </>}{i.message}</button> : i.message}
                {edit && i.code === "unconnected_port" && i.nodeId && i.port && endDef && <> <button className="btn sm" type="button" onClick={() => leaveAlone(i.nodeId!, i.port!)}>Leave the file alone here</button></>}</span></li>))}</ul>}
            {errors.length > 0 && <p className="sm muted">Publish waits until the problems are fixed.</p>}
          </div>
        </aside></OwnVars.Provider>
      </div>
    </div>);
}

// The palette (SCREENS V4): search, then the step types by category; double-click or Enter adds one at the view's
// centre. The library trigger and migration-only types aren't offered; categories with nothing to add aren't shown.
const CAT_NAME: Record<string, string> = { filter: "Checks", video: "Video", audio: "Audio", subtitles: "Subtitles", container: "File type", verify: "Check the result", output: "Save", flow: "Flow" };
function Palette({ defs, onAdd, dragType, onDrag, searchRef }: { defs: StepDefinition[]; onAdd: (d: StepDefinition) => void; dragType: string | null; onDrag: (t: string | null) => void; searchRef: RefObject<HTMLInputElement | null> }) {
  // the step list's "Add step" lands here with ?add=<type> (its conditions do): the search starts on that step's name
  const [q, setQ] = useState(() => defs.find((x) => x.type === new URLSearchParams(window.location.search).get("add"))?.name ?? "");
  const shown = defs.filter((d) => d.category !== "input" && !d.readOnly && !d.deprecated && (!q || (d.name + " " + d.description + " " + d.typeLabel).toLowerCase().includes(q.toLowerCase())));
  const cats = Object.keys(CAT_NAME).filter((c) => shown.some((d) => d.category === c));
  return (
    <aside className="edpal gpal" aria-label="Steps you can add">
      <div className="stack">
        <input ref={searchRef} className="input" type="search" placeholder="Search steps" aria-label="Search steps" value={q} onChange={(e) => setQ(e.target.value)} />
        {!cats.length && <p className="muted sm">No step matches.</p>}
        {cats.map((c) => (
          <details key={c} open className="gfold"><summary className="lab">{CAT_NAME[c]}</summary>
            <ul className="gpl">{shown.filter((d) => d.category === c).map((d) => (
              <li key={d.type}><button type="button" className={"gpi" + (dragType === d.type ? " dragging" : "")} draggable style={{ ["--cat" as string]: CAT[d.category] }} title={d.description + " Drag it onto the canvas, or double-click or press Enter to add it."}
                onDragStart={(e) => { e.dataTransfer.setData(PALETTE_TYPE, d.type); e.dataTransfer.effectAllowed = "copy"; onDrag(d.type); }} onDragEnd={() => onDrag(null)}
                onDoubleClick={() => onAdd(d)} onKeyDown={(e) => e.key === "Enter" && (e.preventDefault(), onAdd(d))} aria-label={"Add " + d.name + ": " + d.description}>
                <Icon id={ICON[d.icon] ?? "i-workflow"} /><span className="sp"><strong>{d.name}</strong><small>{d.description}</small></span><span className="tt">{d.typeLabel}</span></button></li>))}</ul>
          </details>))}
        <p className="hint">Drag a step onto the canvas, or double-click it (or press Enter) to add it below the selected step. Dropped on a connection, it goes between its two steps.</p>
      </div>
    </aside>);
}

const newId = () => (crypto.randomUUID ? crypto.randomUUID() : "n" + Math.random().toString(36).slice(2));
async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}
function useMedia(q: string) {
  const [m, setM] = useState(() => matchMedia(q).matches);
  useEffect(() => { const l = matchMedia(q), f = () => setM(l.matches); l.addEventListener("change", f); return () => l.removeEventListener("change", f); }, [q]);
  return m;
}

// The opening view and Fit never go below 75%. When the whole graph would fit smaller, it opens at 75% with the library
// files step (the start; else the top-left-most step) at the top-left, and the person pans from there. Manual zoom keeps 20%.
const FLOOR = 0.75;
type Flow = { getNodes(): { position: { x: number; y: number }; measured?: { width?: number; height?: number }; initialHeight?: number; data: unknown }[]; fitView(o: { padding: number }): unknown; setViewport(v: { x: number; y: number; zoom: number }): unknown };
function frame(rf: Flow) {
  const nodes = rf.getNodes(), box = document.querySelector(".gcanvas")?.getBoundingClientRect();
  if (!nodes.length || !box) return;
  const left = Math.min(...nodes.map((n) => n.position.x)), top = Math.min(...nodes.map((n) => n.position.y));
  const right = Math.max(...nodes.map((n) => n.position.x + (n.measured?.width ?? W))), bottom = Math.max(...nodes.map((n) => n.position.y + (n.measured?.height ?? n.initialHeight ?? 100)));
  if (Math.min(box.width / ((right - left) * 1.15), box.height / ((bottom - top) * 1.15)) >= FLOOR) { rf.fitView({ padding: 0.15 }); return; } // fitView's own fit: padding 0.15 adds 15% to the size
  const start = nodes.find((n) => (n.data as { node?: Node }).node?.type === "input.library") ?? nodes.reduce((a, c) => (c.position.y < a.position.y || (c.position.y === a.position.y && c.position.x < a.position.x) ? c : a));
  rf.setViewport({ x: 24 - start.position.x * FLOOR, y: 24 - start.position.y * FLOOR, zoom: FLOOR });
}

// Toolbar zoom (SCREENS V4: Fit, Zoom -, %, +).
function Zoom() {
  const rf = useReactFlow(), { zoom } = useViewport();
  return (
    <div className="gzoom" role="toolbar" aria-label="Zoom">
      <button className="btn sm icon ghost" type="button" aria-label="Zoom out" onClick={() => rf.zoomOut()}><Icon id="i-minus" /></button>
      <span className="sm" aria-live="polite">{Math.round(zoom * 100)}%</span>
      <button className="btn sm icon ghost" type="button" aria-label="Zoom in" onClick={() => rf.zoomIn()}><Icon id="i-plus" /></button>
      <button className="btn sm ghost" type="button" onClick={() => frame(rf)}>Fit</button>
    </div>);
}
