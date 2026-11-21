// V3b Stack view (spec 5.7; DESIGN/v2 SCREENS V3, mockups/workflow-stack.html and -readonly.html) on the real /api/v2
// (#109: step definitions, the draft, validate, publish), or the mock with ?mock=1 (mock/steps.ts mirrors V1). The step
// words and problems come from the server's validate, asked again shortly after each change. Not here: drag to reorder
// (W11's handle, behind a flag) and the graph's keyboard moves, Test run (V5), History, Export, Open graph (V4).
import { PageHead } from "../PageHead";
import { Component, Fragment, createContext, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode, type RefObject } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed, type Answer } from "../api";
import { deleteNode, groupOf, insertAfter, insertQuestion, isQuestion, moveNode, newNode, order, otherwiseOf, setOtherwise, type Otherwise } from "../stepEdits"; // the steps in running order: a property of the document, not data
import { useData } from "../useData";
import { getDraft, getLadder, getWorkflow, listTemplates, listWorkflows, patchWorkflow, publish, publishPreview, saveDraft, stepDefinitions, validateDoc, type Checked, type Ladder } from "../workflowApi";
import { Link, navigate, onLeave } from "../router";
import { Browse } from "../FolderPicker";
import { OwnVars, VarInput, builtinsOf, takesVars, varListFor, varProblems, withOwn } from "../VarInput";
import "../styles/variables.css";
import { getSettings, listLibraries, shrunk } from "../libraryApi";
import type { TemplateVar } from "../mock/variables";
import type { AddStep, ID, JSONSchema, Node, PublishPreview, StepDefinition, ValidationIssue, Variable, Workflow, WorkflowDoc, WorkflowRevision } from "../types/v2";

// The step definitions and the server's words for each step of the document being edited.
// libs: the folders of the libraries that use this workflow, for "Next to each library" (V5a).
// wf: the workflow being edited, for its variable list (the {{name}} fields).
export const Reg = createContext<{ defs: StepDefinition[]; words: Record<string, string>; libs: string[]; wf: string }>({ defs: [], words: {}, libs: [], wf: "" });
function useReg() { const r = useContext(Reg); return { stepDef: (t: string) => r.defs.find((d) => d.type === t), summary: (n: Node) => r.words[n.id] ?? "", libs: r.libs, wf: r.wf }; }

// The server's icon keys (registry) -> design's sprite.
export const ICON: Record<string, string> = { folder: "i-folder", filter: "i-shield", video: "i-video", audio: "i-audio", subtitles: "i-subtitle", file: "i-file", check: "i-checks", save: "i-save", cond: "i-cond", history: "i-history", branch: "i-branch", stop: "i-stop", variable: "i-tag", note: "i-text", tell: "i-link", bell: "i-info", shield: "i-shield", copy: "i-copy", terminal: "i-terminal", tag: "i-tag", clock: "i-clock", redo: "i-redo", workflow: "i-workflow" }; // branch, stop: V6a check + end; variable, note: FB1d cards; tell, bell, shield, copy: FB2a after cards; workflow: FB4b Hand to another workflow
// The words around each option come from the server's step definitions (#114: title, description, x-enumHints for
// each choice, x-placeholder); the page adds none of its own.
const MORE: Record<string, string[]> = { "video.convert": ["format", "resolution", "maxBitrate", "speed"] };

type Props = Record<string, JSONSchema>;
const labels = (s: JSONSchema) => ((s.enum as unknown[]) ?? []).map((v, i) => [v, ((s["x-enumLabels"] as string[]) ?? [])[i] ?? String(v)] as [unknown, string]);
const widget = (s: JSONSchema) => ((s["x-ui"] as { widget?: string } | undefined)?.widget ?? "");
// The hint for the chosen value (x-enumHints follows enum's order).
const hintFor = (s: JSONSchema, v: unknown) => { const e = (s.enum as unknown[]) ?? [], h = (s["x-enumHints"] as string[]) ?? []; return h[e.indexOf(v)] ?? ""; };
const Hint = ({ text }: { text: string }) => (text ? <span className="hint" aria-live="polite">{text}</span> : null);

// User bug 11-20 (row cd65bf29): "Save as separate .srt files" says which languages become files; the others stay inside.
function SrtFiles() {
  const st = useData(getSettings, []), [list, setList] = useState<[string, string][]>([]);
  useEffect(() => { fetch("/languages.json").then((r) => r.json()).then(setList, () => {}); }, []);
  const codes = st.data && !failed(st.data) ? st.data.languages ?? [] : null;
  if (!codes) return null;
  const name = (c: string) => list.find((l) => l[0] === c)?.[1] ?? c, c0 = codes[0] ?? "<language>";
  return (<span className="hint" aria-live="polite">
    {codes.length ? "Saved as files: " + codes.map(name).join(", ") + ", and subtitles with no language set." : "No languages chosen, so subtitles in the main audio's language, and those with no language set, are saved as files."} <Link to="/settings/general">Change your languages</Link>
    <span style={{ display: "block" }}>For example: Movie.{c0}.srt, plus Movie.{c0}.sdh.srt or Movie.{c0}.forced.srt if the video has them.</span></span>);
}

// One option from the step's configSchema, as a native labelled control (design review rule 1).
// W1 (#521): a typed setting marked x-variableRef may hold one whole {{name}} instead of its own value. The field then
// shows a variable picker in place of its control, with a way back to typing a value; the server's words for a name
// that doesn't fit (variable_wrong_type) or text around it (variable_ref_not_whole) come back on the field like any
// other problem. The flow builder owns this screen (manager, 02:10Z).
const isRef = (v: unknown) => typeof v === "string" && /^\{\{[a-z][a-z0-9_]*\}\}$/.test(v);
const refName = (v: unknown) => (isRef(v) ? (v as string).slice(2, -2) : "");
function RefPick({ fid, title, value, set, back, selectRef, none }: { fid: string; title: string; value: unknown; set: (v: unknown) => void; back: () => void; selectRef: RefObject<HTMLSelectElement | null>; none: unknown }) {
  // only the workflow's own variables can set a typed setting (W1's resolver, internal/workflow/resolve.go); the
  // built-ins fill text fields only (ui-shop review2 #559). A secret never sets one, so it is listed but can't be chosen.
  const own = useContext(OwnVars), cur = refName(value);
  const vars = own.map((v) => ({ name: v.name, value: v.type === "secret" ? null : v.hasValue === false || v.value == null ? null : String(v.value), secret: v.type === "secret" }));
  const known = vars.some((v) => v.name === cur);
  return (
    <div className="field">
      <label htmlFor={fid}>{title}</label>
      <div className="row nw" style={{ gap: 8 }}>
        <select ref={selectRef} id={fid} className="input" value={cur} aria-describedby={fid + "-ref"} onChange={(e) => { const next = e.target.value ? "{{" + e.target.value + "}}" : none; if (next !== value) set(next); }}>
          <option value="">Choose a variable…</option>
          {cur && !known && <option value={cur}>{"{{" + cur + "}}"} · not a variable of this workflow</option>}
          <optgroup label="Your variables">{vars.map((v) => <option key={v.name} value={v.name} disabled={v.secret}>{"{{" + v.name + "}}"}{v.secret ? " · a secret can't set this" : v.value != null && v.value !== "" ? " · " + v.value : ""}</option>)}</optgroup>
        </select>
        <button className="btn sm ghost nowrap" type="button" onClick={back}>Type a value instead</button>
      </div>
      <span className="hint" id={fid + "-ref"}>{vars.length === 0 ? "This workflow has no variables yet: add one in Variables above, then choose it here." : "One of this workflow's own variables sets this; its value must fit the setting. A secret can't be used here."}</span>
    </div>);
}

function Field(props: { id: string; name: string; s: JSONSchema; value: unknown; set: (v: unknown) => void; wf: string }) {
  const { id, name, s, value, set } = props;
  const title = (s.title as string) ?? name.charAt(0).toUpperCase() + name.slice(1).replace(/([A-Z])/g, " $1").toLowerCase(), fid = id + "-" + name;
  const [refMode, setRefMode] = useState(false), typed = s["x-variableRef"] === true;
  // ui-shop review #559: the typed value is kept across a round trip (F1), nothing is written when nothing changes (F2),
  // and focus follows the mode switch (F3): the picker after "Use a variable", the control (or the link) after "Type a value instead"
  const typedValue = useRef<unknown>(undefined), pick = useRef<HTMLSelectElement>(null), link = useRef<HTMLButtonElement>(null);
  const [focusTyped, setFocusTyped] = useState(0);
  useEffect(() => { if (refMode) pick.current?.focus(); }, [refMode]);
  useEffect(() => { if (focusTyped) (document.getElementById(fid) as HTMLElement | null ?? link.current)?.focus(); }, [focusTyped, fid]);
  const inRef = typed && (refMode || isRef(value));
  // "Choose a variable…" picked again puts the typed value back (never "" into a number or a choice: ui-shop N1)
  const none = typedValue.current !== undefined ? typedValue.current : s.default;
  if (inRef) return <RefPick fid={fid} title={title} value={value} set={set} selectRef={pick} none={none} back={() => {
    const restore = typedValue.current !== undefined ? typedValue.current : s.default;
    setRefMode(false); setFocusTyped((n) => n + 1);
    if (isRef(value)) { if (restore !== value) set(restore); } // a plain value already there is left as it is
  }} />;
  return (
    <>
      <Control {...props} />
      {typed && <button ref={link} className="linkbtn sm" type="button" onClick={() => { typedValue.current = value; setRefMode(true); }} aria-label={"Use a variable for " + title}>Use a variable</button>}
    </>);
}

// WorkflowPick lists the published workflows other than this one, by name, for "Hand to another workflow" (FB4b); the
// server's validation says when the chosen one is this workflow, a draft, gone, or leads back here (handoff_loop).
function WorkflowPick({ fid, title, value, set, wf, description }: { fid: string; title: string; value: string; set: (v: unknown) => void; wf: string; description?: string }) {
  const all = useData(listWorkflows, []), list = (all.data && !failed(all.data) ? all.data : []).filter((w) => w.id !== wf && w.status !== "draft");
  const known = list.some((w) => w.id === value);
  return (
    <div className="field"><label htmlFor={fid}>{title}</label>
      <select id={fid} className="input" value={value} onChange={(e) => set(e.target.value)} aria-describedby={fid + "-h"}>
        <option value="">Choose a workflow…</option>
        {value && !known && <option value={value}>{value === wf ? "This workflow" : all.data && !failed(all.data) ? "A workflow that isn't published, or isn't there" : "Workflow " + value}</option>}
        {list.map((w) => <option key={w.id} value={w.id}>{w.name}</option>)}
      </select>
      <span className="hint" id={fid + "-h"}>{list.length === 0 ? "No other workflow is published yet: publish one first, then choose it here." : description ?? ""}</span>
    </div>);
}

function Control({ id, name, s, value, set, wf }: { id: string; name: string; s: JSONSchema; value: unknown; set: (v: unknown) => void; wf: string }) {
  // The schema's title; without one (V1's "ignore" has none), the key in plain form.
  const title = (s.title as string) ?? name.charAt(0).toUpperCase() + name.slice(1).replace(/([A-Z])/g, " $1").toLowerCase(), fid = id + "-" + name;
  if (widget(s) === "slider") {
    const opts = labels(s), i = Math.max(0, opts.findIndex((o) => o[0] === value));
    return (
      <div className="field"><label htmlFor={fid}>{title}</label>
        <strong className="slnow phone" aria-hidden="true">{opts[i][1]}</strong>
        <input id={fid} type="range" min={0} max={opts.length - 1} step={1} value={i} aria-valuetext={`${i + 1} of ${opts.length}, ${opts[i][1]}`} onChange={(e) => set(opts[Number(e.target.value)][0])} />
        <div className="row sm muted hide-sm" style={{ justifyContent: "space-between", flexWrap: "nowrap", gap: 2 }} aria-hidden="true">
          {opts.map((o, k) => (k === i ? <strong key={k} className="slider-on">{o[1]}</strong> : <span key={k}>{o[1]}</span>))}</div>
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
      </div><Hint text={hintFor(s, value)} />{name === "subtitles" && value === "srt" && <SrtFiles />}{typeof s.description === "string" && <span className="hint">{s.description}</span>}</fieldset>);
  }
  if (s.enum)
    return <div className="field"><label htmlFor={fid}>{title}</label><select id={fid} className="input" value={String(value)} onChange={(e) => set(labels(s).find((o) => String(o[0]) === e.target.value)?.[0])}>
      {labels(s).map(([v, l]) => <option key={String(v)} value={String(v)}>{l}</option>)}</select><Hint text={hintFor(s, value)} />{name === "subtitles" && value === "srt" && <SrtFiles />}{typeof s.description === "string" && <span className="hint">{s.description}</span>}</div>;
  // a list of choices (V6a check: "Video format is one of"): a checkbox per choice
  const items = s.items as JSONSchema | undefined;
  if (s.type === "array" && items?.enum) {
    const known = labels(items).map((o) => o[0]), on = ((value as unknown[]) ?? []).filter((x) => known.includes(x)); // a change drops values it doesn't know (#165: so a bad item can be fixed)
    return (
      <fieldset className="field choice"><legend className="lab">{title}</legend><div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
        {labels(items).map(([v, l]) => <label key={String(v)} className="row nw"><input type="checkbox" checked={on.includes(v)} onChange={(e) => set(e.target.checked ? [...on, v] : on.filter((x) => x !== v))} /><span>{l}</span></label>)}
      </div>{typeof s.description === "string" && <span className="hint">{s.description}</span>}</fieldset>);
  }
  // FB4b: "Hand to another workflow" chooses one of the other published workflows (never this one, never a draft)
  if (widget(s) === "workflow") return <WorkflowPick fid={fid} title={title} value={String(value ?? "")} set={set} wf={wf} description={typeof s.description === "string" ? s.description : undefined} />;
  // a text field that takes {{name}} variables (x-variables): the hint, the example and the picker
  if (s["x-variables"] === true) return <VarInput id={fid} title={title} value={String(value ?? "")} wf={wf} set={(v) => set(v)} description={typeof s.description === "string" ? s.description : undefined} />;
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

export function Options({ n, set, issues }: { n: Node; set: (k: string, v: unknown, more?: Record<string, unknown>) => void; issues: ValidationIssue[] }) {
  const { stepDef, libs, wf } = useReg(), def = stepDef(n.type)!, props = Object.fromEntries(Object.entries((def.configSchema.properties ?? {}) as Props).filter(([, s]) => !s.readOnly)) as Props, c = n.config;
  const lad = useData(getLadder, []).data, conv = (k: string) => (n.type === "video.convert" ? convertField(k, props[k], c, lad) : props[k]);
  // FB2b: a step's required settings come first, in the order the schema requires them (the server's object keys are alphabetical)
  const req = (def.configSchema.required as string[] | undefined) ?? [], rank = (k: string) => (req.includes(k) ? req.indexOf(k) : req.length);
  const keys = Object.keys(props).filter((k) => !(MORE[n.type] ?? []).includes(k)).sort((a, b) => rank(a) - rank(b)), more = (MORE[n.type] ?? []).filter((k) => props[k] && conv(k));
  // #165: a setting's own problems (invalid_config, keyed on the step + field) show at that field, which is marked
  const bad = (k: string) => issues.filter((i) => i.nodeId === n.id && (i.field === k || i.configPath === "/" + k) && i.code !== "unconnected_port");
  // one edit writes its switches in the same update (A-API-SHAPE, engine #211): an old flow takes the new meaning on any
  // edit (the slider's quality, or level in custom settings); AV1 takes Quality only, with a line saying so once
  const [said, setSaid] = useState("");
  const [ask, setAsk] = useState(false); // "Go back to the slider?" (SCREENS V3)
  const put = (k: string, v: unknown) => {
    const x: Record<string, unknown> = {}, rc = String(c.rateControl ?? "quality");
    if (n.type === "video.convert") {
      // decide from the format this edit leaves (not the saved one): AV1 takes Quality only (engine #211 refuses the rest)
      const fmt = k === "format" ? String(v) : String(c.format ?? "hevc");
      if (rc.endsWith("_legacy") && k !== "rateControl") x.rateControl = fmt === "av1" || k === "quality" ? "quality" : "level";
      if (k !== "quality" && k !== "kind" && c.kind !== "selectors") x.kind = "selectors"; // a custom edit turns the slider off
      const next = String(x.rateControl ?? rc);
      if (fmt === "av1" && next !== "quality") { x.rateControl = "quality"; setSaid(k === "format" ? "Switched to Quality only: AV1 can't use a bitrate. Your bitrate settings are kept if you switch back." : ""); }
      else if (k === "format") setSaid("");
    }
    set(k, v, x);
  };
  const field = (k: string) => { const b = bad(k), sk = conv(k); if (!sk) return null; return (
    <div key={k} className={b.length ? "fieldbad" : undefined} data-field={k} aria-invalid={b.length ? true : undefined}>
      <Field id={n.id} name={k} s={sk} value={c[k] ?? sk.default} set={(v) => put(k, v)} wf={wf} />
      {b.map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
    </div>); };
  // a problem in a field under 'More options' opens it by itself (review c0189fa P3), also when the problems list picks
  // this step; it isn't closed again when the last one is fixed, so the field doesn't vanish while it's being edited
  const moreRef = useRef<HTMLDetailsElement>(null), moreBad = more.some((k) => bad(k).length > 0);
  // an open the page makes itself is not the person's choice: it must not switch the step to custom settings (W1 screen:
  // a problem on a More-options field opened it, the toggle wrote kind=selectors, the draft went dirty and the live problems hid)
  const byPage = useRef(false);
  useEffect(() => { if (moreBad && moreRef.current && !moreRef.current.open) { byPage.current = true; moreRef.current.open = true; } }, [moreBad, n.id]);
  const problem = (path: string) => issues.filter((i) => i.nodeId === n.id && i.configPath === path).map((i) => <span key={i.code} className="hint" style={{ color: "var(--error-text)" }}>{i.message}</span>);
  if (n.type === "video.convert") {
    // A-API-SHAPE + engine #211 + SCREENS V3 'Convert video, revised 11-13' (01:05Z): the slider (rateControl quality), or
    // custom settings (kind selectors) with three rate controls: Bitrate level, VBR with a max, Quality only. AV1 takes
    // Quality only; old flows show their meaning until an edit.
    const sel = c.kind === "selectors", rc = String(c.rateControl ?? "quality"), av1 = String(c.format ?? "hevc") === "av1";
    const legacy = rc.endsWith("_legacy"), vbr = rc === "vbr", q = rc === "quality";
    const sl = lad?.slider.find((x) => x.position === Number(c.quality ?? 3) && x.format === String(c.format ?? "hevc"));
    // Opening More options is the switch to custom: kind and rateControl in one update (AV1 gets Quality only)
    // an old flow keeps its meaning until its first edit (design: 'Changing its bitrate settings switches it')
    const custom = () => !legacy && set("kind", "selectors", { rateControl: av1 ? "quality" : "level" });
    // the slider's own values: custom settings that differ from them are asked about before they're cleared (SCREENS V3)
    const SLIDER = { format: "hevc", resolution: "keep", maxBitrate: "none", speed: "normal", rateControl: "quality" };
    const differs = Object.entries(SLIDER).some(([k, v]) => String(c[k] ?? v) !== v) || c.vbrMaxKbps != null;
    const backToSlider = () => { setAsk(false); set("kind", "slider", { ...SLIDER, vbrMaxKbps: undefined }); };
    const keep = () => { setAsk(false); if (moreRef.current) moreRef.current.open = true; };
    const goBack = () => (differs ? setAsk(true) : backToSlider());
    return (
      <div className="stack">
        {legacy && <div className="note"><Icon id="i-info" /><div>This step was set up before 13 Nov and still makes the same files as before. Changing its bitrate settings switches it to the new rules.</div></div>}
        {!sel && <>
          {field("quality")}
          {sl && <p className="sm">{((labels(props.format).find((f) => f[0] === sl.format)?.[1] ?? sl.format) as string).split(" (")[0] + " · quality " + sl.crf + " (CRF) · " + (sl.keepsResolution ? "keeps the resolution" : "lowers the resolution") + " · " + (sl.capKbps ? "capped at " + sl.capKbps.toLocaleString() + " kbps" : "no bitrate cap") + " · usually about " + sl.saveLowPct + "–" + sl.saveHighPct + "% smaller"}</p>}
          <p className="hint">Lower quality numbers mean a better picture and bigger files. The number is for 1080p; 720p and smaller files use one lower, 4K one higher. Every file's picture is checked after converting, and the original stays if it doesn't pass.</p>
        </>}
        {sel && <p className="note"><Icon id="i-info" /><span>Using your own settings. The slider is off. <button className="linkbtn" type="button" onClick={goBack}>Use the slider again</button></span></p>}
        {ask && <div className="note" role="alertdialog" aria-labelledby={n.id + "-go"} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), keep())}>
          <Icon id="i-alert" /><div className="sp"><strong id={n.id + "-go"}>Go back to the slider?</strong> Your custom settings will be cleared.</div>
          <button className="btn sm primary" type="button" onClick={backToSlider}>Clear and use the slider</button>
          <button className="btn sm ghost" type="button" autoFocus onClick={keep}>Keep my settings</button></div>}
        <details ref={moreRef} onToggle={(e) => { if (byPage.current) { byPage.current = false; return; } if (e.currentTarget.open) { if (!sel) custom(); } else if (sel) goBack(); }}><summary className="btn sm" style={{ display: "inline-flex" }}>More options</summary>
          <div className="grid cols2" style={{ marginTop: 12 }}>
            {field("format")}{field("resolution")}{field("speed")}
            {(q || vbr) && sel && <div className="stack">
              <fieldset disabled={vbr} aria-describedby={vbr ? n.id + "-vbrq" : undefined} style={{ border: 0, padding: 0, margin: 0, minWidth: 0, color: vbr ? "var(--text-2)" : undefined }}>{field("quality")}</fieldset>
              {vbr && <p className="hint" id={n.id + "-vbrq"}>VBR sizes the file by bitrate, so Quality isn't used.</p>}</div>}
            {av1 && sel && <p className="hint" role="status">AV1 can only use Quality only. Pick HEVC or H.264 to set a bitrate.</p>}
            <fieldset className="field choice stack"><legend className="lab">Bitrate</legend>
              <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked={rc === "level"} disabled={av1} onChange={() => put("rateControl", "level")} />
                <span><strong>Bitrate level</strong><span className="hint" style={{ display: "block" }}>Aims for the level's bitrate for each file's resolution.</span></span></label>
              <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked={rc === "vbr"} disabled={av1} onChange={() => put("rateControl", "vbr")} />
                <span><strong>VBR with a max</strong><span className="hint" style={{ display: "block" }}>Aims for a steady picture and uses only the bitrate each scene needs, never above your max.</span></span></label>
              <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked={rc === "quality"} onChange={() => put("rateControl", "quality")} />
                <span><strong>Quality only</strong><span className="hint" style={{ display: "block" }}>Uses the Quality setting above, with no bitrate limit. File sizes vary the most.</span></span></label>
              {legacy && <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked onChange={() => undefined} />
                <span><strong>As set up before 13 Nov</strong><span className="hint" style={{ display: "block" }}>Makes the same files as before.</span></span></label>}
            </fieldset>
            {/* shortcut: the level and limit of an old flow show as fields, not as plain text; upgrade when design asks for the plain text */}
            <fieldset disabled={vbr || q} aria-describedby={vbr ? n.id + "-mbv" : q ? n.id + "-mbq" : undefined} style={{ border: 0, padding: 0, margin: 0, minWidth: 0, color: vbr || q ? "var(--text-2)" : undefined }}>{field("maxBitrate")}
              {vbr && <span className="hint" id={n.id + "-mbv"}>Not used with VBR.</span>}{q && <span className="hint" id={n.id + "-mbq"}>Not used with Quality only.</span>}
              {!vbr && !q && lad && <p className="hint">Raised by {Math.round((lad.factors.fps60 - 1) * 100)}% for 60 fps files and by {Math.round((lad.factors.hdr - 1) * 100)}% for HDR. A file never gets more bitrate than it has now.</p>}</fieldset>
            {vbr && field("vbrMaxKbps")}
          </div>
        </details>
        {said && <p className="hint" role="status">{said}</p>}
        <p className="hint">Every video not already in this format is converted, even if the new file isn't smaller, so your library ends up in one format. Jobs shows the size before and after.</p>
      </div>);
  }
  if (n.type === "output.commit") {
    const policy = (c.policy as string) ?? "separate_folder", mode = (c.folderMode as string) ?? "next_to_library", bigger = (c.ifBigger as string) ?? "replace"; // an absent ifBigger reads as replace (preset.go)
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
        {props.ifBigger && <fieldset className="field choice"><legend className="lab">{(props.ifBigger.title as string) ?? "If bigger"}</legend><div className="row">
          {labels(props.ifBigger).map(([v, l]) => <label key={String(v)} className="radio"><input type="radio" name={n.id + "-bigger"} checked={bigger === v} onChange={() => set("ifBigger", v)} /><span>{l}</span></label>)}
        </div><Hint text={hintFor(props.ifBigger, bigger)} /></fieldset>}
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
// W11: a step the server marks fixed (or any step, when the server sends no places) can't move or be deleted: a lock,
// "Set place", and its More menu greyed out with the line below. The first and last steps have no menu.
const FIXED_LINE = "The steps that convert run in a set order for now, so they can't be moved or deleted. To skip this step, turn it off.";
type Move = { fixed: boolean; reason?: string; up: boolean; down: boolean; del: boolean; act: (w: "up" | "down" | "del") => void };
// FB1d: a question's row says what its No does: one of the ends, or another path the graph shows. FB4b: "hand to
// another workflow" is an end with a setting, so the row shows the workflow picker under it (node: that end).
type ElseKind = "leave" | "fail" | "goto" | "other";
type Else = { kind: Otherwise; set: (k: ElseKind) => void; node?: Node; update?: (n: Node) => void };
const OTHERWISE: [Otherwise, string][] = [["leave", "leave the file alone"], ["fail", "stop as failed"], ["goto", "hand to another workflow"], ["other", "another path (in the graph)"]];
function Step({ n, i, mv, open, toggle, update, issues, otherwise, inline }: { n: Node; i: number; mv: Move; open: boolean; toggle: () => void; update: (n: Node) => void; issues: ValidationIssue[]; otherwise?: Else; inline?: boolean }) {
  const { stepDef, summary, wf } = useReg(), def = stepDef(n.type), mine = issues.filter((x) => x.nodeId === n.id);
  if (inline) open = true; // FB3, the simple view: every form is on the page, nothing folds
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
  const end = n.type === "input.library" ? "first" : n.type === "output.commit" ? "last" : undefined, nm = n.name ?? def.name;
  return (
    <li className={"srow" + (open ? " open" : "")} aria-labelledby={n.id + "-name"}>
      <div className="sline">
        <span className="stepn">{i + 1}</span>{!end && mv.fixed && <span className="lock" aria-hidden="true"><Icon id="i-lock" /></span>}<span className="sic"><Icon id={ICON[def.icon] ?? "i-box"} /></span>
        <div className="sp" title={(n.name ?? def.name) + ": " + summary(n)}><span className="snm" id={n.id + "-name"}>{n.name ?? def.name}</span> <span className="ssum">{summary(n)}</span></div>
        {mine.length > 0 && <span className="pill bad"><Icon id="i-alert" />{mine.length === 1 ? "1 problem" : mine.length + " problems"}</span>}
        {end ? <span className="muted sm nowrap">{end === "first" ? "Always first" : "Always last"}</span>
          : otherwise ? <label className="row nw otherwise"><span className="muted nowrap">Otherwise</span>
              <select className="input" aria-label={"Otherwise: what happens when " + nm + " answers No"} value={otherwise.kind === "none" ? "leave" : otherwise.kind}
                onChange={(e) => otherwise.set(e.target.value as ElseKind)}>
                {OTHERWISE.filter(([k]) => k !== "other" || otherwise.kind === "other").map(([k, l]) => <option key={k} value={k}>{l}</option>)}
                {otherwise.kind !== "other" && <option value="other">another path (open the graph)</option>}
              </select></label>
          : def.alwaysOn ? <span className="muted sm nowrap">Always on</span>
          : <>{mv.fixed && !inline && <span className="muted sm nowrap" title={mv.reason ?? FIXED_LINE}>Set place</span>}
            <input type="checkbox" role="switch" className="switch" aria-label={(def.name) + " on"} checked={n.enabled} onChange={(e) => update({ ...n, enabled: e.target.checked })} /></>}
        {!end && !inline && <details className="menu">
          <summary className="btn icon ghost" id={n.id + "-more"} aria-label={"More for " + nm}><Icon id="i-more" /></summary>
          <div className="pop" role="menu">
            <button className="it" type="button" role="menuitem" aria-disabled={mv.up ? undefined : true} onClick={(e) => { (e.currentTarget.closest("details") as HTMLDetailsElement).open = false; if (mv.up) mv.act("up"); }}>Move up</button>
            <button className="it" type="button" role="menuitem" aria-disabled={mv.down ? undefined : true} onClick={(e) => { (e.currentTarget.closest("details") as HTMLDetailsElement).open = false; if (mv.down) mv.act("down"); }}>Move down</button>
            <button className="it" type="button" role="menuitem" aria-disabled={mv.del ? undefined : true} onClick={(e) => { (e.currentTarget.closest("details") as HTMLDetailsElement).open = false; if (mv.del) mv.act("del"); }}>Delete</button>
            {mv.fixed && <><hr /><p className="hd" style={{ whiteSpace: "normal", maxWidth: 260 }}>{mv.reason ?? FIXED_LINE}</p></>}
          </div></details>}
        {hasOptions && !inline ? <button className="btn icon ghost" type="button" aria-expanded={open} aria-controls={n.id + "-opts"} aria-label={"Options for " + def.name} onClick={toggle}><Icon id={open ? "i-chev-d" : "i-chev-r"} /></button>
          : <span className="chev-sp" />}
      </div>
      {otherwise?.kind === "goto" && otherwise.node && otherwise.update && (() => { const g = otherwise.node, put = otherwise.update, bad = issues.filter((x) => x.nodeId === g.id); return (
        <div className="otherwise-goto" data-node={g.id}>
          <WorkflowPick fid={g.id + "-workflow"} title={"When " + nm + " answers No, hand the file to"} value={String(g.config.workflow ?? "")} set={(v) => put({ ...g, config: { ...g.config, workflow: v } })} wf={wf} description={stepDef("flow.goto")?.description} />
          {bad.map((x, k) => <span key={k} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{x.message}</span>)}
        </div>); })()}
      {open && hasOptions && <div className={"sopts" + (inline ? " inline" : "")} id={n.id + "-opts"} role="group" aria-label={"Options for " + def.name} onKeyDown={(e) => !inline && e.key === "Escape" && toggle()}>
        {!inline && <div className="sheethd phone"><button ref={back} className="btn icon ghost" type="button" aria-label="Back to the steps" onClick={toggle}><Icon id="i-chev-l" /></button><strong>{def.name}</strong></div>}
        <Options n={n} issues={issues} set={(k, v, more) => update({ ...n, config: { ...n.config, [k]: v, ...more } })} /></div>}
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

// W11, list side: the add-step menu. Every step type, grouped by kind. The server's addSteps says which can be added and
// where; with no map, everything is off except what the workflow already has, and conditions open the graph instead.
const CAT_VAR: Record<string, string> = { input: "input", filter: "condition", flow: "logic", video: "video", audio: "audio", subtitles: "subtitle", container: "files", verify: "inspect", output: "output" };
function AddStepMenu({ wf, doc, defs, adds, onAdd, onClose }: { wf: string; doc: WorkflowDoc; defs: StepDefinition[]; adds?: Record<string, AddStep>; onAdd: (d: StepDefinition, after?: ID) => void; onClose: () => void }) {
  const [q, setQ] = useState(""), [sel, setSel] = useState<string | null>(null);
  const have = new Set(doc.nodes.map((n) => n.type));
  const nameOf = (x?: ID) => { const n = doc.nodes.find((y) => y.id === x); return n ? (n.name ?? defs.find((d) => d.type === n.type)?.name ?? n.type) : "the step before"; };
  const rows = defs
    .filter((d) => !d.readOnly && !d.deprecated && (d.name + " " + d.description).toLowerCase().includes(q.trim().toLowerCase()))
    .map((d) => {
      const cond = d.outputs.some((o) => o.outcome === "branch"), a = adds?.[d.type], again = cond || d.category === "flow"; // a question or an action may appear more than once
      const on = cond ? !!a?.addable || !adds : (again || !have.has(d.type)) && !!a?.addable;
      const line = cond ? (a?.addable ? "Only if it answers Yes; otherwise the file is left alone. Goes after " + nameOf(a.after) : "Splits files by a rule. Opens the graph.")
        : have.has(d.type) && !again ? "Already in this workflow" : a?.addable ? "Goes after " + nameOf(a.after) : a?.reason || "Not available here yet.";
      return { d, g: groupOf(d), on, line, after: a?.after };
    })
    .sort((x, y) => x.g.index - y.g.index); // stable: the registry's order inside a group
  const on = rows.filter((r) => r.on).map((r) => r.d.type);
  const cur = sel && on.includes(sel) ? sel : on[0] ?? null;
  const pick = (r: (typeof rows)[number]) => { if (r.on) onAdd(r.d, r.after); };
  // on a phone the menu is a bottom sheet: Tab stays inside it, and Escape (from anywhere in it) closes it
  const sheet = useRef<HTMLElement>(null);
  const trap = (e: { key: string; shiftKey: boolean; preventDefault: () => void }) => {
    if (e.key === "Escape") return onClose();
    if (e.key !== "Tab" || !window.matchMedia("(max-width: 640px)").matches || !sheet.current) return;
    const f = [...sheet.current.querySelectorAll<HTMLElement>("input, button")].filter((x) => !(x as HTMLButtonElement).disabled), first = f[0], last = f[f.length - 1];
    if (!first) return;
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  };
  const key = (e: { key: string; preventDefault: () => void }) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (on.length) { const i = cur ? on.indexOf(cur) : -1; setSel(on[(i + (e.key === "ArrowDown" ? 1 : -1) + on.length) % on.length]); }
    }
    if (e.key === "Enter") { e.preventDefault(); const r = rows.find((x) => x.d.type === cur); if (r) pick(r); }
  };
  return (
    <section ref={sheet} className="card addsheet" aria-label="Add a step" onKeyDown={trap}><div className="bd stack">
      <label className="sr" htmlFor={wf + "-addq"}>Search steps</label>
      <input id={wf + "-addq"} className="input" type="text" role="combobox" aria-expanded="true" aria-controls={wf + "-addlist"} aria-autocomplete="list"
        aria-activedescendant={cur ? "add-" + cur : undefined} placeholder="Search steps" value={q} autoFocus
        onChange={(e) => { setQ(e.target.value); setSel(null); }} onKeyDown={key} />
      <ul id={wf + "-addlist"} role="listbox" aria-label="Steps you can add" style={{ listStyle: "none", margin: 0, padding: 0, maxHeight: 360, overflowY: "auto" }}>
        {rows.length === 0 && <li role="presentation" className="muted sm">No step matches “{q}”.</li>}
        {rows.map((r, k) => (
          <Fragment key={r.d.type}>
            {(k === 0 || rows[k - 1].g.title !== r.g.title) && <li role="presentation" className="addgrp">{r.g.title}</li>}
            <li role="option" id={"add-" + r.d.type} aria-selected={r.d.type === cur} aria-disabled={r.on ? undefined : true}
              className={"addrow" + (r.on ? "" : " off")} onClick={() => pick(r)} onMouseEnter={() => r.on && setSel(r.d.type)}>
              <span className="addsq" style={{ background: "var(--cat-" + (CAT_VAR[r.d.category] ?? "files") + ")" }} aria-hidden="true" />
              <span className="sp"><span className="snm">{r.d.name}</span> <span className="sm muted addline">{r.line}</span></span>
            </li>
          </Fragment>))}
      </ul>
      <div className="row" style={{ justifyContent: "flex-end" }}><button className="btn sm ghost" type="button" onClick={onClose}>Close</button></div>
    </div></section>);
}

async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}

function StackPage({ id }: { id: string }) {
  const meta = useData(() => getWorkflow(id), [id]), defs = useData(stepDefinitions, []), draft = useData(() => getDraft(id), [id]);
  const problem = meta.error ?? defs.error ?? draft.error;
  if (meta.error?.code === "not_found" || (meta.loaded && !meta.data)) return <div className="card"><div className="bd">There is no such workflow. It may have been deleted.</div></div>;
  if (!meta.data || !defs.data || !draft.data) return <div className="empty">{problem?.message ?? "Loading…"}</div>;
  return <Editor id={id} w={meta.data} defs={defs.data} first={draft.data} reloadMeta={meta.reload} />;
}

// The {{name}} references in a workflow's fields: name -> the steps that use it ("Used by", and the delete warning).
function usedBy(nodes: Node[], defs: StepDefinition[]) {
  const out = new Map<string, Node[]>();
  for (const n of nodes) {
    const props = (defs.find((d) => d.type === n.type)?.configSchema.properties ?? {}) as Record<string, { "x-variables"?: unknown }>;
    for (const [k, s] of Object.entries(props)) {
      if (s["x-variables"] !== true) continue;
      for (const m of String(n.config?.[k] ?? "").matchAll(/\{\{\s*([^{}]*?)\s*\}\}/g)) {
        const list = out.get(m[1]) ?? [];
        if (!list.includes(n)) list.push(n);
        out.set(m[1], list);
      }
    }
  }
  return out;
}

// The workflow's own variables: a table (name, value, where it comes from, the steps that use it), an add form, and a
// box for fields that use a name that isn't a variable. Changes go through edit(), so undo and Save draft cover them.
const NAME = /^[a-z][a-z0-9_]{0,39}$/;
// The server's codes for a bad variable name (W4a, #497): shown on the variable's row, and a refused save names them.
const VAR_CODES = ["invalid_variable_name", "builtin_variable_name", "duplicate_variable"];
const refusedWords = (is: ValidationIssue[]) => "Nothing was saved. Rename or remove " + [...new Set(is.map((i) => (i.name ? "{{" + i.name + "}}" : "the variable with no name")))].join(", ") + " in Variables, then save again.";

function VariablesCard({ doc, defs, builtins, problems, nameIssues, edit, open }: { doc: WorkflowDoc; defs: StepDefinition[]; builtins: TemplateVar[]; problems: { nodeId: string; title: string; code: string; name: string; error: string }[]; nameIssues: ValidationIssue[]; edit: (d: WorkflowDoc, run?: string) => void; open: (nodeId: string) => void }) {
  const [name, setName] = useState(""), [val, setVal] = useState(""), [what, setWhat] = useState(""), [err, setErr] = useState("");
  const [shown, setShown] = useState(false); // closed by default; opens by itself when a field uses an unknown name or a variable name is refused
  const [editing, setEditing] = useState<number | null>(null); // the row whose description is open for editing (by index: a saved doc can repeat a name)
  const vars = doc.variables ?? [], used = usedBy(doc.nodes, defs), titleOf = (n: Node) => defs.find((d) => d.type === n.type)?.name ?? n.type;
  const unknown = problems.filter((p) => p.code === "unknown_variable"), bad = unknown.length > 0;
  useEffect(() => { if (bad || nameIssues.length > 0) setShown(true); }, [bad, nameIssues.length]);
  const setVars = (list: Variable[], run?: string) => edit({ ...doc, variables: list }, run);
  const add = (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim(), b = builtins.find((v) => v.name === n);
    const msg = !NAME.test(n) ? "Use lowercase letters, numbers and _, starting with a letter."
      : b ? `{{${n}}} is already a built-in variable (${b.description.charAt(0).toLowerCase() + b.description.slice(1)}). Pick another name.`
      : vars.some((v) => v.name === n) ? `{{${n}}} already exists. Edit it instead.` : "";
    setErr(msg);
    if (msg) return;
    setVars([...vars, { name: n, type: "string", value: val === "" ? undefined : val, description: what.trim() || undefined }]);
    setName(""); setVal(""); setWhat("");
  };
  const remove = (n: string, at: number) => {
    const by = used.get(n) ?? [];
    if (by.length && !window.confirm(`${by.map(titleOf).join(" and ")} use {{${n}}}. Delete it anyway? Those fields will show an error until you change them.`)) return;
    setVars(vars.filter((_, j) => j !== at));
    setEditing(null); // indexes after the deleted row shift
  };
  // Used by, and the warning box: open the step's options and focus the field that uses the variable ({{ name }} counts too)
  const openField = (nodeId: string, name: string) => {
    open(nodeId);
    const uses = new RegExp("\\{\\{\\s*" + name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s*\\}\\}");
    setTimeout(() => {
      const ins = [...document.querySelectorAll<HTMLInputElement>(`#${CSS.escape(nodeId)}-opts input[role=combobox]`)];
      (ins.find((i) => uses.test(i.value)) ?? ins[0])?.focus();
    }, 0);
  };
  return (
    <section className="card" aria-labelledby="vars-h">
      <div className="hd"><h2 id="vars-h">Variables</h2><span className="muted sm">{used.size} used here · {vars.length} set here</span>
        <button className="btn sm ghost" type="button" aria-expanded={shown} aria-controls="vars-body" onClick={() => setShown(!shown)}>{shown ? "Hide" : "Show"}</button></div>
      <div id="vars-body" className="bd stack" hidden={!shown}>
        {bad && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{unknown.length === 1 ? "1 field uses a name that isn't a variable" : unknown.length + " fields use a name that isn't a variable"}: {unknown.map((p, i) => <span key={i}>{i > 0 && ", "}<button className="linkbtn" type="button" onClick={() => openField(p.nodeId, p.name)}>{p.title}</button></span>)}</span></p>}
        <p className="muted sm">Values set in Settings apply to every workflow. A value set here wins for this workflow. A value typed straight into a step wins over both.</p>
        {vars.length === 0 ? <p className="muted">No variables yet. Add one below, then use it in a field as {"{{name}}"}.</p>
          : <table className="tbl vars-tbl">
            <thead><tr><th scope="col">Name</th><th scope="col">Value</th><th scope="col">Comes from</th><th scope="col">Used by</th><th scope="col"><span className="sr">Action</span></th></tr></thead>
            <tbody>{vars.map((v, i) => {
              const by = used.get(v.name) ?? [];
              return (
                <tr key={i}>
                  <th scope="row"><code>{"{{" + v.name + "}}"}</code>{nameIssues.filter((i) => (i.name ?? "") === v.name).map((i, k) => <span key={k} className="st bad" style={{ display: "flex", gap: 4 }}><Icon id="i-alert" /><span>{i.message}</span></span>)}{editing === i
                    ? <input className="input" autoFocus aria-label={"Description of {{" + v.name + "}}"} value={v.description ?? ""} onChange={(e) => setVars(vars.map((x, j) => (j === i ? { ...x, description: e.target.value } : x)), "desc:" + i)} />
                    : v.description && <span className="muted sm" style={{ display: "block" }}>{v.description}</span>}</th>
                  <td data-label="Value"><input className="input" aria-label={"Value of {{" + v.name + "}}"} value={String(v.value ?? "")} onChange={(e) => setVars(vars.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)), "value:" + i)} /></td>
                  <td><span className="pill vars-pill-own">This workflow</span></td>
                  <td data-label="Used by">{by.length ? by.map((n, k) => <span key={n.id}>{k > 0 && ", "}<button className="linkbtn" type="button" onClick={() => openField(n.id, v.name)}>{titleOf(n)}</button></span>) : <span className="muted">Not used yet</span>}</td>
                  <td><button className="btn sm ghost" type="button" aria-expanded={editing === i} aria-label={"Edit {{" + v.name + "}} description"} onClick={() => setEditing(editing === i ? null : i)}>{editing === i ? "Done" : "Edit"}</button>{" "}<button className="btn sm ghost" type="button" aria-label={"Delete {{" + v.name + "}}"} onClick={() => remove(v.name, i)}>Delete</button></td>
                </tr>);
            })}</tbody>
          </table>}
        <form className="row vars-add" onSubmit={add} noValidate aria-label="Add a variable">
          <div className="field"><label htmlFor="var-name">Name</label><input id="var-name" className="input" value={name} onChange={(e) => (setName(e.target.value), setErr(""))} aria-describedby={err ? "var-err" : undefined} /></div>
          <div className="field"><label htmlFor="var-val">Value</label><input id="var-val" className="input" value={val} onChange={(e) => setVal(e.target.value)} /></div>
          <div className="field"><label htmlFor="var-what">What it's for (optional)</label><input id="var-what" className="input" value={what} onChange={(e) => setWhat(e.target.value)} /></div>
          <button className="btn" type="submit">Add variable</button>
          {err && <span id="var-err" className="st bad" role="alert"><Icon id="i-alert" /><span>{err}</span></span>}
        </form>
      </div>
    </section>);
}

function Editor({ id, w, defs, first, reloadMeta }:{ id: string; w: Workflow; defs: StepDefinition[]; first: WorkflowRevision; reloadMeta: () => void }) {
  const [etag, setEtag] = useState(first.etag), layout = first.layout;
  const [hist, setHist] = useState<{ docs: WorkflowDoc[]; at: number }>({ docs: [first.workflow], at: 0 });
  const [saved, setSaved] = useState(first.workflow);
  const [live, setLive] = useState<Checked>({ issues: first.validation, stackable: first.stackable, stepSummaries: first.stepSummaries });
  // ?step=<node id>: that step starts open (the library's Output tab links here)
  const [open, setOpen] = useState<string | null>(() => new URLSearchParams(location.search).get("step")), [said, setSaid] = useState(""), [checked, setChecked] = useState(false);
  const [panel, setPanel] = useState<PublishPreview | null>(null), [renaming, setRenaming] = useState(false);
  // the workflow's own variables the server refused on the last save (W4a): shown on their rows until the next edit
  const [refused, setRefused] = useState<ValidationIssue[]>([]);
  // W11 list side: the delete bar (Undo, 8 s), the add-step menu, and the button that opened it (focus goes back there)
  const [gone, setGone] = useState<{ text: string; at: number } | null>(null), [hold, setHold] = useState(false), [addOpen, setAddOpen] = useState(false);
  const opener = useRef<HTMLElement | null>(null);
  const doc = hist.docs[hist.at], dirty = doc !== saved;
  const all = useData(listLibraries, []), libs = (all.data ?? []).filter((l) => l.flowId === id).map((l) => l.path);
  // {{name}} fields with a bad name (or an unclosed {{): Publish waits for them, once the variable list has loaded
  // the list is needed for the picker (a step takes variables) and for the Variables card's add form (it shows with own variables too)
  const tv = takesVars(defs), needVars = tv || (doc.variables ?? []).length > 0, vl = useData(() => varListFor(id, needVars), [id, needVars]).data, varBad = vl ? varProblems(doc.nodes, defs, withOwn(vl.variables, doc.variables ?? [])) : [];
  // The server's words and problems for the document as it is now (nothing saved): asked 300 ms after a change.
  const ask = async (d: WorkflowDoc) => { const r = await act(validateDoc(id, d), setSaid); if (r) setLive(r); return r; };
  const seen = useRef<WorkflowDoc | null>(null); // null: the first check runs on load, so the list has the server's places and add rules
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
  // A run of typing in one variable's value (the same run key) writes into one history entry, so Undo takes back the whole run.
  const typing = useRef<{ run: string; at: number } | null>(null);
  const edit = (d: WorkflowDoc, run?: string) => {
    setRefused([]);
    const t = typing.current;
    if (run && t && t.run === run && t.at === hist.at) setHist({ docs: hist.docs.map((x, i) => (i === hist.at ? d : x)), at: hist.at });
    else { typing.current = run ? { run, at: hist.at + 1 } : null; setHist({ docs: [...hist.docs.slice(0, hist.at + 1), d], at: hist.at + 1 }); }
    setChecked(false); setPanel(null); setSaid(""); setGone(null);
  };
  const update = (n: Node) => edit({ ...doc, nodes: doc.nodes.map((x) => (x.id === n.id ? n : x)) });
  // "Start from Shrink videos": the template's whole document as one edit, so Undo (the note below) takes it back
  const starting = useRef(false); // a second click while the first is loading does nothing
  const startShrink = async () => {
    if (starting.current) return;
    starting.current = true;
    try {
      const list = await act(listTemplates(), setSaid); // on a failure act has already said the server's message
      if (!list) return;
      const t = list.find((x) => x.id === "shrink_videos")?.workflow;
      if (!t) return setSaid("The Shrink videos template isn't available right now.");
      edit(t); setGone({ text: "Started from Shrink videos. Change any step as you like.", at: hist.at + 1 });
    } finally { starting.current = false; }
  };
  const defOf = (t: string) => defs.find((d) => d.type === t);
  const nameOf = (x: Node) => x.name ?? defOf(x.type)?.name ?? x.type;
  // the delete bar stays 8 s, and longer while it has focus or the pointer is on it
  useEffect(() => { if (!gone || hold) return; const t = setTimeout(() => setGone(null), 8000); return () => clearTimeout(t); }, [gone, hold]);
  const moveStep = (nid: ID, dir: -1 | 1) => {
    const d = moveNode(doc, nid, dir), now = order(d), k = now.findIndex((x) => x.id === nid);
    edit(d); setSaid(`Moved ${nameOf(now[k])} to step ${k + 1} of ${now.length}.`);
  };
  const deleteStep = (nid: ID) => {
    const before = order(doc), i = before.findIndex((x) => x.id === nid), prev = before[i - 1], nxt = before[i + 1], gone0 = before[i];
    edit(deleteNode(doc, nid));
    setGone({ text: prev && nxt ? `Deleted ${nameOf(gone0)}. ${nameOf(prev)} now goes straight to ${nameOf(nxt)}.` : `Deleted ${nameOf(gone0)}.`, at: hist.at + 1 });
    // focus goes to the next row's More button, or the previous row's when it was the last (never the input or the save row)
    const to = [nxt, prev].find((x) => x && x.type !== "input.library" && x.type !== "output.commit");
    if (to) requestAnimationFrame(() => document.getElementById(to.id + "-more")?.focus());
  };
  const undoGone = () => { if (gone) setHist({ ...hist, at: gone.at - 1 }); setGone(null); };
  const endDef = defs.find((x) => x.type === "flow.end"), failDef = defs.find((x) => x.type === "flow.fail"), gotoDef = defs.find((x) => x.type === "flow.goto");
  // ui-shop review #579: a hand-over's picked workflow is remembered through a switch of Otherwise and back, so an
  // accidental change doesn't empty the picker (the end step is rebuilt from its defaults by setOtherwise)
  const lastGoto = useRef<Record<ID, Record<string, unknown>>>({});
  const setElse = (q: Node, k: ElseKind) => {
    if (k === "other") { navigate(`/workflows/${id}/graph?node=${encodeURIComponent(q.id)}`); return; }
    const def = k === "leave" ? endDef : k === "fail" ? failDef : gotoDef;
    if (!def) return setSaid("This server has no such step.");
    const cur = otherwiseOf(doc, q);
    if (cur.kind === "goto" && cur.node && Object.keys(cur.node.config).length > 0) lastGoto.current[q.id] = cur.node.config;
    let next = setOtherwise(doc, q, def);
    const remembered = lastGoto.current[q.id], made = k === "goto" && next !== doc ? otherwiseOf(next, q).node : undefined;
    if (made && remembered && Object.keys(made.config).length === 0) next = { ...next, nodes: next.nodes.map((n) => (n.id === made.id ? { ...n, config: { ...remembered } } : n)) };
    if (next !== doc) { edit(next); setSaid(`${nameOf(q)}: when the answer is No, ${k === "leave" ? "the file is left alone" : k === "fail" ? "the run stops as failed" : made && remembered ? "the file is handed to another workflow, the one picked before" : "the file is handed to another workflow: choose which, under the step"}.`); }
  };
  const addStep = (d: StepDefinition, after?: ID) => {
    const question = d.outputs.some((o) => o.outcome === "branch");
    if (question && (!live.addSteps?.[d.type]?.addable || !endDef)) { navigate(`/workflows/${id}/graph?add=${encodeURIComponent(d.type)}`); return; }
    // a draft with no Library files step gets one first (the page adds it itself), so the chain has a start and the new step shows
    const inDef = defs.find((x) => x.type === "input.library"), start = inDef && !doc.nodes.some((n) => n.type === "input.library") ? { ...doc, nodes: [...doc.nodes, newNode(inDef, "n" + Math.random().toString(36).slice(2, 8))] } : doc;
    const nid = "n" + Math.random().toString(36).slice(2, 8), next = question ? insertQuestion(start, after, newNode(d, nid), endDef!) : insertAfter(start, after, newNode(d, nid)), now = order(next);
    edit(next); setOpen(nid); setAddOpen(false);
    requestAnimationFrame(() => document.getElementById(nid + "-more")?.focus()); // focus lands on the new step's row, not the body
    setSaid(`Added ${d.name} as step ${now.findIndex((x) => x.id === nid) + 1} of ${now.length}.`);
  };
  const openAdd = (e: { currentTarget: HTMLElement }) => { opener.current = e.currentTarget; setAddOpen(true); };
  const closeAdd = () => { setAddOpen(false); requestAnimationFrame(() => opener.current?.focus()); };
  const save = async () => {
    let r: Answer<WorkflowRevision>;
    try { r = await saveDraft(id, doc, layout, etag); } catch (e) { if (e instanceof SignedOut) return null; throw e; }
    if (failed(r)) {
      // a refused variable name comes back as the error's issues (one per name), with nothing saved
      const bad = ((r.error as { issues?: ValidationIssue[] }).issues ?? []).filter((i) => VAR_CODES.includes(i.code));
      if (bad.length) { setRefused(bad); setSaid(refusedWords(bad)); } else setSaid(r.error.message);
      return null;
    }
    setRefused([]);
    setEtag(r.etag); setSaved(doc); setLive((l) => ({ ...l, issues: r.validation, stackable: r.stackable, stepSummaries: r.stepSummaries })); setSaid("Draft saved."); reloadMeta();
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
  // the variable problems to show on their rows: the last refused save's, else the server's live check of the document
  const nameIssues = refused.length ? refused : issues.filter((i) => VAR_CODES.includes(i.code));
  const tools = <>
    <button className="btn sm icon ghost" type="button" title="Undo" aria-label="Undo" disabled={hist.at === 0} onClick={() => setHist({ ...hist, at: hist.at - 1 })}><Icon id="i-undo" /></button>
    <button className="btn sm icon ghost" type="button" title="Redo" aria-label="Redo" disabled={hist.at === hist.docs.length - 1} onClick={() => setHist({ ...hist, at: hist.at + 1 })}><Icon id="i-redo" /></button>
    <button className="btn sm ghost" type="button" onClick={async () => { if (await ask(doc)) setChecked(true); }}><Icon id="i-checks" />Check</button>
    <button className="btn sm" type="button" disabled={!dirty} onClick={save}><Icon id="i-save" />Save draft</button>
  </>;
  const canStack = live.stackable;
  const list = order(doc), total = list.length, tailOut = list[total - 1]?.type === "output.commit", lastMovable = total - 1 - (tailOut ? 1 : 0), only = total <= 1;
  // FB3 (flow-builder.md 4.3): the simple view is this list filtered to the cards that shape the one conversion (the
  // steps that convert, check and save), each with its form on the page; the questions and actions before them and
  // the steps after the save fold into one line each, which opens the steps view. Same document, same checks.
  const simple = new URLSearchParams(location.search).get("view") === "simple";
  const shapes = (n: Node) => { const r = defs.find((d) => d.type === n.type)?.runsAs; return r === "plan" || r === "verify" || r === "commit"; };
  const saveAt = list.findIndex((n) => n.type === "output.commit");
  const before = list.filter((n, i) => i > 0 && !shapes(n) && (saveAt < 0 || i < saveAt)), after = saveAt < 0 ? [] : list.slice(saveAt + 1);
  const shown = simple ? list.filter(shapes) : list;
  const fold = (ns: Node[], what: string) => ns.length === 0 ? null : <li className="srow fold"><div className="sline"><span className="stepn" aria-hidden="true">·</span><Icon id="i-layers" />
    <div className="sp"><span className="snm">{ns.length === 1 ? "1 step " + what : ns.length + " steps " + what}</span> <span className="ssum">{ns.map((n) => n.name ?? defs.find((d) => d.type === n.type)?.name ?? n.type).join(" · ")}</span></div>
    <Link className="btn sm ghost vlink" to={"/workflows/" + id}>Open the steps view</Link></div></li>;
  return (
    <Reg.Provider value={{ defs, words: live.stepSummaries, libs, wf: id }}>
    <OwnVars.Provider value={doc.variables ?? []}>
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
        {simple ? <Link className="btn sm ghost vlink" to={"/workflows/" + id}>All steps</Link> : <Link className="btn sm ghost vlink" to={"/workflows/" + id + "?view=simple"}>Simple view</Link>}
        <Link className="btn sm ghost" to={"/workflows/" + id + "/graph"}>Graph</Link>
        {canStack && <>
          <span className="desk">{tools}</span>
          <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{tools}</div></details>
          <button className="btn sm primary" type="button" disabled={varBad.length > 0} title={varBad.length ? "Fix the variable names first" : undefined} onClick={openPublish}>Publish</button>
        </>}
      </PageHead>
      {canStack && (checked || !dirty) && (issues.length === 0 ? <p className="st ok checkline"><Icon id="i-check-c" /><span>Check: no problems</span></p>
        : <p className="st bad checkline"><Icon id="i-alert" /><span>Check: {issues.length === 1 ? "1 problem" : issues.length + " problems"}. Publish waits until {issues.length === 1 ? "it's" : "they're"} fixed. <button className="linkbtn" type="button" onClick={() => setOpen(issues[0].nodeId ?? null)}>Show</button></span></p>)}
      {canStack && varBad.length > 0 && <p className="st bad" role="alert"><Icon id="i-alert" /><span>Fix {varBad.length === 1 ? "1 field" : varBad.length + " fields"} before publishing. <button className="linkbtn" type="button" onClick={() => setOpen(varBad[0].nodeId)}>{varBad[0].title}</button></span></p>}
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

      {canStack ? <>
        {/* no field takes variables yet (the server marks none): show the card only if the draft already has some, so they can still be seen and deleted */}
        {(tv || (doc.variables ?? []).length > 0) && <VariablesCard doc={doc} defs={defs} builtins={builtinsOf(vl?.variables ?? [])} problems={varBad} nameIssues={nameIssues} edit={edit} open={(n) => setOpen(n)} />}
        {only && <div className="note"><Icon id="i-info" /><div className="stack" style={{ gap: 6 }}>
          <strong>This workflow has no steps yet</strong>
          <span>Every workflow starts with Library files: the videos from the libraries that use it. Add what should happen to them next.</span>
          <div className="row"><button className="btn sm primary" type="button" autoFocus onClick={openAdd}><Icon id="i-plus" />Add the first step</button>
            <button className="btn sm" type="button" onClick={startShrink}>Start from Shrink videos</button></div>
        </div></div>}
        {simple && <p className="muted simplenote">The steps that convert, check and save. {before.length + after.length > 0 ? "The rest of this workflow is folded; the steps view has everything." : "Questions, notes and what happens after the save go in the steps view."}</p>}
        <ol className="steps">{simple && fold(before, "before converting")}{shown.map((n) => {
          const i = list.indexOf(n);
          // the server's places: no answer for a step means it's fixed. Moves stay inside the first and last steps.
          const p = live.stepPlaces?.[n.id], fixed = p ? p.fixedPlace : true;
          const mv: Move = { fixed, reason: p?.fixedReason, up: !fixed && !simple && i > 1 && i <= lastMovable, down: !fixed && !simple && i >= 1 && i < lastMovable, del: !fixed && !simple && i >= 1 && i <= lastMovable, // the simple view shows no place badge or menu: moving and deleting live in the steps view (ui-shop review #543)
            act: (w) => (w === "del" ? deleteStep(n.id) : moveStep(n.id, w === "up" ? -1 : 1)) };
          const ow = isQuestion(n) ? otherwiseOf(doc, n) : undefined;
          const otherwise: Else | undefined = ow ? { kind: ow.kind, set: (k) => setElse(n, k), node: ow.node, update } : undefined;
          return <Step key={n.id} n={n} i={i} mv={mv} open={open === n.id} toggle={() => setOpen(open === n.id ? null : n.id)} update={update} issues={issues} otherwise={otherwise} inline={simple} />;
        })}{simple && fold(after, "after the file is saved")}</ol>
        {!only && !simple && <button className="btn sm ghost" type="button" onClick={openAdd}><Icon id="i-plus" />Add step</button>}
        {addOpen && <AddStepMenu wf={id} doc={doc} defs={defs} adds={live.addSteps} onAdd={addStep} onClose={closeAdd} />}
        {/* one status region, always mounted: a delete fills its text, so screen readers announce it */}
        <p className="sr" role="status" aria-live="polite">{gone && hist.at === gone.at ? gone.text : ""}</p>
        {gone && hist.at === gone.at && <div className="note" onPointerEnter={() => setHold(true)} onPointerLeave={() => setHold(false)} onFocus={() => setHold(true)} onBlur={() => setHold(false)}>
          <Icon id="i-undo" /><span className="sp">{gone.text}</span><button className="btn sm ghost" type="button" onClick={undoGone}>Undo</button></div>}
      </> : <ReadOnly doc={doc} id={id} />}
    </div>
    </OwnVars.Provider>
    </Reg.Provider>);
}
