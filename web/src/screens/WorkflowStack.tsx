// V3b Stack view (spec 5.7; DESIGN/v2 SCREENS V3, mockups/workflow-stack.html and -readonly.html) on the real /api/v2
// (#109: step definitions, the draft, validate, publish), or the mock with ?mock=1 (mock/steps.ts mirrors V1). The step
// words and problems come from the server's validate, asked again shortly after each change. Not here: drag to reorder and "+ Add step"
// (V1 runs the preset's chain in its order), Test run (V5), History, Export, Open graph (V4).
import { PageHead } from "../PageHead";
import { Component, createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed, type Answer } from "../api";
import { order } from "../mock/steps"; // the steps in running order: a property of the document, not data
import { useData } from "../useData";
import { getDraft, getLadder, getWorkflow, patchWorkflow, publish, publishPreview, saveDraft, stepDefinitions, validateDoc, type Checked, type Ladder } from "../workflowApi";
import { Link, onLeave } from "../router";
import { Browse } from "../FolderPicker";
import { listLibraries, shrunk } from "../libraryApi";
import type { JSONSchema, Node, PublishPreview, StepDefinition, ValidationIssue, Workflow, WorkflowDoc, WorkflowRevision } from "../types/v2";

// The step definitions and the server's words for each step of the document being edited.
// libs: the folders of the libraries that use this workflow, for "Next to each library" (V5a).
export const Reg = createContext<{ defs: StepDefinition[]; words: Record<string, string>; libs: string[] }>({ defs: [], words: {}, libs: [] });
function useReg() { const r = useContext(Reg); return { stepDef: (t: string) => r.defs.find((d) => d.type === t), summary: (n: Node) => r.words[n.id] ?? "", libs: r.libs }; }

// The server's icon keys (registry) -> design's sprite.
export const ICON: Record<string, string> = { folder: "i-folder", filter: "i-shield", video: "i-video", audio: "i-audio", subtitles: "i-subtitle", file: "i-file", check: "i-checks", save: "i-save", cond: "i-cond", history: "i-history", branch: "i-branch", stop: "i-stop" }; // branch, stop: V6a check + end
// The words around each option come from the server's step definitions (#114: title, description, x-enumHints for
// each choice, x-placeholder); the page adds none of its own.
const MORE: Record<string, string[]> = { "video.convert": ["format", "resolution", "maxBitrate", "speed"] };

type Props = Record<string, JSONSchema>;
const labels = (s: JSONSchema) => ((s.enum as unknown[]) ?? []).map((v, i) => [v, ((s["x-enumLabels"] as string[]) ?? [])[i] ?? String(v)] as [unknown, string]);
const widget = (s: JSONSchema) => ((s["x-ui"] as { widget?: string } | undefined)?.widget ?? "");
// The hint for the chosen value (x-enumHints follows enum's order).
const hintFor = (s: JSONSchema, v: unknown) => { const e = (s.enum as unknown[]) ?? [], h = (s["x-enumHints"] as string[]) ?? []; return h[e.indexOf(v)] ?? ""; };
const Hint = ({ text }: { text: string }) => (text ? <span className="hint" aria-live="polite">{text}</span> : null);

// One option from the step's configSchema, as a native labelled control (design review rule 1).
function Field({ id, name, s, value, set }: { id: string; name: string; s: JSONSchema; value: unknown; set: (v: unknown) => void }) {
  // The schema's title; without one (V1's "ignore" has none), the key in plain form.
  const title = (s.title as string) ?? name.charAt(0).toUpperCase() + name.slice(1).replace(/([A-Z])/g, " $1").toLowerCase(), fid = id + "-" + name;
  if (widget(s) === "slider") {
    const opts = labels(s), i = Math.max(0, opts.findIndex((o) => o[0] === value));
    return (
      <div className="field"><label htmlFor={fid}>{title}</label>
        <strong className="slnow phone" aria-hidden="true">{opts[i][1]}</strong>
        <input id={fid} type="range" min={0} max={opts.length - 1} step={1} value={i} aria-valuetext={`${i + 1} of ${opts.length}, ${opts[i][1]}`} onChange={(e) => set(opts[Number(e.target.value)][0])} />
        <div className="row sm muted hide-sm" style={{ justifyContent: "space-between", flexWrap: "nowrap", gap: 2 }} aria-hidden="true">
          {opts.map((o, k) => (k === i ? <strong key={k} style={{ color: "var(--text)" }}>{o[1]}</strong> : <span key={k}>{o[1]}</span>))}</div>
        {/* phones (design, step 4): the chosen name above, only the two ends under the slider */}
        <div className="row sm muted phone slends" aria-hidden="true"><span>{opts[0][1]}</span><span>{opts[opts.length - 1][1]}</span></div>
        <Hint text={hintFor(s, value)} />
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
      </div><Hint text={hintFor(s, value)} />{typeof s.description === "string" && <span className="hint">{s.description}</span>}</fieldset>);
  }
  if (s.enum)
    return <div className="field"><label htmlFor={fid}>{title}</label><select id={fid} className="input" value={String(value)} onChange={(e) => set(labels(s).find((o) => String(o[0]) === e.target.value)?.[0])}>
      {labels(s).map(([v, l]) => <option key={String(v)} value={String(v)}>{l}</option>)}</select><Hint text={hintFor(s, value)} />{typeof s.description === "string" && <span className="hint">{s.description}</span>}</div>;
  // a list of choices (V6a check: "Video format is one of"): a checkbox per choice
  const items = s.items as JSONSchema | undefined;
  if (s.type === "array" && items?.enum) {
    const known = labels(items).map((o) => o[0]), on = ((value as unknown[]) ?? []).filter((x) => known.includes(x)); // a change drops values it doesn't know (#165: so a bad item can be fixed)
    return (
      <fieldset className="field choice"><legend className="lab">{title}</legend><div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
        {labels(items).map(([v, l]) => <label key={String(v)} className="row nw"><input type="checkbox" checked={on.includes(v)} onChange={(e) => set(e.target.checked ? [...on, v] : on.filter((x) => x !== v))} /><span>{l}</span></label>)}
      </div>{typeof s.description === "string" && <span className="hint">{s.description}</span>}</fieldset>);
  }
  const hint = typeof s.description === "string" ? <span className="hint" id={fid + "-h"}>{s.description}{s.type === "array" ? " One per line." : ""}</span> : null;
  if (s.type === "integer" || s.type === "number") // a number, saved as a number (empty: not set)
    return <div className="field"><label htmlFor={fid}>{title}</label><input id={fid} className="input" type="number" inputMode="decimal" min={s.minimum as number | undefined} step={s.type === "integer" ? 1 : "any"}
      value={value == null ? "" : String(value)} onChange={(e) => { const v = e.target.value; set(v === "" ? undefined : s.type === "integer" ? Math.trunc(Number(v)) : Number(v)); }} onBlur={(e) => { const lo = s.minimum as number | undefined; if (e.target.value !== "" && lo != null && Number(e.target.value) < lo) set(lo); }} aria-describedby={hint ? fid + "-h" : undefined} />{hint}</div>;
  if (s.type === "array")
    return <div className="field"><label htmlFor={fid}>{title}</label><textarea id={fid} className="input" rows={2} value={((value as string[]) ?? []).join("\n")}
      onChange={(e) => set(e.target.value.split("\n").map((x) => x.trim()).filter(Boolean))} aria-describedby={hint ? fid + "-h" : undefined} />{hint}</div>;
  return <div className="field"><label htmlFor={fid}>{title}</label><input id={fid} className="input" value={String(value ?? "")} placeholder={(s["x-placeholder"] as string) ?? undefined} onChange={(e) => set(s.type === "integer" || s.type === "number" ? (e.target.value === "" ? e.target.value : Number(e.target.value)) : e.target.value)} aria-describedby={hint ? fid + "-h" : undefined} />{hint}</div>;
}

// User review 11-12 item 1: the Convert step's bitrate choices with the engine's own numbers (GET /api/v2/bitrate-ladder).
// A fixed resolution shows that row's Mbps on each level; "Keep as is" names the chosen level's figure per row (the new
// file's own height picks it). VBR is offered only for the formats the ladder lists, has no "No limit", and its peak
// limit says the level's figure it must reach. null: the field isn't shown.
const mbps = (l: Ladder, n: number) => n.toLocaleString(undefined, { maximumFractionDigits: 1 }) + " " + l.unit;
// SCREENS V3 'Convert video, revised 11-13': the one line under the chosen slider position (its words, not the engine's hints)
const SLIDER_WORDS = ["The smallest files. Fine detail and dark scenes can look a little softer.", "Smaller files, with little you'd notice.", "A good picture at a much smaller size. Right for most libraries.", "Close to the original. Saves less space.", "Very hard to tell from the original. Saves the least space."];
function convertField(k: string, s: JSONSchema, c: Record<string, unknown>, l: Ladder | undefined): JSONSchema | null {
  if (k === "quality") return { ...s, "x-enumHints": SLIDER_WORDS };
  const fmt = String(c.format ?? "hevc"), vbr = c.rateControl === "vbr", row = l?.resolutionRow[String(c.resolution ?? "keep")];
  const byRow = l ? l.formats[fmt] ?? l.formats.hevc : undefined, level = String(c.maxBitrate ?? "none") as "low" | "medium" | "high" | "none";
  if (k === "rateControl") return !l || vbr || l.vbrFormats.includes(fmt) ? s : null; // a VBR choice stays visible so it can be undone
  if (k === "vbrMaxKbps") {
    if (!vbr) return null;
    const at = l && byRow && level !== "none" ? (row ? mbps(l, byRow[row][level]) + " (" + level + " for " + l.rows.find((x) => x.row === row)?.label + ")" : "the " + level + " figure for the new file's height") : "";
    return at ? { ...s, description: (s.description as string) + " VBR aims at " + at + "; the limit must be at least that." } : s;
  }
  if (k !== "maxBitrate") return s;
  const e = ((s.enum as unknown[]) ?? []).filter((v) => !(vbr && v === "none")), ls = labels(s);
  const named = e.map((v) => { const t = ls.find((x) => x[0] === v)?.[1] ?? String(v); return v === "none" || !l || !byRow || !row ? t : t + " · " + mbps(l, byRow[row][v as "low"]); });
  const perRow = l && byRow && !row && level !== "none" ? " " + level.charAt(0).toUpperCase() + level.slice(1) + " by the new file's height: " + l.rows.map((r) => r.label + " " + mbps(l, byRow[r.row][level])).join(", ") + "." : "";
  return { ...s, enum: e, "x-enumLabels": named, description: (s.description as string) + perRow };
}

export function Options({ n, set, issues }: { n: Node; set: (k: string, v: unknown) => void; issues: ValidationIssue[] }) {
  const { stepDef, libs } = useReg(), def = stepDef(n.type)!, props = Object.fromEntries(Object.entries((def.configSchema.properties ?? {}) as Props).filter(([, s]) => !s.readOnly)) as Props, c = n.config;
  const lad = useData(getLadder, []).data, conv = (k: string) => (n.type === "video.convert" ? convertField(k, props[k], c, lad) : props[k]);
  const keys = Object.keys(props).filter((k) => !(MORE[n.type] ?? []).includes(k)), more = (MORE[n.type] ?? []).filter((k) => props[k] && conv(k));
  // #165: a setting's own problems (invalid_config, keyed on the step + field) show at that field, which is marked
  const bad = (k: string) => issues.filter((i) => i.nodeId === n.id && (i.field === k || i.configPath === "/" + k) && i.code !== "unconnected_port");
  const field = (k: string) => { const b = bad(k), sk = conv(k); if (!sk) return null; return (
    <div key={k} className={b.length ? "fieldbad" : undefined} data-field={k} aria-invalid={b.length ? true : undefined}>
      <Field id={n.id} name={k} s={sk} value={c[k] ?? sk.default} set={(v) => set(k, v)} />
      {b.map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
    </div>); };
  // a problem in a field under 'More options' opens it by itself (review c0189fa P3), also when the problems list picks
  // this step; it isn't closed again when the last one is fixed, so the field doesn't vanish while it's being edited
  const moreRef = useRef<HTMLDetailsElement>(null), moreBad = more.some((k) => bad(k).length > 0);
  useEffect(() => { if (moreBad && moreRef.current) moreRef.current.open = true; }, [moreBad, n.id]);
  const problem = (path: string) => issues.filter((i) => i.nodeId === n.id && i.configPath === path).map((i) => <span key={i.code} className="hint" style={{ color: "var(--error-text)" }}>{i.message}</span>);
  if (n.type === "video.convert") {
    // A-API-SHAPE + SCREENS V3 'Convert video, revised 11-13': the slider, or custom settings that replace it (kind); the
    // rate control (level or VBR with a max; the old meanings read-only); the levels, greyed under VBR.
    const sel = c.kind === "selectors", rc = String(c.rateControl ?? "level"), vbr = rc === "vbr", legacy = rc.endsWith("_legacy"), av1 = String(c.format ?? "hevc") === "av1";
    const sl = lad?.slider.find((x) => x.position === Number(c.quality ?? 3) && x.format === String(c.format ?? "hevc"));
    return (
      <div className="stack">
        {legacy && <div className="note"><Icon id="i-info" /><div>This step was set up before 13 Nov and still makes the same files as before. Changing its bitrate settings switches it to the new rules.</div></div>}
        {sel ? <p className="note"><Icon id="i-info" /><span>Using your own settings. The slider is off. <button className="linkbtn" type="button" onClick={() => set("kind", "slider")}>Use the slider again</button></span></p> : <>
          {field("quality")}
          {sl && <p className="sm">{((labels(props.format).find((f) => f[0] === sl.format)?.[1] ?? sl.format) as string).split(" (")[0] + " · quality " + sl.crf + " (CRF) · " + (sl.keepsResolution ? "keeps the resolution" : "lowers the resolution") + " · " + (sl.capKbps ? "capped at " + sl.capKbps.toLocaleString() + " kbps" : "no bitrate cap") + " · usually about " + sl.saveLowPct + "-" + sl.saveHighPct + "% smaller"}</p>}
          <p className="hint">Lower quality numbers mean a better picture and bigger files. The number is for 1080p; 720p and smaller files use one lower, 4K one higher. Every file's picture is checked after converting, and the original stays if it doesn't pass.</p>
        </>}
        <details ref={moreRef} onToggle={(e) => e.currentTarget.open && !sel && set("kind", "selectors")}><summary className="btn sm" style={{ display: "inline-flex" }}>More options</summary>
          <div className="grid cols2" style={{ marginTop: 12 }}>
            {field("format")}{field("resolution")}{field("speed")}
            <fieldset className="field choice stack"><legend className="lab">Bitrate</legend>
              <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked={!vbr && !legacy} onChange={() => set("rateControl", "level")} />
                <span><strong>Bitrate level</strong><span className="hint" style={{ display: "block" }}>Aims for the level's bitrate for each file's resolution.</span></span></label>
              <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked={vbr} disabled={av1} onChange={() => set("rateControl", "vbr")} />
                <span><strong>VBR with a max</strong><span className="hint" style={{ display: "block" }}>Aims for a steady picture and uses only the bitrate each scene needs, never above your max.</span>
                  {av1 && <span className="hint" style={{ display: "block" }}>VBR isn't available for AV1. Pick HEVC or H.264 to use it.</span>}</span></label>
              {legacy && <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked onChange={() => undefined} />
                <span><strong>As set up before 13 Nov</strong><span className="hint" style={{ display: "block" }}>Makes the same files as before.</span></span></label>}
            </fieldset>
            {/* shortcut: the level and limit of an old flow show as fields, not as plain text; upgrade when design asks for the plain text */}
            <fieldset disabled={vbr} style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}>{field("maxBitrate")}{vbr && <span className="hint">Not used with VBR.</span>}</fieldset>
            {vbr && field("vbrMaxKbps")}
          </div>
        </details>
        <p className="hint">Every video not already in this format is converted, even if the new file isn't smaller, so your library ends up in one format. Jobs shows the size before and after.</p>
      </div>);
  }
  if (n.type === "output.commit") {
    const policy = (c.policy as string) ?? "separate_folder", mode = (c.folderMode as string) ?? "next_to_library";
    return (
      <div className="stack">
        <fieldset className="field choice stack"><legend className="lab">Save the result</legend>
          {labels(props.policy).map(([v, l]) => (
            <label key={String(v)} className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-policy"} checked={policy === v} onChange={() => set("policy", v)} />
              <span><strong>{l}</strong><span className="hint" style={{ display: "block" }}>{hintFor(props.policy, v)}</span>
                {v === "replace" && policy === "replace" && <span className="st warn" style={{ display: "flex", marginTop: 4 }}><Icon id="i-alert" /><span>Your original files will be replaced.</span></span>}</span>
            </label>))}
        </fieldset>
        {policy === "separate_folder" && (
          <fieldset className="field choice"><legend className="lab">{(props.folderMode.title as string) ?? "Where"}</legend><div className="row">
            {labels(props.folderMode).map(([v, l]) => <label key={String(v)} className="radio"><input type="radio" name={n.id + "-mode"} checked={mode === v} onChange={() => set("folderMode", v)} /><span>{l}</span></label>)}
          </div><Hint text={hintFor(props.folderMode, mode)} />{problem("/folderMode")}
          {mode === "next_to_library" && libs.length > 0 && <ul className="mono sm" style={{ listStyle: "none", margin: "6px 0 0", padding: 0 }} aria-label="Where each library's new files go">
            {libs.map((p) => <li key={p} style={{ overflowWrap: "anywhere" }}>{p} → {shrunk(p)}</li>)}</ul>}</fieldset>)}
        {policy === "separate_folder" && mode === "one_folder" && <>{field("folder")}<div className="row"><Browse value={String(c.folder ?? "")} onPick={(p) => set("folder", p)} /></div>{problem("/folder")}</>}
      </div>);
  }
  return (
    <div className="stack">
      {keys.map(field)}
      {n.type === "video.convert" && <>
        <p className="hint">Every video not already in this format is converted, even if the new file isn't smaller, so your library ends up in one format. Jobs shows the size before and after.</p></>}
      {more.length > 0 && <details ref={moreRef}><summary className="btn sm" style={{ display: "inline-flex" }}>More options</summary><div className="grid cols2" style={{ marginTop: 12 }}>{more.map(field)}</div></details>}
    </div>);
}

// One row of the step list (clean pass W1): number, icon, name and summary, the switch or "Always on", the chevron.
// Its options open under the row; on phones as a full-screen sheet with a back button (M5).
function Step({ n, i, open, toggle, update, issues }: { n: Node; i: number; open: boolean; toggle: () => void; update: (n: Node) => void; issues: ValidationIssue[] }) {
  const { stepDef, summary } = useReg(), def = stepDef(n.type), mine = issues.filter((x) => x.nodeId === n.id);
  const back = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (open && matchMedia("(max-width:640px)").matches) back.current?.focus(); }, [open]);
  // A read-only step (an earlier install's settings) or one this page doesn't know (a newer server, a plugin not
  // installed): shown with its type, its settings kept as they are, never edited (review P2).
  if (!def || def.readOnly) return (
    <li className="srow ro" aria-labelledby={n.id + "-name"}><div className="sline"><span className="stepn">{i + 1}</span>
      <span className="sic"><Icon id={(def && ICON[def.icon]) || "i-box"} /></span>
      <div className="sp"><span className="snm" id={n.id + "-name"}>{n.name ?? def?.name ?? "Unknown step"}</span> <span className="ssum">{(def && summary(n)) || "Type " + n.type}</span>
        <div className="sm muted">Its settings are kept as they are. {def ? def.description : "This version of the page doesn't know this step, so it only shows it."}</div></div>
      <span className="muted sm">Read-only</span></div></li>);
  const hasOptions = Object.values((def.configSchema.properties ?? {}) as Props).some((s) => !s.readOnly);
  return (
    <li className={"srow" + (open ? " open" : "")} aria-labelledby={n.id + "-name"}>
      <div className="sline">
        <span className="stepn">{i + 1}</span><span className="sic"><Icon id={ICON[def.icon] ?? "i-box"} /></span>
        <div className="sp" title={(n.name ?? def.name) + ": " + summary(n)}><span className="snm" id={n.id + "-name"}>{n.name ?? def.name}</span> <span className="ssum">{summary(n)}</span></div>
        {mine.length > 0 && <span className="pill bad"><Icon id="i-alert" />{mine.length === 1 ? "1 problem" : mine.length + " problems"}</span>}
        {def.alwaysOn ? <span className="muted sm nowrap">Always on</span>
          : <input type="checkbox" role="switch" className="switch" aria-label={(def.name) + " on"} checked={n.enabled} onChange={(e) => update({ ...n, enabled: e.target.checked })} />}
        {hasOptions ? <button className="btn icon ghost" type="button" aria-expanded={open} aria-controls={n.id + "-opts"} aria-label={"Options for " + def.name} onClick={toggle}><Icon id={open ? "i-chev-d" : "i-chev-r"} /></button>
          : <span className="chev-sp" />}
      </div>
      {open && hasOptions && <div className="sopts" id={n.id + "-opts"} role="group" aria-label={"Options for " + def.name} onKeyDown={(e) => e.key === "Escape" && toggle()}>
        <div className="sheethd phone"><button ref={back} className="btn icon ghost" type="button" aria-label="Back to the steps" onClick={toggle}><Icon id="i-chev-l" /></button><strong>{def.name}</strong></div>
        <Options n={n} issues={issues} set={(k, v) => update({ ...n, config: { ...n.config, [k]: v } })} /></div>}
    </li>);
}

// A workflow with branches: the steps in order with the branches named, nothing editable (spec 5.7; SCREENS V3).
function ReadOnly({ doc, id }: { doc: WorkflowDoc; id: string }) {
  const { stepDef, summary } = useReg();
  const name = (id: string) => { const n = doc.nodes.find((x) => x.id === id); return n ? (n.name ?? stepDef(n.type)?.name ?? n.type) + " (" + summary(n) + ")" : id; };
  return (
    <div className="stack">
      <div className="note"><Icon id="i-info" /><div>This workflow has conditions that the list can't show. <Link to={"/workflows/" + id + "/graph"}>Open graph</Link> to see them.</div></div>
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

async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}

function StackPage({ id }: { id: string }) {
  const meta = useData(() => getWorkflow(id), [id]), defs = useData(stepDefinitions, []), draft = useData(() => getDraft(id), [id]);
  const problem = meta.error ?? defs.error ?? draft.error;
  if (meta.error?.code === "not_found" || (meta.loaded && !meta.data)) return <div className="card"><div className="bd">There is no such workflow. It may have been deleted.</div></div>;
  if (!meta.data || !defs.data || !draft.data) return <div className="empty">{problem?.message ?? "Loading..."}</div>;
  return <Editor id={id} w={meta.data} defs={defs.data} first={draft.data} reloadMeta={meta.reload} />;
}

function Editor({ id, w, defs, first, reloadMeta }: { id: string; w: Workflow; defs: StepDefinition[]; first: WorkflowRevision; reloadMeta: () => void }) {
  const [etag, setEtag] = useState(first.etag), layout = first.layout;
  const [hist, setHist] = useState<{ docs: WorkflowDoc[]; at: number }>({ docs: [first.workflow], at: 0 });
  const [saved, setSaved] = useState(first.workflow);
  const [live, setLive] = useState<Checked>({ issues: first.validation, stackable: first.stackable, stepSummaries: first.stepSummaries });
  // ?step=<node id>: that step starts open (the library's Output tab links here)
  const [open, setOpen] = useState<string | null>(() => new URLSearchParams(location.search).get("step")), [said, setSaid] = useState(""), [checked, setChecked] = useState(false);
  const [panel, setPanel] = useState<PublishPreview | null>(null), [renaming, setRenaming] = useState(false);
  const doc = hist.docs[hist.at], dirty = doc !== saved;
  const all = useData(listLibraries, []), libs = (all.data ?? []).filter((l) => l.flowId === id).map((l) => l.path);
  // The server's words and problems for the document as it is now (nothing saved): asked 300 ms after a change.
  const ask = async (d: WorkflowDoc) => { const r = await act(validateDoc(id, d), setSaid); if (r) setLive(r); return r; };
  const seen = useRef(first.workflow);
  useEffect(() => {
    if (doc === seen.current) return;
    seen.current = doc;
    const t = setTimeout(() => ask(doc), 300);
    return () => clearTimeout(t);
  }, [doc]); // eslint-disable-line react-hooks/exhaustive-deps
  // Leaving with unsaved changes asks, as Appearance does: the router can't stop a move, so it offers to save first.
  const now = useRef({ dirty, doc, etag }); now.current = { dirty, doc, etag };
  useEffect(() => {
    onLeave(() => {
      const { dirty, doc, etag } = now.current;
      if (dirty && doc && window.confirm("Save your changes to this draft before leaving?\n\nOK saves them. Cancel leaves them unsaved.")) void saveDraft(id, doc, layout, etag).catch(() => {});
    });
    return () => onLeave(null);
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { // closing or reloading the tab with unsaved changes: the browser's own question (review P3)
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  const edit = (d: WorkflowDoc) => (setHist({ docs: [...hist.docs.slice(0, hist.at + 1), d], at: hist.at + 1 }), setChecked(false), setPanel(null), setSaid(""));
  const update = (n: Node) => edit({ ...doc, nodes: doc.nodes.map((x) => (x.id === n.id ? n : x)) });
  const save = async () => {
    const r = await act(saveDraft(id, doc, layout, etag), setSaid);
    if (!r) return null;
    setEtag(r.etag); setSaved(doc); setLive({ issues: r.validation, stackable: r.stackable, stepSummaries: r.stepSummaries }); setSaid("Draft saved."); reloadMeta();
    return r;
  };
  const openPublish = async () => { if (dirty && !(await save())) return; const p = await act(publishPreview(id), setSaid); if (p) setPanel(p); };
  const doPublish = async () => {
    const r = await act(publish(id, etag), setSaid);
    if (!r) return;
    const d = await act(getDraft(id), setSaid);
    if (d) setEtag(d.etag);
    setSaved(doc); setPanel(null); setSaid("Published v" + r.number + "."); reloadMeta();
  };
  const rename = async (v: string) => { if (await act(patchWorkflow(id, { name: v }), setSaid)) reloadMeta(); setRenaming(false); };
  const issues = checked || !dirty ? live.issues : [];
  const tools = <>
    <button className="btn sm icon ghost" type="button" title="Undo" aria-label="Undo" disabled={hist.at === 0} onClick={() => setHist({ ...hist, at: hist.at - 1 })}><Icon id="i-undo" /></button>
    <button className="btn sm icon ghost" type="button" title="Redo" aria-label="Redo" disabled={hist.at === hist.docs.length - 1} onClick={() => setHist({ ...hist, at: hist.at + 1 })}><Icon id="i-redo" /></button>
    <button className="btn sm ghost" type="button" onClick={async () => { if (await ask(doc)) setChecked(true); }}><Icon id="i-checks" />Check</button>
    <button className="btn sm" type="button" disabled={!dirty} onClick={save}><Icon id="i-save" />Save draft</button>
  </>;
  const canStack = live.stackable;
  return (
    <Reg.Provider value={{ defs, words: live.stepSummaries, libs }}>
    <div className="stack">
      {/* the workflow's header row (clean pass W2) */}
      <PageHead icon="i-workflow" crumbs={[["Workflows", "/workflows"]]}
        title={renaming ? <form className="row" onSubmit={(e) => { e.preventDefault(); const v = (e.currentTarget.elements.namedItem("nm") as HTMLInputElement).value.trim(); if (v) rename(v); else setRenaming(false); }}>
            <label className="sr" htmlFor="wf-rename">Name</label><input id="wf-rename" name="nm" className="input" defaultValue={w.name} autoFocus style={{ width: "auto" }} />
            <button className="btn sm" type="submit">Rename</button><button className="btn sm ghost" type="button" onClick={() => setRenaming(false)}>Cancel</button></form> : w.name}
        after={<>
          {!renaming && <button className="btn sm ghost" type="button" onClick={() => setRenaming(true)} aria-label={"Rename " + w.name}>Rename</button>}
          <span className="pill">{w.version == null || w.draftDiffers || dirty ? "Draft" : "Published"}</span>
          <span className="muted sm">{w.version == null ? "Not published yet" : "v" + w.version + " published" + (w.draftDiffers || dirty ? " · v" + (w.version + 1) + " draft" : "")}</span>
        </>}>
        <Link className="btn sm ghost" to={"/workflows/" + id + "/graph"}>Graph</Link>
        {canStack && <>
          <span className="desk">{tools}</span>
          <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{tools}</div></details>
          <button className="btn sm primary" type="button" onClick={openPublish}>Publish</button>
        </>}
      </PageHead>
      {canStack && (checked || !dirty) && (issues.length === 0 ? <p className="st ok checkline"><Icon id="i-check-c" /><span>Check: no problems</span></p>
        : <p className="st bad checkline"><Icon id="i-alert" /><span>Check: {issues.length === 1 ? "1 problem" : issues.length + " problems"}. Publish waits until {issues.length === 1 ? "it's" : "they're"} fixed. <button className="linkbtn" type="button" onClick={() => setOpen(issues[0].nodeId ?? null)}>Show</button></span></p>)}
      <p className="muted" role="status">{said}</p>

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
            <button className="btn primary" type="button" disabled={(panel.validation ?? []).some((v) => v.severity === "error")} onClick={doPublish}>Publish v{panel.nextNumber}</button>
            <button className="btn ghost" type="button" onClick={() => setPanel(null)}>Cancel</button></div>
        </div></section>)}

      {canStack ? <ol className="steps">{order(doc).map((n, i) => (
        <Step key={n.id} n={n} i={i} open={open === n.id} toggle={() => setOpen(open === n.id ? null : n.id)} update={update} issues={issues} />))}</ol>
        : <ReadOnly doc={doc} id={id} />}
    </div>
    </Reg.Provider>);
}
