// V3b Stack view (spec 5.7; DESIGN/v2 SCREENS V3, mockups/workflow-stack.html and -readonly.html), on the typed mock
// of V1 (mock/steps.ts mirrors V1's registry, summaries and validation). Not here: drag to reorder and "+ Add step"
// (V1 runs the preset's chain in its order), Test run (V5), History, Export, Open graph (V4).
import { Component, useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { getDraft, publish, publishPreview, saveDraft, setWorkflow, useMock } from "../mock";
import { order, stackable, stepDef, summary, validate } from "../mock/steps";
import { onLeave } from "../router";
import type { JSONSchema, Node, PublishPreview, ValidationIssue, WorkflowDoc } from "../types/v2";

// The server's icon keys (registry) -> design's sprite.
const ICON: Record<string, string> = { folder: "i-folder", filter: "i-shield", video: "i-video", audio: "i-audio", subtitles: "i-subtitle", file: "i-file", check: "i-checks", save: "i-save", cond: "i-cond", history: "i-history" };
// Design's words around the forms (SCREENS V3; the old Basic/Standard editor's lines).
const PICTURE = ["The smallest files. Fine detail can soften, mostly in dark or busy scenes.", "Small files, with a small loss of fine detail.",
  "Little visible change for most people. A good start.", "Keeps more detail. Files stay larger.", "Hard to tell from the original. Files stay largest."];
const POLICY_HINT: Record<string, string> = {
  separate_folder: "New files go to the folder below, in the same sub-folders. Your originals aren't changed, so no space is freed until you remove them yourself.",
  replace: "The original is kept for 7 days (Settings > Safety), then deleted. This frees space. You can get an original back from its job.",
  beside: "Both stay. Nothing is deleted.",
};
const MORE: Record<string, string[]> = { "video.convert": ["format", "resolution", "maxBitrate", "speed"] };

type Props = Record<string, JSONSchema>;
const labels = (s: JSONSchema) => ((s.enum as unknown[]) ?? []).map((v, i) => [v, ((s["x-enumLabels"] as string[]) ?? [])[i] ?? String(v)] as [unknown, string]);
const widget = (s: JSONSchema) => ((s["x-ui"] as { widget?: string } | undefined)?.widget ?? "");

// One option from the step's configSchema, as a native labelled control (design review rule 1).
function Field({ id, name, s, value, set }: { id: string; name: string; s: JSONSchema; value: unknown; set: (v: unknown) => void }) {
  // The schema's title; without one (V1's "ignore" has none), the key in plain form.
  const title = (s.title as string) ?? name.charAt(0).toUpperCase() + name.slice(1).replace(/([A-Z])/g, " $1").toLowerCase(), fid = id + "-" + name;
  if (widget(s) === "slider") {
    const opts = labels(s), i = Math.max(0, opts.findIndex((o) => o[0] === value));
    return (
      <div className="field"><label htmlFor={fid}>{title}</label>
        <input id={fid} type="range" min={0} max={opts.length - 1} step={1} value={i} aria-valuetext={`${i + 1} of ${opts.length}, ${opts[i][1]}`} onChange={(e) => set(opts[Number(e.target.value)][0])} />
        <div className="row sm muted" style={{ justifyContent: "space-between", flexWrap: "nowrap", gap: 2 }} aria-hidden="true">
          {opts.map((o, k) => (k === i ? <strong key={k} style={{ color: "var(--text)" }}>{o[1]}</strong> : <span key={k}>{o[1]}</span>))}</div>
      </div>);
  }
  if (s.type === "boolean")
    return <label className="row nw" htmlFor={fid}><input id={fid} type="checkbox" role="switch" className="switch" checked={value === true} onChange={(e) => set(e.target.checked)} /><span>{title}</span></label>;
  if (s.enum && widget(s) === "segmented") {
    const avail = s["x-available"] as unknown[] | undefined;
    return (
      <fieldset className="field choice"><legend className="lab">{title}</legend><div className="row">
        {labels(s).map(([v, l]) => (
          <label key={String(v)} className="radio"><input type="radio" name={fid} checked={value === v} disabled={!!avail && !avail.includes(v)} onChange={() => set(v)} /><span>{l}</span></label>))}
      </div></fieldset>);
  }
  if (s.enum)
    return <div className="field"><label htmlFor={fid}>{title}</label><select id={fid} className="input" value={String(value)} onChange={(e) => set(labels(s).find((o) => String(o[0]) === e.target.value)?.[0])}>
      {labels(s).map(([v, l]) => <option key={String(v)} value={String(v)}>{l}</option>)}</select>{typeof s.description === "string" && <span className="hint">{s.description}</span>}</div>;
  const hint = typeof s.description === "string" ? <span className="hint" id={fid + "-h"}>{s.description}{s.type === "array" ? " One per line." : ""}</span> : null;
  if (s.type === "array")
    return <div className="field"><label htmlFor={fid}>{title}</label><textarea id={fid} className="input" rows={2} value={((value as string[]) ?? []).join("\n")}
      onChange={(e) => set(e.target.value.split("\n").map((x) => x.trim()).filter(Boolean))} aria-describedby={hint ? fid + "-h" : undefined} />{hint}</div>;
  return <div className="field"><label htmlFor={fid}>{title}</label><input id={fid} className="input" value={String(value ?? "")} onChange={(e) => set(e.target.value)} aria-describedby={hint ? fid + "-h" : undefined} />{hint}</div>;
}

function Options({ n, set, issues }: { n: Node; set: (k: string, v: unknown) => void; issues: ValidationIssue[] }) {
  const def = stepDef(n.type)!, props = Object.fromEntries(Object.entries((def.configSchema.properties ?? {}) as Props).filter(([, s]) => !s.readOnly)) as Props, c = n.config;
  const keys = Object.keys(props).filter((k) => !(MORE[n.type] ?? []).includes(k)), more = (MORE[n.type] ?? []).filter((k) => props[k]);
  const field = (k: string) => <Field key={k} id={n.id} name={k} s={props[k]} value={c[k] ?? props[k].default} set={(v) => set(k, v)} />;
  const problem = (path: string) => issues.filter((i) => i.nodeId === n.id && i.configPath === path).map((i) => <span key={i.code} className="hint" style={{ color: "var(--error-text)" }}>{i.message}</span>);
  if (n.type === "output.commit") {
    const policy = (c.policy as string) ?? "separate_folder", mode = (c.folderMode as string) ?? "next_to_library";
    return (
      <div className="stack">
        <fieldset className="field choice stack"><legend className="lab">Save the result</legend>
          {labels(props.policy).map(([v, l]) => (
            <label key={String(v)} className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-policy"} checked={policy === v} onChange={() => set("policy", v)} />
              <span><strong>{l}</strong><span className="hint" style={{ display: "block" }}>{POLICY_HINT[String(v)]}</span>
                {v === "replace" && policy === "replace" && <span className="st warn" style={{ display: "flex", marginTop: 4 }}><Icon id="i-alert" /><span>Your original files will be replaced.</span></span>}</span>
            </label>))}
        </fieldset>
        {policy === "separate_folder" && (
          <fieldset className="field choice"><legend className="lab">Where</legend><div className="row">
            {labels(props.folderMode).map(([v, l]) => <label key={String(v)} className="radio"><input type="radio" name={n.id + "-mode"} checked={mode === v} onChange={() => set("folderMode", v)} /><span>{l}</span></label>)}
          </div>{mode === "next_to_library" && <span className="hint">Each library's new files go to a folder beside it, for example /media/movies → /media/movies (shrunk).</span>}{problem("/folderMode")}</fieldset>)}
        {policy === "separate_folder" && mode === "one_folder" && <>{field("folder")}{problem("/folder")}</>}
      </div>);
  }
  return (
    <div className="stack">
      {keys.map(field)}
      {n.type === "video.convert" && <>
        <div className="note"><Icon id="i-film" /><div><strong>Picture</strong><br />{PICTURE[(Number(c.quality) || 3) - 1]}</div></div>
        <p className="hint">Every video not already in this format is converted, even if the new file isn't smaller, so your library ends up in one format. Jobs shows the size before and after.</p></>}
      {more.length > 0 && <details><summary className="btn sm" style={{ display: "inline-flex" }}>More options</summary><div className="grid cols2" style={{ marginTop: 12 }}>{more.map(field)}</div></details>}
    </div>);
}

function Step({ n, i, open, toggle, update, issues }: { n: Node; i: number; open: boolean; toggle: () => void; update: (n: Node) => void; issues: ValidationIssue[] }) {
  const def = stepDef(n.type), mine = issues.filter((x) => x.nodeId === n.id);
  // A read-only step (an earlier install's settings) or one this page doesn't know (a newer server, a plugin not
  // installed): shown with its type, its settings kept as they are, never edited (review P2).
  if (!def || def.readOnly) return (
    <section className="card step" aria-labelledby={n.id + "-name"}><div className="row nw" style={{ padding: "10px var(--pad)" }}><span className="stepn">{i + 1}</span>
      <span style={{ color: "var(--accent-text)" }}><Icon id={(def && ICON[def.icon]) || "i-box"} /></span>
      <div className="sp" style={{ minWidth: 0 }}><strong id={n.id + "-name"}>{n.name ?? def?.name ?? "Unknown step"}</strong>
        <div className="muted">{(def && summary(n)) || "Type " + n.type}</div>
        <div className="sm muted">Its settings are kept as they are. {def ? def.description : "This version of the page doesn't know this step, so it only shows it."}</div></div>
      <span className="muted sm">Read-only</span></div></section>);
  const hasOptions = Object.values((def.configSchema.properties ?? {}) as Props).some((s) => !s.readOnly);
  return (
    <section className="card step" aria-labelledby={n.id + "-name"}>
      <div className="row nw" style={{ padding: "10px var(--pad)" }}>
        <span className="stepn">{i + 1}</span><span style={{ color: "var(--accent-text)" }}><Icon id={ICON[def.icon] ?? "i-box"} /></span>
        <div className="sp" style={{ minWidth: 0 }}><strong id={n.id + "-name"}>{n.name ?? def.name}</strong><div className="muted">{summary(n)}</div>
          {mine.length > 0 && <span className="st bad"><Icon id="i-alert" /><span>{mine.length === 1 ? "1 problem" : mine.length + " problems"}</span></span>}</div>
        {def.alwaysOn ? <span className="muted sm">Always on</span>
          : <input type="checkbox" role="switch" className="switch" aria-label={(def.name) + " on"} checked={n.enabled} onChange={(e) => update({ ...n, enabled: e.target.checked })} />}
        {hasOptions && <button className="btn sm icon ghost" type="button" aria-expanded={open} aria-controls={n.id + "-opts"} aria-label={"Options for " + def.name} onClick={toggle}><Icon id={open ? "i-chev-d" : "i-chev-r"} /></button>}
      </div>
      {open && hasOptions && <div className="bd" id={n.id + "-opts"} style={{ borderTop: "1px solid var(--border)" }}>
        <Options n={n} issues={issues} set={(k, v) => update({ ...n, config: { ...n.config, [k]: v } })} /></div>}
    </section>);
}

// A workflow with branches: the steps in order with the branches named, nothing editable (spec 5.7; SCREENS V3).
function ReadOnly({ doc }: { doc: WorkflowDoc }) {
  const name = (id: string) => { const n = doc.nodes.find((x) => x.id === id); return n ? (n.name ?? stepDef(n.type)?.name ?? n.type) + " (" + summary(n) + ")" : id; };
  return (
    <div className="stack">
      <div className="note"><Icon id="i-info" /><div>This workflow has conditions that the list can't show. The graph, where you can see and change them, comes in a later step of the new interface.</div></div>
      <ol className="card bd stack" style={{ margin: 0, paddingLeft: 36 }}>
        {doc.nodes.map((n) => {
          const out = doc.edges.filter((e) => e.from === n.id && e.fromPort !== "success");
          return <li key={n.id}><strong>{n.name ?? stepDef(n.type)?.name}</strong> <span className="muted">{summary(n)}</span>
            {out.length > 0 && <ul>{out.map((e) => <li key={e.id}>{e.label ?? e.fromPort}: {name(e.to)}</li>)}</ul>}</li>;
        })}
      </ol>
    </div>);
}

// Anything this page can't draw (an unexpected document) shows a plain message, not a blank page (review P2).
class Guard extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    return this.state.failed ? <div className="note bad"><Icon id="i-alert" /><div>This workflow can't be shown here yet. Nothing was changed.</div></div> : this.props.children;
  }
}

export function WorkflowStack({ id }: { id: string }) {
  return <Guard><StackPage id={id} /></Guard>;
}

function StackPage({ id }: { id: string }) {
  const s = useMock(), w = s.wf.find((x) => x.id === id);
  const first = useRef(getDraft(id));
  const [etag, setEtag] = useState(first.current?.etag ?? "");
  const [hist, setHist] = useState<{ docs: WorkflowDoc[]; at: number }>({ docs: first.current ? [first.current.workflow] : [], at: 0 });
  const [saved, setSaved] = useState(first.current?.workflow);
  const [open, setOpen] = useState<string | null>(null), [said, setSaid] = useState(""), [checked, setChecked] = useState<ValidationIssue[] | null>(null);
  const [panel, setPanel] = useState<PublishPreview | null>(null), [renaming, setRenaming] = useState(false);
  const doc = hist.docs[hist.at], dirty = doc !== saved;
  // Leaving with unsaved changes asks, as Appearance does: the router can't stop a move, so it offers to save first.
  const now = useRef({ dirty, doc, etag }); now.current = { dirty, doc, etag };
  useEffect(() => {
    onLeave(() => {
      const { dirty, doc, etag } = now.current;
      if (dirty && doc && window.confirm("Save your changes to this draft before leaving?\n\nOK saves them. Cancel leaves them unsaved.")) saveDraft(id, doc, etag);
    });
    return () => onLeave(null);
  }, [id]);
  useEffect(() => { // closing or reloading the tab with unsaved changes: the browser's own question (review P3)
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  if (!w || !doc) return <div className="card"><div className="bd">There is no such workflow. It may have been deleted.</div></div>;
  const edit = (d: WorkflowDoc) => (setHist({ docs: [...hist.docs.slice(0, hist.at + 1), d], at: hist.at + 1 }), setChecked(null), setPanel(null), setSaid(""));
  const update = (n: Node) => edit({ ...doc, nodes: doc.nodes.map((x) => (x.id === n.id ? n : x)) });
  const save = () => {
    const r = saveDraft(id, doc, etag);
    if (r.error || !r.draft) return (setSaid(r.error ?? ""), null);
    setEtag(r.draft.etag); setSaved(doc); setSaid("Draft saved."); return r.draft;
  };
  const openPublish = () => { const r = dirty ? save() : true; if (r) setPanel(publishPreview(id)); };
  const issues = checked ?? (dirty ? [] : validate(doc));
  const canStack = stackable(doc);
  return (
    <div className="stack">
      <section className="card"><div className="bd stack">
        <div className="row">
          {renaming ? <form className="row" onSubmit={(e) => { e.preventDefault(); const v = (e.currentTarget.elements.namedItem("nm") as HTMLInputElement).value.trim(); if (v) setWorkflow(id, { name: v }); setRenaming(false); }}>
            <label className="sr" htmlFor="wf-rename">Name</label><input id="wf-rename" name="nm" className="input" defaultValue={w.name} autoFocus style={{ width: "auto" }} />
            <button className="btn sm" type="submit">Rename</button><button className="btn sm ghost" type="button" onClick={() => setRenaming(false)}>Cancel</button></form>
            : <><h1 style={{ fontSize: "var(--fs-lg)" }}>{w.name}</h1><button className="btn sm ghost" type="button" onClick={() => setRenaming(true)} aria-label={"Rename " + w.name}>Rename</button></>}
          <span className="tag">{w.version == null || w.draftDiffers || dirty ? "Draft" : "Published"}</span>
          <span className="muted">{w.version == null ? "Not published yet" : "v" + w.version + " published" + (w.draftDiffers || dirty ? " · v" + (w.version + 1) + " draft" : "")}</span>
        </div>
        {canStack && <div className="row">
          <button className="btn sm" type="button" disabled={hist.at === 0} onClick={() => setHist({ ...hist, at: hist.at - 1 })}><Icon id="i-undo" />Undo</button>
          <button className="btn sm" type="button" disabled={hist.at === hist.docs.length - 1} onClick={() => setHist({ ...hist, at: hist.at + 1 })}><Icon id="i-redo" />Redo</button>
          <button className="btn sm" type="button" onClick={() => setChecked(validate(doc))}><Icon id="i-checks" />Check</button>
          <span className="sp" />
          <button className="btn sm" type="button" disabled={!dirty} onClick={save}><Icon id="i-save" />Save draft</button>
          <button className="btn sm primary" type="button" onClick={openPublish}>Publish</button>
        </div>}
        {canStack && (checked || !dirty) && (issues.length === 0 ? <span className="st ok"><Icon id="i-check-c" /><span>Check: no problems</span></span>
          : <span className="st bad"><Icon id="i-alert" /><span>Check: {issues.length === 1 ? "1 problem" : issues.length + " problems"}. Publish waits until it's fixed.</span></span>)}
        <span className="muted" role="status">{said}</span>
      </div></section>

      {panel && (
        <section className="card" aria-labelledby="pub-h"><div className="hd"><h2 id="pub-h">Publish v{panel.nextNumber}</h2></div><div className="bd stack">
          {panel.changes.length ? <ul style={{ margin: 0 }}>{panel.changes.map((c) => <li key={c.nodeId + c.kind}>{c.kind === "added" ? "Added: " : c.kind === "removed" ? "Removed: " : ""}{c.summary}</li>)}</ul>
            : <p className="muted">No changes since the last version.</p>}
          <p>{panel.libraries.length ? "Used by " + panel.libraries.map((l) => l.name).join(", ") + "." : "No library uses it yet."}
            {/* contract correction 22:24Z: waiting jobs take the newest published version when they start (SCREENS V3) */}
            {panel.nextNumber > 1 && " Files already converting finish with v" + (panel.nextNumber - 1) + ". Files still waiting use v" + panel.nextNumber + " when they start."}</p>
          {panel.changesOriginals && <span className="st warn"><Icon id="i-alert" /><span>This version replaces original files.</span></span>}
          {(panel.validation ?? []).filter((v) => v.severity === "error").map((v) => <span key={v.code + v.configPath} className="st bad"><Icon id="i-alert" /><span>{v.message}</span></span>)}
          <div className="row">
            <button className="btn primary" type="button" disabled={(panel.validation ?? []).some((v) => v.severity === "error")}
              onClick={() => { const r = publish(id, etag); if (r.error) setSaid(r.error); else { const d = getDraft(id)!; setEtag(d.etag); setSaved(doc); setPanel(null); setSaid("Published v" + r.version + "."); } }}>Publish v{panel.nextNumber}</button>
            <button className="btn ghost" type="button" onClick={() => setPanel(null)}>Cancel</button></div>
        </div></section>)}

      {canStack ? <div className="stack">{order(doc).map((n, i) => (
        <Step key={n.id} n={n} i={i} open={open === n.id} toggle={() => setOpen(open === n.id ? null : n.id)} update={update} issues={issues} />))}</div>
        : <ReadOnly doc={doc} />}
    </div>);
}
