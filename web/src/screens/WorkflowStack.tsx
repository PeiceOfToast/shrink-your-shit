// V3b Stack view (spec 5.7; DESIGN/v2 SCREENS V3, mockups/workflow-stack.html and -readonly.html) on the real /api/v2
// (#109: step definitions, the draft, validate, publish), or the mock with ?mock=1 (mock/steps.ts mirrors V1). The step
// words and problems come from the server's validate, asked again shortly after each change. The handle moves a step by
// pointer or keys (W11). Not here: the graph's keyboard moves, Test run (V5), History, Export, Open graph (V4).
import { Component, Fragment, createContext, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode, type RefObject } from "react";
import { Icon } from "../Icon";
import { GC_CHOICES, gcLabel } from "../gcLabel";
import { PageHead } from "../PageHead";
import { FlowTabs } from "./Guided";
import { SignedOut, failed, type Answer } from "../api";
import { afterOnly, beforeSave, deleteNode, dropResult, groupOf, insertAfter, insertAfterSave, insertQuestion, isQuestion, moveNode, newNode, order, otherwiseOf, setOtherwise, type Otherwise } from "../stepEdits"; // the steps in running order: a property of the document, not data
import { useData } from "../useData";
import { exportWorkflow, getDraft, getLadder, getWorkflow, listTemplates, listWorkflows, patchWorkflow, publish, publishPreview, saveDraft, stepDefinitions, validateDoc, type Checked, type Ladder } from "../workflowApi";
import { Link, navigate, onLeave, useSearch } from "../router";
import { Browse } from "../FolderPicker";
import { OwnVars, VarInput, builtinsOf, takesVars, varListFor, varProblems, withOwn } from "../VarInput";
import { refProblem } from "../refFit";
import { customEdit, formatEdit, qualityCap } from "../convertFormat";
import { refusalText } from "../refusalWords";
import { touchPress } from "../touchHold";
import "../styles/variables.css";
import { getSettings, listLibraries, shrunk } from "../libraryApi";
import type { TemplateVar } from "../mock/variables";
import type { AddStep, ID, JSONSchema, Node, PublishPreview, StepDefinition, ValidationIssue, Variable, Workflow, WorkflowDoc, WorkflowRevision } from "../types/v2";
import { closeMenuToSummary, menuEscape } from "../menuFocus";

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
type RefEntry = { name: string; value: string | null; type?: string; problem: string };
function RefPick({ fid, title, value, set, back, selectRef, none, s, wf }: { fid: string; title: string; value: unknown; set: (v: unknown) => void; back: () => void; selectRef: RefObject<HTMLSelectElement | null>; none: unknown; s: JSONSchema; wf: string }) {
  // W1 (#521), W2 (contract 09:33Z): a typed setting takes one whole {{name}}: the workflow's own variable (which wins a
  // shared name), a server-wide one, or one of the file's own values. The built-ins fill text fields only (ui-shop
  // review2 #559). A kind that doesn't fit the setting is greyed with its words; the server is the judge on save
  // (variable_wrong_type comes back on the field). A secret never sets one, so it is listed but can't be chosen.
  const own = useContext(OwnVars), cur = refName(value);
  const list = useData(() => varListFor(wf, true), [wf]).data, served = list && !failed(list) ? list.variables : [];
  // ui-shop review #596: the fit follows the value too (text that reads as a number fills a number setting; a
  // fraction doesn't fill a whole-number one; no value yet is refused), the way the server checks on save
  const entry = (name: string, type: string | undefined, value: string | null, file = false): RefEntry => ({ name, type, value, problem: refProblem(s, { type, value, file }) });
  const mine = own.map((v) => entry(v.name, v.type, v.type === "secret" ? null : v.hasValue === false || v.value == null ? null : String(v.value)));
  const ownNames = new Set(mine.map((v) => v.name));
  const server = served.filter((v) => v.group === "Server" && !ownNames.has(v.name)).map((v) => entry(v.name, v.type, v.value));
  const file = served.filter((v) => v.wholeFieldOnly).map((v) => entry(v.name, v.type, v.value, true));
  const known = [...mine, ...server, ...file].some((v) => v.name === cur);
  const label = (v: RefEntry) => "{{" + v.name + "}}" + (v.problem ? " · " + v.problem : v.value != null && v.value !== "" ? " · " + v.value : "");
  const group = (name: string, vars: RefEntry[]) => vars.length > 0 && <optgroup label={name}>{vars.map((v) => <option key={v.name} value={v.name} disabled={v.problem !== ""}>{label(v)}</option>)}</optgroup>;
  return (
    <div className="field">
      <label htmlFor={fid}>{title}</label>
      <div className="row nw" style={{ gap: 8 }}>
        <select ref={selectRef} id={fid} className="input" value={cur} aria-describedby={fid + "-ref"} onChange={(e) => { const next = e.target.value ? "{{" + e.target.value + "}}" : none; if (next !== value) set(next); }}>
          <option value="">Choose a variable…</option>
          {cur && !known && <option value={cur}>{"{{" + cur + "}}"} · not a variable here</option>}
          {group("Your variables", mine)}
          {group("Server-wide variables", server)}
          {group("The file's own values", file)}
        </select>
        <button className="btn sm ghost nowrap" type="button" onClick={back}>Type a value instead</button>
      </div>
      <span className="hint" id={fid + "-ref"}>{mine.length === 0 && server.length === 0 ? <>This workflow has no variables yet: add one in Variables above or on the Workflows page's <Link to="/workflows?tab=variables">Variables tab</Link>, or pick one of the file's own values.</> : "The variable's value must fit the setting; one that won't is greyed and says why. A file value's kind is checked when you save; its value once the job has the file."}</span>
    </div>);
}

function Field(props: { id: string; name: string; s: JSONSchema; value: unknown; set: (v: unknown) => void; wf: string; off?: string; describe?: string }) {
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
  if (inRef) return <RefPick fid={fid} title={title} value={value} set={set} selectRef={pick} none={none} s={s} wf={props.wf} back={() => {
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
      <select id={fid} className="input" value={value} onChange={(e) => set(e.target.value === "" ? undefined : e.target.value)} aria-describedby={fid + "-h"}>
        <option value="">Choose a workflow…</option>
        {value && !known && <option value={value}>{value === wf ? "This workflow" : all.data && !failed(all.data) ? "A workflow that isn't published, or isn't there" : "Workflow " + value}</option>}
        {list.map((w) => <option key={w.id} value={w.id}>{w.name}</option>)}
      </select>
      <span className="hint" id={fid + "-h"}>{list.length === 0 ? "No other workflow is published yet: publish one first, then choose it here." : description ?? ""}</span>
    </div>);
}

// off: why the control can't be used right now (its switch is disabled and says so); describe: the id of a line that explains this field
function Control({ id, name, s, value, set, wf, off, describe }: { id: string; name: string; s: JSONSchema; value: unknown; set: (v: unknown) => void; wf: string; off?: string; describe?: string }) {
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
  if (s.type === "boolean") {
    // the words sit under the switch, outside its label, so they are not part of its name (the radio lesson of P2)
    const line = off ?? (typeof s.description === "string" ? s.description : ""), hid = fid + "-h";
    return (
      <div className="field">
        <label className="row nw" htmlFor={fid}><input id={fid} type="checkbox" role="switch" className="switch" checked={value === true} disabled={!!off} aria-describedby={line ? hid : undefined} onChange={(e) => set(e.target.checked)} /><span>{title}</span></label>
        {line && <span className="hint" id={hid}>{line}</span>}
      </div>);
  }
  if (s.enum && widget(s) === "segmented") {
    const avail = s["x-available"] as unknown[] | undefined;
    return (
      <fieldset className="field choice" aria-describedby={describe}><legend className="lab">{title}</legend><div className="row">
        {labels(s).map(([v, l]) => (
          <label key={String(v)} className="radio"><input type="radio" name={fid} checked={value === v} disabled={!!avail && !avail.includes(v)} onChange={() => set(v)} /><span>{l}</span></label>))}
      </div><Hint text={hintFor(s, value)} />{name === "subtitles" && value === "srt" && <SrtFiles />}{typeof s.description === "string" && <span className="hint">{s.description}</span>}</fieldset>);
  }
  if (s.enum)
    return <div className="field"><label htmlFor={fid}>{title}</label><select id={fid} className="input" value={String(value)} aria-describedby={describe} onChange={(e) => set(labels(s).find((o) => String(o[0]) === e.target.value)?.[0])}>
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

const GC_FAIL: [string, string, string][] = [
  ["next", "Try another way", "Steps down to the next way of converting the file, as before."],
  ["fail", "Fail the job", "Stops at the first failure and says why. The file isn't tried again by itself."]];
// index 0 is 'Same as Quality' (the default, saved as absent); 1 to 5 are the Quality names
const CPU_Q = ["Same as Quality", "Smallest file", "Smaller", "Balanced", "Better quality", "Best quality"];

export function Options({ n, set, issues }: { n: Node; set: (k: string, v: unknown, more?: Record<string, unknown>) => void; issues: ValidationIssue[] }) {
  const { stepDef, libs, wf } = useReg(), def = stepDef(n.type)!, props = Object.fromEntries(Object.entries((def.configSchema.properties ?? {}) as Props).filter(([, s]) => !s.readOnly)) as Props, c = n.config;
  const lad = useData(getLadder, []).data;
  // x-onlyWhen: a setting the server reads only while another setting has one of the listed values (Compare the result's
  // percent and seconds): the form shows it only then
  const shown = (k: string) => Object.entries((props[k]["x-onlyWhen"] as Record<string, unknown[]> | undefined) ?? {}).every(([key, vals]) => vals.includes(c[key] ?? props[key]?.default));
  const conv = (k: string) => (!shown(k) ? null : n.type === "video.convert" ? convertField(k, props[k], c, lad) : props[k]);
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
    let x: Record<string, unknown> = {};
    if (n.type === "video.convert") { const f = formatEdit(c, k, v); x = f.x; if (f.said !== undefined) setSaid(f.said); } // #697: the rules live in convertFormat.ts
    set(k, v, x);
  };
  // Audio (P3): As it is copies every track, so Even out loudness is off and says why, and the Audio choice points at that line
  const asIs = n.type === "audio.tracks" && c.audio === "as_is" && keys.includes("evenLoudness"), AS_IS_LINE = "Not used with As it is: every track is copied unchanged.";
  const field = (k: string) => { const b = bad(k), sk = conv(k); if (!sk) return null; return (
    <div key={k} className={b.length ? "fieldbad" : undefined} data-field={k} aria-invalid={b.length ? true : undefined}>
      <Field id={n.id} name={k} s={sk} value={c[k] ?? sk.default} set={(v) => put(k, v)} wf={wf}
        off={asIs && k === "evenLoudness" ? AS_IS_LINE : undefined} describe={asIs && k === "audio" ? n.id + "-evenLoudness-h" : undefined} />
      {b.map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
    </div>); };
  // a problem in a field under 'More options' opens it by itself (review c0189fa P3), also when the problems list picks
  // this step; it isn't closed again when the last one is fixed, so the field doesn't vanish while it's being edited
  const moreRef = useRef<HTMLDetailsElement>(null), moreBad = more.some((k) => bad(k).length > 0);
  // an open the page makes itself is not the person's choice: it must not switch the step to custom settings (W1 screen:
  // a problem on a More-options field opened it, the toggle wrote kind=selectors, the draft went dirty and the live problems hid)
  const byPage = useRef(false);
  useEffect(() => { if (moreBad && moreRef.current && !moreRef.current.open) { byPage.current = true; moreRef.current.open = true; } }, [moreBad, n.id]);
  // Convert's 'Graphics card' block (design-direction-w7-f4-convert): open on load when a value isn't the default or a problem
  // points at one of its fields. Opening or closing it writes nothing.
  const gcRef = useRef<HTMLDetailsElement>(null);
  const gcBad = ["hardware", "hardwareFail", "cpuQuality"].some((k) => bad(k).length > 0);
  const gcOff = (c.hardware ?? "auto") !== "auto" || (c.hardwareFail ?? "next") !== "next" || Number(c.cpuQuality ?? 0) !== 0;
  useEffect(() => { if (gcRef.current && (gcBad || gcOff) && !gcRef.current.open) gcRef.current.open = true; }, [gcBad, n.id]);
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
    // Quality only, as the slider has it: opening More options changes nothing until the person picks something
    const custom = () => !legacy && set("kind", "selectors", customEdit(c));
    // Quality only: a click or Space/Enter on it applies, even when it is already checked (no change event fires then; #706)
    const pickQuality = () => { if (rc !== "quality" || Object.keys(qualityCap(c, "quality")).length) put("rateControl", "quality"); };
    // the slider's own values: custom settings that differ from them are asked about before they're cleared (SCREENS V3)
    const SLIDER = { format: "hevc", resolution: "keep", maxBitrate: "none", speed: "normal", rateControl: "quality" };
    const differs = Object.entries(SLIDER).some(([k, v]) => String(c[k] ?? v) !== v) || c.vbrMaxKbps != null;
    const backToSlider = () => { setAsk(false); set("kind", "slider", { ...SLIDER, vbrMaxKbps: undefined }); };
    const keep = () => { setAsk(false); if (moreRef.current) moreRef.current.open = true; };
    const goBack = () => (differs ? setAsk(true) : backToSlider());
    // Picture (P6): the schema's own words and hints; bit depth's 10-bit is off with H.264 or VP9
    const fpsSchema = props.maxFrameRate ?? {}, bdSchema = props.bitDepth ?? {}, bd = String(c.bitDepth ?? "keep");
    // One hint line at a time: with H.264 or VP9 only "10-bit needs HEVC or AV1."; the HDR line shows only where 10-bit can be made
    const no10 = ["h264", "vp9"].includes(String(c.format ?? "hevc")), hdrLine = bd === "8" && !no10;
    const bdDescribed = [no10 ? n.id + "-bd-10" : "", hdrLine ? n.id + "-bd-8" : ""].filter(Boolean).join(" ");
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
              <label className="radio" style={{ alignItems: "flex-start" }}><input type="radio" name={n.id + "-rc"} checked={rc === "quality"} onChange={() => undefined} onClick={pickQuality} onKeyDown={(e) => { if (e.key === " " || e.key === "Enter") { e.preventDefault(); pickQuality(); } }} />
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
        {/* Picture and Graphics card sit below More options, the same in both modes. They write with set, not put: put would switch the step to custom settings. A default is saved as absent. */}
        <fieldset className="field choice" style={{ marginTop: 12 }}><legend className="lab">Picture</legend>
          <div className="grid cols2">
            {props.maxFrameRate && <div className={bad("maxFrameRate").length ? "fieldbad" : undefined} data-field="maxFrameRate" aria-invalid={bad("maxFrameRate").length ? true : undefined}>
              <div className="field"><label htmlFor={n.id + "-fps"}>Frame rate</label>
                <select id={n.id + "-fps"} className="input" value={String(c.maxFrameRate ?? "keep")} aria-describedby={n.id + "-fps-h"} onChange={(e) => set("maxFrameRate", e.target.value === "keep" ? undefined : e.target.value)}>
                  {labels(fpsSchema).map(([v, l]) => <option key={String(v)} value={String(v)}>{l}</option>)}
                </select>
                <span className="hint" id={n.id + "-fps-h"}>{fpsSchema.description as string}</span>
                {bad("maxFrameRate").map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
              </div>
            </div>}
            {props.bitDepth && <div className={bad("bitDepth").length ? "fieldbad" : undefined} data-field="bitDepth" aria-invalid={bad("bitDepth").length ? true : undefined}>
              <fieldset className="field choice" aria-describedby={bdDescribed || undefined}><legend className="lab">Bit depth</legend>
                <div className="seg radios bitdepth">
                  {labels(bdSchema).map(([v, l]) => { const val = String(v), off = val === "10" && no10; return (
                    <label key={val} className={bd === val ? "on" : undefined}><input type="radio" name={n.id + "-bd"} checked={bd === val} disabled={off} onChange={() => set("bitDepth", val === "keep" ? undefined : val)} />
                      <span>{l}</span></label>); })}
                </div>
                {no10 && <span className="hint" id={n.id + "-bd-10"} style={{ display: "block" }}>10-bit needs HEVC or AV1.</span>}
                {hdrLine && <span className="hint" id={n.id + "-bd-8"} style={{ display: "block" }}>HDR videos stay 10-bit.</span>}
              </fieldset>
              {bad("bitDepth").map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
            </div>}
            <div style={{ gridColumn: "1 / -1" }} className={bad("removeBlackBars").length ? "fieldbad" : undefined} data-field="removeBlackBars">
              <div className="field"><label className="row nw" htmlFor={n.id + "-bars"}><input id={n.id + "-bars"} type="checkbox" role="switch" className="switch" checked={c.removeBlackBars === true} aria-describedby={n.id + "-bars-1 " + n.id + "-bars-2"} aria-invalid={bad("removeBlackBars").length ? true : undefined} onChange={(e) => set("removeBlackBars", e.target.checked || undefined)} /><span>Remove black bars</span></label>
              <span className="hint" id={n.id + "-bars-1"} style={{ display: "block" }}>Cuts the black bars above and below (or beside) the picture, when samples from the whole video agree where they are. If they don't, the video is left as it is and the Log says why.</span>
              <span className="hint" id={n.id + "-bars-2"} style={{ display: "block" }}>Not yet on remote workers: they leave the bars and say so in the Log.</span>
              {bad("removeBlackBars").map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
              </div>
            </div>
          </div>
        </fieldset>
        <details ref={gcRef} style={{ marginTop: 12 }}><summary className="btn sm" style={{ display: "inline-flex" }}>Graphics card: {gcLabel(c.hardware)}</summary>
          <div className="stack" style={{ marginTop: 12 }}>
            <div className={bad("hardware").length ? "fieldbad" : undefined} data-field="hardware" aria-invalid={bad("hardware").length ? true : undefined}>
            <fieldset className="field choice" aria-describedby={n.id + "-hwh"}><legend className="lab">Graphics card</legend>
              {/* one segmented row at 1440 with the chosen hint under it; at 390 stacked rows with each hint under its label (.hwseg rules in shell.css) */}
              <div className="seg radios hwseg">
                {GC_CHOICES.map(([v, l, h]) => (
                  <Fragment key={v}>
                    <label className={String(c.hardware ?? "auto") === v ? "on" : undefined}><input type="radio" name={n.id + "-hw"} checked={String(c.hardware ?? "auto") === v} aria-describedby={n.id + "-hwo-" + v} onChange={() => set("hardware", v === "auto" ? undefined : v)} />
                      <span>{l}</span></label>
                    <span className="hint hwopt" id={n.id + "-hwo-" + v}>{h}</span>
                  </Fragment>))}
              </div>
              <span className="hint hwchosen" id={n.id + "-hwh"}>{GC_CHOICES.find((x) => x[0] === String(c.hardware ?? "auto"))?.[2]}</span>
              {bad("hardware").map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
            </fieldset>
            </div>
            {String(c.hardware ?? "auto") !== "cpu" && <div className={bad("hardwareFail").length ? "fieldbad" : undefined} data-field="hardwareFail" aria-invalid={bad("hardwareFail").length ? true : undefined}>
            <fieldset className="field choice" aria-describedby={n.id + "-hwfh"}><legend className="lab">If the graphics card fails</legend>
              <div className="seg radios hwfail">{GC_FAIL.map(([v, l]) => { const on = String(c.hardwareFail ?? "next") === v; return (
                <label key={v} className={on ? "on" : undefined}><input type="radio" name={n.id + "-hwfail"} checked={on} onChange={() => set("hardwareFail", v === "next" ? undefined : v)} /><span>{l}</span></label>); })}</div>
              <span className="hint" id={n.id + "-hwfh"}>{GC_FAIL.find((x) => x[0] === String(c.hardwareFail ?? "next"))?.[2]}</span>
              {bad("hardwareFail").map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
            </fieldset>
            </div>}
            {String(c.hardware ?? "auto") !== "only" && (!sel || rc === "quality") && <div className={bad("cpuQuality").length ? "fieldbad" : undefined} data-field="cpuQuality"><div className="field">
              <label htmlFor={n.id + "-cpuq"}>Quality on the processor</label>
              <select id={n.id + "-cpuq"} className="input" value={String(Number(c.cpuQuality ?? 0))} aria-invalid={bad("cpuQuality").length ? true : undefined} onChange={(e) => set("cpuQuality", Number(e.target.value) || undefined)}>
                {CPU_Q.map((l, i) => <option key={i} value={i}>{l}</option>)}</select>
              <span className="hint">The processor and the graphics card can give different results at the same Quality. Pick a position here to set the processor's own.</span>
              {bad("cpuQuality").map((i, x) => <span key={x} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{i.message}</span>)}
            </div></div>}
          </div>
        </details>
        {/* always mounted, so the live region exists before its first message; visually hidden while empty */}
        <p className={said ? "hint" : "sr"} role="status">{said}</p>
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
// W11: every row between Library files and Save the result can move and be deleted; the first and last steps have no menu.
type DragEv = { button?: number; pointerId: number; pointerType?: string; clientX: number; clientY: number; currentTarget: HTMLElement; preventDefault: () => void };
type DragMove = { pointerId: number; clientX: number; clientY: number };
type Move = { up: boolean; down: boolean; del: boolean; act: (w: "up" | "down" | "del") => void; grip?: { label: string; lifted: boolean; onKey: (e: { key: string; preventDefault: () => void }) => void; drag: { down: (e: DragEv) => void; cancel: () => void } } };
// FB1d: a question's row says what its No does: one of the ends, or another path the graph shows. FB4b: "hand to
// another workflow" is an end with a setting, so the row shows the workflow picker under it (node: that end).
type ElseKind = "leave" | "fail" | "goto" | "other" | "none";
type Else = { kind: Otherwise; set: (k: ElseKind) => void; node?: Node; update?: (n: Node) => void; check?: boolean };
const OTHERWISE: [Otherwise, string][] = [["leave", "leave the file alone"], ["fail", "stop as failed"], ["goto", "hand to another workflow"], ["other", "another path (in the graph)"]];
// FB4b: after the save nothing ends the run, so a question there offers only "nothing more" or the graph
const OTHERWISE_AFTER: [Otherwise, string][] = [["none", "nothing more"], ["other", "another path (in the graph)"]];
// FB4b-3: between the check and the save a No keeps the original (the new file is thrown away) or stops the run;
// a hand-over makes no sense there, since the conversion is done
const OTHERWISE_CHECK: [Otherwise, string][] = [["leave", "leave the file alone"], ["fail", "stop as failed"], ["other", "another path (in the graph)"]];
// W9: a step whose error port runs after the save (fileOn "saved") says what happens when it fails, and links to the graph
function Step({ n, i, mv, open, toggle, update, issues, otherwise, inline, graphHref }: { n: Node; i: number; mv: Move; open: boolean; toggle: () => void; update: (n: Node) => void; issues: ValidationIssue[]; otherwise?: Else; inline?: boolean; graphHref?: string }) {
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
  const errSaved = def.fileOn?.error === "saved" && (def.outputs ?? []).some((o) => o.name === "error");
  const end = n.type === "input.library" ? "first" : n.type === "output.commit" ? "last" : undefined, nm = n.name ?? def.name;
  // the refused order's sentence: the same words as the bar; in the options, so a phone's full-screen sheet shows it first
  const refused = mine.filter((x) => x.code === "not_runnable_yet").map((x, k) => <p key={"refused" + k} className="hint" style={{ color: "var(--error-text)" }}>{refusalText(x.message)}</p>);
  return (
    <li className={"srow" + (open ? " open" : "") + (mv.grip?.lifted ? " lifted" : "")} data-sid={n.id} aria-labelledby={n.id + "-name"}>
      <div className="sline">
        {!end && !inline && mv.grip && <button id={n.id + "-grip"} className="btn icon ghost grip" type="button" aria-label={mv.grip.label} aria-describedby="order-help" data-lifted={mv.grip.lifted ? "" : undefined} onKeyDown={mv.grip.onKey} onClick={(e) => e.stopPropagation()} onPointerDown={mv.grip.drag.down} onPointerCancel={mv.grip.drag.cancel}><Icon id="i-grip" /></button>}
        <span className="stepn">{i + 1}</span><span className="sic"><Icon id={ICON[def.icon] ?? "i-box"} /></span>
        <div className="sp" title={(n.name ?? def.name) + ": " + summary(n)}><span className="snm" id={n.id + "-name"}>{n.name ?? def.name}</span> <span className="ssum">{summary(n)}</span></div>
        {mine.length > 0 && <span className="pill bad"><Icon id="i-alert" />{mine.length === 1 ? "1 problem" : mine.length + " problems"}</span>}
        {end ? <span className="muted sm nowrap">{end === "first" ? "Always first" : "Always last"}</span>
          : otherwise && otherwise.check ? <label className="row nw otherwise"><span className="muted nowrap">Otherwise</span>
              {/* FB4b-3: a question before the save: nothing on its No keeps the original, so it reads "leave the file alone" */}
              <select className="input" aria-label={"Otherwise: what happens when " + nm + " answers No"} value={otherwise.kind === "none" ? "leave" : otherwise.kind}
                onChange={(e) => otherwise.set(e.target.value as ElseKind)}>
                {OTHERWISE_CHECK.filter(([k]) => k !== "other" || otherwise.kind === "other").map(([k, l]) => <option key={k} value={k}>{l}</option>)}
                {otherwise.kind !== "other" && <option value="other">another path (in the graph)</option>}
              </select></label>
          : otherwise && afterOnly(def) ? <label className="row nw otherwise"><span className="muted nowrap">Otherwise</span>
              {/* ui-shop review #582: a No that holds anything but nothing (a fail or a hand-over the graph built, another chain) reads
                  "another path", never "nothing more"; "leave" means the same as nothing after the save */}
              <select className="input" aria-label={"Otherwise: what happens when " + nm + " answers No"} value={otherwise.kind === "none" || otherwise.kind === "leave" ? "none" : "other"}
                onChange={(e) => otherwise.set(e.target.value as ElseKind)}>
                {OTHERWISE_AFTER.filter(([k]) => k !== "other" || (otherwise.kind !== "none" && otherwise.kind !== "leave")).map(([k, l]) => <option key={k} value={k}>{l}</option>)}
                {(otherwise.kind === "none" || otherwise.kind === "leave") && <option value="other">another path (in the graph)</option>}
              </select></label>
          : otherwise ? <label className="row nw otherwise"><span className="muted nowrap">Otherwise</span>
              <select className="input" aria-label={"Otherwise: what happens when " + nm + " answers No"} value={otherwise.kind === "none" ? "leave" : otherwise.kind}
                onChange={(e) => otherwise.set(e.target.value as ElseKind)}>
                {OTHERWISE.filter(([k]) => k !== "other" || otherwise.kind === "other").map(([k, l]) => <option key={k} value={k}>{l}</option>)}
                {otherwise.kind !== "other" && <option value="other">another path (in the graph)</option>}
              </select></label>
          : def.alwaysOn ? <span className="muted sm nowrap">Always on</span>
          : <>
            <input type="checkbox" role="switch" className="switch" aria-label={(def.name) + " on"} checked={n.enabled} onChange={(e) => update({ ...n, enabled: e.target.checked })} /></>}
        {!end && !inline && <details className="menu" onKeyDown={menuEscape}>
          <summary className="btn icon ghost" id={n.id + "-more"} aria-label={"More for " + nm}><Icon id="i-more" /></summary>
          <div className="pop" role="menu">
            <button className="it" type="button" role="menuitem" aria-disabled={mv.up ? undefined : true} onClick={(e) => { closeMenuToSummary(e.currentTarget); if (mv.up) mv.act("up"); }}>Move up</button>
            <button className="it" type="button" role="menuitem" aria-disabled={mv.down ? undefined : true} onClick={(e) => { closeMenuToSummary(e.currentTarget); if (mv.down) mv.act("down"); }}>Move down</button>
            <button className="it" type="button" role="menuitem" aria-disabled={mv.del ? undefined : true} onClick={(e) => { closeMenuToSummary(e.currentTarget); if (mv.del) mv.act("del"); }}>Delete</button>
          </div></details>}
        {hasOptions && !inline ? <button className="btn icon ghost" type="button" aria-expanded={open} aria-controls={n.id + "-opts"} aria-label={"Options for " + def.name} onClick={toggle}><Icon id={open ? "i-chev-d" : "i-chev-r"} /></button>
          : <span className="chev-sp" />}
      </div>
      {otherwise?.kind === "goto" && otherwise.node && otherwise.update && (() => { const g = otherwise.node, put = otherwise.update, bad = issues.filter((x) => x.nodeId === g.id); return (
        <div className="otherwise-goto" data-node={g.id}>
          <WorkflowPick fid={g.id + "-workflow"} title={"When " + nm + " answers No, hand the file to"} value={String(g.config.workflow ?? "")} set={(v) => put({ ...g, config: { ...g.config, workflow: v } })} wf={wf} description={stepDef("flow.goto")?.description} />
          {bad.map((x, k) => <span key={k} className="hint" role="alert" style={{ color: "var(--error-text)" }}>{x.message}</span>)}
        </div>); })()}
      {/* a step with no options has no sheet: its refused sentence goes here (design item 3); with options it sits in the sheet */}
      {open && !hasOptions && refused}
      {open && hasOptions && <div className={"sopts" + (inline ? " inline" : "")} id={n.id + "-opts"} role="group" aria-label={"Options for " + def.name} onKeyDown={(e) => !inline && e.key === "Escape" && toggle()}>
        {!inline && <div className="sheethd phone"><button ref={back} className="btn icon ghost" type="button" aria-label="Back to the steps" onClick={toggle}><Icon id="i-chev-l" /></button><strong>{def.name}</strong></div>}
        {refused}
        <Options n={n} issues={issues} set={(k, v, more) => update({ ...n, config: { ...n.config, [k]: v, ...more } })} />
        {errSaved && <p className="hint">If it doesn't work: a warning, and the next steps still run. <Link to={graphHref ?? ""}>Add another path in the graph</Link></p>}</div>}
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
      const line = cond ? (a?.addable ? (afterOnly(d) ? "Only if it answers Yes; otherwise nothing more happens. Goes after " : "Only if it answers Yes; otherwise the file is left alone. Goes after ") + nameOf(a.after) + (afterOnly(d) && d.phase?.includes("check") ? ". Move it above 'Save the result' to keep the original when it answers No" : "") : "Splits files by a rule. Opens the graph.")
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
  if (meta.error?.code === "not_found" || (meta.loaded && !meta.data)) return <div className="stack"><PageHead icon="i-workflow" crumbs={[["Workflows", "/workflows"]]} title="Workflow not found" /><div className="card"><div className="bd">There is no such workflow. It may have been deleted.</div></div></div>;
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
  const [gone, setGone] = useState<{ text: string; at: number; undo?: string } | null>(null), [hold, setHold] = useState(false), [addOpen, setAddOpen] = useState(false);
  // the step a handle has lifted (its order is a copy until the drop), and the history step the last move, drop or delete made
  const [lift, setLift] = useState<{ id: ID; doc: WorkflowDoc } | null>(null), [orderAt, setOrderAt] = useState<number | null>(null);
  const opener = useRef<HTMLElement | null>(null);
  const doc = hist.docs[hist.at], dirty = doc !== saved;
  const all = useData(listLibraries, []), libs = (all.data ?? []).filter((l) => l.flowId === id).map((l) => l.path);
  // {{name}} fields with a bad name (or an unclosed {{): Publish waits for them, once the variable list has loaded
  // the list is needed for the picker (a step takes variables) and for the Variables card's add form (it shows with own variables too)
  const tv = takesVars(defs), needVars = tv || (doc.variables ?? []).length > 0, vl = useData(() => varListFor(id, needVars), [id, needVars]).data, varBad = vl ? varProblems(doc.nodes, defs, withOwn(vl.variables, doc.variables ?? [])) : [];
  // The server's words and problems for the document as it is now (nothing saved): asked 300 ms after a change.
  // Export: the draft as workflow-<id>.json, saved the way a job log is (Blob + download). A failure is an alert.
  const [exportErr, setExportErr] = useState(""), [exportNote, setExportNote] = useState("");
  const exportFile = async () => {
    setExportErr(""); setExportNote("");
    const r = await act(exportWorkflow(id), setExportErr);
    if (!r) return;
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([JSON.stringify(r, null, 2)], { type: "application/json" }));
    a.download = "workflow-" + id + ".json";
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    // the file holds the saved draft only: no autosave here, so say so when the page has edits it can't see
    if (dirty) setExportNote("Exported the last saved draft. Your unsaved changes aren't in the file.");
  };
  useEffect(() => setExportNote(""), [doc]); // the note is about the file just downloaded: an edit makes it stale
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
    edit(d); setOrderAt(hist.at + 1); setSaid(`Moved ${nameOf(now[k])} to step ${k + 1} of ${now.length}.`);
  };
  const deleteStep = (nid: ID) => {
    const before = order(doc), i = before.findIndex((x) => x.id === nid), prev = before[i - 1], nxt = before[i + 1], gone0 = before[i];
    edit(deleteNode(doc, nid)); setOrderAt(hist.at + 1);
    setGone({ text: prev && nxt ? `Deleted ${nameOf(gone0)}. ${nameOf(prev)} now goes straight to ${nameOf(nxt)}.` : `Deleted ${nameOf(gone0)}.`, at: hist.at + 1 });
    // focus goes to the next row's handle, or the previous row's when it was the last (never the input or the save row)
    const to = [nxt, prev].find((x) => x && x.type !== "input.library" && x.type !== "output.commit");
    if (to) requestAnimationFrame(() => document.getElementById(to.id + "-grip")?.focus());
  };
  // W11 keyboard moves on the handle: Space or Enter picks the step up, the arrows move it on a copy of the document,
  // Space or Enter drops it (one history step, then the check answers), Escape or Tab puts it back; Home and End go to the limits
  const movableEnd = (d: WorkflowDoc) => { const l = order(d); return l.length - 1 - (l[l.length - 1]?.type === "output.commit" ? 1 : 0); };
  const placeOf = (d: WorkflowDoc, nid: ID) => order(d).findIndex((x) => x.id === nid);
  const focusGrip = (nid: ID) => requestAnimationFrame(() => document.getElementById(nid + "-grip")?.focus());
  const pick = (nid: ID) => { setLift({ id: nid, doc }); const l = order(doc), k = placeOf(doc, nid); setSaid(`${nameOf(l[k])}, picked up. Step ${k + 1} of ${l.length}.`); };
  const stepWord = (d: WorkflowDoc, nid: ID) => { const l = order(d), k = placeOf(d, nid); return `Step ${k + 1} of ${l.length}, after ${nameOf(l[k - 1])}.`; };
  const nudge = (dir: -1 | 1) => {
    if (!lift) return;
    const cur = lift.doc, k = placeOf(cur, lift.id), l = order(cur);
    if (k + dir < 1 || k + dir > movableEnd(cur)) { // at a limit: say so, and don't move
      return setSaid(dir < 0 ? `${nameOf(l[k])} can't go higher. ${nameOf(l[0])} is always first.` : `${nameOf(l[k])} can't go lower. ${nameOf(l[movableEnd(cur) + 1])} is always last.`);
    }
    const d2 = moveNode(cur, lift.id, dir);
    setLift({ id: lift.id, doc: d2 }); setSaid(stepWord(d2, lift.id)); focusGrip(lift.id); // the row's node moves in the DOM, which drops focus
  };
  const jump = (dir: -1 | 1) => {
    if (!lift) return;
    let d = lift.doc;
    for (;;) { const k = placeOf(d, lift.id); if (k + dir < 1 || k + dir > movableEnd(d)) break; d = moveNode(d, lift.id, dir); }
    setLift({ id: lift.id, doc: d }); setSaid(stepWord(d, lift.id)); focusGrip(lift.id);
  };
  const drop = () => {
    if (!lift) return;
    const { id, doc: d } = lift; setLift(null);
    const l = order(d), k = placeOf(d, id);
    if (dropResult(doc, d).moved) { edit(d); setOrderAt(hist.at + 1); setSaid(`${nameOf(l[k])} moved to step ${k + 1} of ${l.length}.`); }
    else setSaid(`${nameOf(l[k])} stays at step ${k + 1} of ${l.length}.`);
    focusGrip(id);
  };
  // refocus: Escape and a pointer cancel put focus back on the handle; Tab leaves it to the browser (one Tab press leaves)
  const cancel = (refocus = true) => {
    if (!lift) return;
    const id = lift.id; setLift(null);
    const l = order(doc), k = placeOf(doc, id); setSaid(`Move cancelled. ${nameOf(l[k])} is step ${k + 1} of ${l.length}.`); if (refocus) focusGrip(id);
  };
  const gripKey = (nid: ID) => (e: { key: string; preventDefault: () => void }) => {
    if (lift?.id !== nid) { if (e.key === " " || e.key === "Enter") { e.preventDefault(); pick(nid); } return; }
    if (e.key === "ArrowUp" || e.key === "ArrowDown") { e.preventDefault(); nudge(e.key === "ArrowUp" ? -1 : 1); }
    else if (e.key === "Home" || e.key === "End") { e.preventDefault(); jump(e.key === "Home" ? -1 : 1); }
    else if (e.key === " " || e.key === "Enter") { e.preventDefault(); drop(); }
    else if (e.key === "Escape") { e.preventDefault(); cancel(); }
    else if (e.key === "Tab") cancel(false);
  };
  // pointer drag on the handle: the press picks the step up, each pointer move crossing a neighbour's middle moves it one place
  // (the same copy and limits as the keys), and the release drops it; a cancelled press puts it back
  // a touch press lifts only after the hold (touchHold.ts); a move or release before that cancels it. Mouse and pen lift at once.
  const [press] = useState(() => touchPress());
  useEffect(() => () => press.dispose(), [press]); // a hold still pending when the stack unmounts must not lift a step
  // the pointer whose press is dragging a step: the window follows only this pointer, so a keyboard lift ignores the mouse
  const dragPointer = useRef<number | null>(null);
  const dragFrom = (nid: ID) => ({
    down: (e: DragEv) => {
      if (e.button !== undefined && e.button !== 0) return;
      dragPointer.current = e.pointerId;
      if (e.pointerType === "touch") {
        const el = e.currentTarget, pid = e.pointerId;
        press.down(e.clientX, e.clientY, () => { pick(nid); try { el.setPointerCapture(pid); } catch { /* the page still follows the release */ } });
        return;
      }
      e.preventDefault(); if (lift?.id !== nid) pick(nid);
      try { e.currentTarget.setPointerCapture(e.pointerId); } catch { /* the page still follows the release */ }
    },
    cancel: () => { dragPointer.current = null; press.cancel(); if (lift) cancel(); },
  });
  // while a step is lifted, its moves and release are read on the window: moving the row drops the handle's pointer capture,
  // so the handle can miss the release and the step stayed lifted (#707). The handle keeps the press and the cancel.
  const dragNow = useRef({ move: (_e: DragMove) => {}, up: (_e: DragMove) => {} });
  dragNow.current = {
    move: (e) => {
      if (dragPointer.current !== e.pointerId) return;
      if (press.move(e.clientX, e.clientY)) return;
      if (!lift) return;
      const nid = lift.id, li = document.getElementById(nid + "-grip")?.closest("li") ?? null;
      const prev = li?.previousElementSibling as HTMLElement | null, next = li?.nextElementSibling as HTMLElement | null;
      const k = placeOf(lift.doc, nid);
      if (prev?.dataset.sid && k - 1 >= 1 && e.clientY < prev.getBoundingClientRect().top + prev.offsetHeight / 2) nudge(-1);
      else if (next?.dataset.sid && k + 1 <= movableEnd(lift.doc) && e.clientY > next.getBoundingClientRect().top + next.offsetHeight / 2) nudge(1);
    },
    up: (e) => { if (dragPointer.current !== e.pointerId) return; dragPointer.current = null; if (press.up()) return; if (lift) drop(); },
  };
  useEffect(() => {
    const move = (e: PointerEvent) => dragNow.current.move(e), up = (e: PointerEvent) => dragNow.current.up(e);
    window.addEventListener("pointermove", move); window.addEventListener("pointerup", up);
    return () => { window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", up); };
  }, []);
  // a refused order: the check's one not_runnable_yet issue, on the step that's out of place. After a move, drop or delete
  // its sentence goes in the status bar once (Undo move takes that step back); the row keeps the problem pill until the order runs
  const refusals = live.issues.filter((i) => i.code === "not_runnable_yet"), refusal = orderAt === hist.at ? refusals[0] : undefined;
  useEffect(() => {
    if (!refusal) return setGone((g) => (g?.undo === "Undo move" ? null : g));
    setGone({ text: refusalText(refusal.message), at: hist.at, undo: "Undo move" });
  }, [refusal?.message, hist.at]); // eslint-disable-line react-hooks/exhaustive-deps
  const undoGone = () => { if (gone) setHist({ ...hist, at: gone.at - 1 }); setGone(null); setSaid("Undone."); };
  const endDef = defs.find((x) => x.type === "flow.end"), failDef = defs.find((x) => x.type === "flow.fail"), gotoDef = defs.find((x) => x.type === "flow.goto");
  // ui-shop review #579: a hand-over's picked workflow is remembered through a switch of Otherwise and back, so an
  // accidental change doesn't empty the picker (the end step is rebuilt from its defaults by setOtherwise)
  const lastGoto = useRef<Record<ID, Record<string, unknown>>>({});
  const setElse = (q: Node, k: ElseKind) => {
    if (k === "other") { navigate(`/workflows/${id}/graph?node=${encodeURIComponent(q.id)}`); return; }
    if (k === "none") return; // FB4b: after the save, "nothing more" is the No port left alone, which it already is
    const def = k === "leave" ? endDef : k === "fail" ? failDef : gotoDef;
    if (!def) return setSaid("This server has no such step.");
    const cur = otherwiseOf(doc, q);
    const picked = (cfg: Record<string, unknown>) => Object.values(cfg).some((v) => v !== undefined && v !== ""); // ui-shop nit #582: a cleared pick is no pick
    if (cur.kind === "goto" && cur.node && picked(cur.node.config)) lastGoto.current[q.id] = cur.node.config;
    else if (cur.kind === "goto") delete lastGoto.current[q.id];
    let next = setOtherwise(doc, q, def);
    const remembered = lastGoto.current[q.id], made = k === "goto" && next !== doc ? otherwiseOf(next, q).node : undefined;
    if (made && remembered && !picked(made.config)) next = { ...next, nodes: next.nodes.map((n) => (n.id === made.id ? { ...n, config: { ...remembered } } : n)) };
    if (next !== doc) { edit(next); setSaid(`${nameOf(q)}: when the answer is No, ${k === "leave" ? "the file is left alone" : k === "fail" ? "the run stops as failed" : made && remembered ? "the file is handed to another workflow, the one picked before" : "the file is handed to another workflow: choose which, under the step"}.`); }
  };
  const addStep = (d: StepDefinition, after?: ID) => {
    const question = d.outputs.some((o) => o.outcome === "branch"), afterSave = afterOnly(d); // FB4b: a question about the result goes after the save, nothing on its No
    if (question && !afterSave && (!live.addSteps?.[d.type]?.addable || !endDef)) { navigate(`/workflows/${id}/graph?add=${encodeURIComponent(d.type)}`); return; }
    // a draft with no Library files step gets one first (the page adds it itself), so the chain has a start and the new step shows
    const inDef = defs.find((x) => x.type === "input.library"), start = inDef && !doc.nodes.some((n) => n.type === "input.library") ? { ...doc, nodes: [...doc.nodes, newNode(inDef, "n" + Math.random().toString(36).slice(2, 8))] } : doc;
    const nid = "n" + Math.random().toString(36).slice(2, 8), next = afterSave ? insertAfterSave(start, after, newNode(d, nid)) : question ? insertQuestion(start, after, newNode(d, nid), endDef!) : insertAfter(start, after, newNode(d, nid)), now = order(next);
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
  // U11 (ui-review C10): the steps view shares the graph's toolbar: undo/redo beside the view switch in the middle,
  // Check and Save draft on the right beside the one primary, Publish
  const undoRedo = <>
    <button className="btn sm icon ghost" type="button" title="Undo" aria-label="Undo" disabled={hist.at === 0} onClick={() => setHist({ ...hist, at: hist.at - 1 })}><Icon id="i-undo" /></button>
    <button className="btn sm icon ghost" type="button" title="Redo" aria-label="Redo" disabled={hist.at === hist.docs.length - 1} onClick={() => setHist({ ...hist, at: hist.at + 1 })}><Icon id="i-redo" /></button>
  </>;
  const tools = <>
    <button className="btn sm ghost" type="button" onClick={async () => { if (await ask(doc)) setChecked(true); }}><Icon id="i-checks" />Check</button>
    <button className="btn sm" type="button" disabled={!dirty} onClick={save}><Icon id="i-save" />Save draft</button>
  </>;
  const canStack = live.stackable;
  const view = lift ? lift.doc : doc; // a lifted step's order is the copy being moved; the rest of the page reads the saved draft
  const rowIssues = [...issues, ...refusals.filter((r) => !issues.includes(r))];
  const list = order(view), total = list.length, tailOut = list[total - 1]?.type === "output.commit", lastMovable = total - 1 - (tailOut ? 1 : 0), only = total <= 1;
  // FB3 (flow-builder.md 4.3): the simple view is this list filtered to the cards that shape the one conversion (the
  // steps that convert, check and save), each with its form on the page; the questions and actions before them and
  // the steps after the save fold into one line each, which opens the steps view. Same document, same checks.
  // read through useSearch so a search-only navigate (Simple, Steps, Back, Forward) re-renders the page (F2)
  const simple = new URLSearchParams(useSearch()).get("view") === "simple";
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
    <div className="editor stackpage">
      {/* U11 (flow-builder.md 4.2, ui-review C10): the steps view is the same workspace as the graph: a 48 px toolbar
          (name, status, the view switch and undo/redo in the middle, Check, Save draft and the one primary on the
          right), then a frame that scrolls inside the window */}
      <FlowTabs id={id} on="advanced" />
      <div className="edbar" role="toolbar" aria-label={"Editing " + w.name}>
        <span className="muted crumb"><Link to="/workflows">Workflows</Link> /</span>
        {renaming ? <form className="row" onSubmit={(e) => { e.preventDefault(); const v = (e.currentTarget.elements.namedItem("nm") as HTMLInputElement).value.trim(); if (v) rename(v); else setRenaming(false); }}>
            <label className="sr" htmlFor="wf-rename">Name</label><input id="wf-rename" name="nm" className="input" defaultValue={w.name} autoFocus style={{ width: "auto" }} />
            <button className="btn sm" type="submit">Rename</button><button className="btn sm ghost" type="button" onClick={() => setRenaming(false)}>Cancel</button></form>
          : <><h1 className="nm">{w.name}</h1><button className="btn sm ghost" type="button" onClick={() => setRenaming(true)} aria-label={"Rename " + w.name}>Rename</button></>}
        <span className="pill">{w.version == null || w.draftDiffers || dirty ? "Draft" : "Published"}</span>
        <span className="muted nowrap desk edver">{w.version == null ? "Not published yet" : "v" + w.version + " published" + (w.draftDiffers || dirty ? " · v" + (w.version + 1) + " draft" : "")}</span>
        <div className="mid">
          {canStack && <><span className="vr" aria-hidden="true" />{undoRedo}</>}
        </div>
        {canStack && tools}
        <button className="btn sm ghost" type="button" onClick={exportFile}>Export</button>
        <span role="status" className={exportNote ? "muted" : "sr"}>{exportNote}</span>{/* always mounted for the announcement; visually hidden when empty */}
        {canStack && <button className="btn sm primary" type="button" disabled={varBad.length > 0} title={varBad.length ? "Fix the variable names first" : undefined} onClick={openPublish}>Publish</button>}
      </div>
      <div className="edframe stackframe"><div className="stackbody">
      {canStack && (checked || !dirty) && (issues.length === 0 ? <p className="st ok checkline"><Icon id="i-check-c" /><span>Check: no problems</span></p>
        : <p className="st bad checkline"><Icon id="i-alert" /><span>Check: {issues.length === 1 ? "1 problem" : issues.length + " problems"}. Publish waits until {issues.length === 1 ? "it's" : "they're"} fixed. <button className="linkbtn" type="button" onClick={() => setOpen(issues[0].nodeId ?? null)}>Show</button></span></p>)}
      {canStack && varBad.length > 0 && <p className="st bad" role="alert"><Icon id="i-alert" /><span>Fix {varBad.length === 1 ? "1 field" : varBad.length + " fields"} before publishing. <button className="linkbtn" type="button" onClick={() => setOpen(varBad[0].nodeId)}>{varBad[0].title}</button></span></p>}
      <p className="muted" role="status">{said}</p>
      {exportErr && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{exportErr}</span></p>}

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
        {!simple && <p id="order-help" className="sr">Press Space or Enter on a step's handle to pick it up. Up and down arrows move it, Enter drops it, Escape puts it back.</p>}
        <ol className="steps">{simple && fold(before, "before converting")}{shown.map((n) => {
          const i = list.indexOf(n);
          // moves stay inside the first and last steps (the server's order rules are its answer on the check, not copied here)
          const mv: Move = { up: !simple && i > 1 && i <= lastMovable, down: !simple && i >= 1 && i < lastMovable, del: !simple && i >= 1 && i <= lastMovable, // the simple view shows no place badge or menu: moving and deleting live in the steps view (ui-shop review #543)
            act: (w) => (w === "del" ? deleteStep(n.id) : moveStep(n.id, w === "up" ? -1 : 1)),
            grip: simple ? undefined : { label: `Move ${nameOf(n)}, step ${i + 1} of ${total}`, lifted: lift?.id === n.id, onKey: gripKey(n.id), drag: dragFrom(n.id) } };
          const ow = isQuestion(n) ? otherwiseOf(doc, n) : undefined;
          const otherwise: Else | undefined = ow ? { kind: ow.kind, set: (k) => setElse(n, k), node: ow.node, update, check: beforeSave(doc, n, defs.find((d) => d.type === n.type)) } : undefined;
          return <Step key={n.id} n={n} i={i} mv={mv} open={open === n.id} toggle={() => setOpen(open === n.id ? null : n.id)} update={update} issues={rowIssues} otherwise={otherwise} inline={simple} graphHref={`/workflows/${id}/graph?node=${encodeURIComponent(n.id)}`} />;
        })}{simple && fold(after, "after the file is saved")}</ol>
        {!only && !simple && <button className="btn sm ghost" type="button" onClick={openAdd}><Icon id="i-plus" />Add step</button>}
        {addOpen && <AddStepMenu wf={id} doc={doc} defs={defs} adds={live.addSteps} onAdd={addStep} onClose={closeAdd} />}
        {/* one status region, always mounted: a delete fills its text, so screen readers announce it */}
        <p className="sr" role="status" aria-live="polite">{gone && hist.at === gone.at ? gone.text : ""}</p>
        {gone && hist.at === gone.at && <div className="note" onPointerEnter={() => setHold(true)} onPointerLeave={() => setHold(false)} onFocus={() => setHold(true)} onBlur={() => setHold(false)}>
          <Icon id="i-undo" /><span className="sp">{gone.text}</span><button className="btn sm ghost" type="button" onClick={undoGone}>{gone.undo ?? "Undo"}</button></div>}
      </> : <ReadOnly doc={doc} id={id} />}
      </div></div>
    </div>
    </OwnVars.Provider>
    </Reg.Provider>);
}
