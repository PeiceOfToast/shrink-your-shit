// A text field that takes {{name}} variables (design-direction-job-log-variables section 3; a step field with
// "x-variables": true). It shows the hint with Insert variable, a live example from the sample file, the errors for an
// unknown name or an unclosed {{, and the picker: it opens when {{ is typed or from Insert variable. A popover under the
// field on desktop, a bottom sheet on phones (from Insert variable only, so it never covers the field being typed in).
// The names come from getVariables; the page checks them itself, with no request per keystroke.
import { createContext, useContext, useEffect, useId, useRef, useState, type ChangeEvent, type KeyboardEvent, type ReactElement } from "react";
import { Icon } from "./Icon";
import { useData } from "./useData";
import { getVariables } from "./workflowApi";
import type { TemplateVar, VarList } from "./mock/variables";
import type { Variable } from "./types/v2";
import "./styles/varpicker.css";

const TOKEN = /\{\{([^{}]*)\}\}/g;
const phone = () => typeof matchMedia === "function" && matchMedia("(max-width:640px)").matches;

// The closest name, when one is within two edits of the typed one (for "Did you mean").
function closest(name: string, names: string[]): string | undefined {
  const dist = (a: string, b: string) => {
    let prev = Array.from({ length: b.length + 1 }, (_, j) => j);
    for (let i = 1; i <= a.length; i++) {
      const cur = [i];
      for (let j = 1; j <= b.length; j++) cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
      prev = cur;
    }
    return prev[b.length];
  };
  let best: string | undefined, bestD = 3;
  for (const n of names) { const d = dist(name, n); if (d < bestD) { bestD = d; best = n; } }
  return best;
}

// The example, the empty-value notes and the errors for one value. Unknown names stay as typed in the example.
// Until the list has loaded (or when it failed) vars is null: names are not checked, only an unclosed {{ is.
function analyse(value: string, vars: TemplateVar[] | null) {
  const list = vars ?? [], byName = new Map(list.map((v) => [v.name, v] as const)), names = list.map((v) => v.name);
  const errors: string[] = [], unknown: string[] = [], empty = new Set<string>();
  const example = value.replace(TOKEN, (all, raw: string) => {
    const name = raw.trim(), v = byName.get(name);
    if (!v) { if (!vars) return all; unknown.push(name); const sug = closest(name, names); errors.push(`{{${name}}} isn't a variable.` + (sug ? ` Did you mean {{${sug}}}?` : "")); return all; }
    if (v.value == null || v.value === "") { empty.add(name); return ""; }
    return v.value;
  });
  const open = value.lastIndexOf("{{");
  if (open >= 0 && value.indexOf("}}", open) < 0) errors.push("A {{ is missing its }}.");
  return { example, errors, empty: [...empty], unknown };
}

// The problems of every {{name}} field in a workflow, for the Publish gate. Only once the list has loaded (no names
// are checked before, so nothing is blocked by a missing list).
// Does any step take {{name}} variables? Only then the editor asks for the variable list (a server without them gets no request).
export const takesVars = (defs: { configSchema: { properties?: Record<string, unknown> } }[]) =>
  defs.some((d) => Object.values(d.configSchema.properties ?? {}).some((s) => (s as { "x-variables"?: unknown })["x-variables"] === true));
const noVars: VarList = { sample: { path: "", builtIn: true }, variables: [] };
export const varListFor = (wf: string, on: boolean) => (on ? getVariables(wf) : Promise.resolve(noVars));
export function varProblems(nodes: { id: string; type: string; config?: Record<string, unknown> }[], defs: { type: string; configSchema: { properties?: Record<string, unknown> } }[], vars: TemplateVar[]) {
  const out: { nodeId: string; field: string; title: string; code: "unknown_variable" | "unclosed_brace"; name: string; error: string }[] = [];
  for (const n of nodes) {
    const props = (defs.find((d) => d.type === n.type)?.configSchema.properties ?? {}) as Record<string, { "x-variables"?: unknown; title?: unknown }>;
    for (const [k, s] of Object.entries(props)) {
      if (s["x-variables"] !== true) continue;
      const a = analyse(String(n.config?.[k] ?? ""), vars), first = a.errors[0];
      // errors[0] is an unknown-name error whenever there is one (analyse pushes those before the brace check)
      if (first) out.push({ nodeId: n.id, field: k, title: (s.title as string) ?? k, code: a.unknown.length ? "unknown_variable" : "unclosed_brace", name: a.unknown[0] ?? "", error: first });
    }
  }
  return out;
}

// The workflow's own variables (its document's variables list). They come first in the picker, under "Your variables",
// and join the Publish check. A name that is already a built-in keeps the built-in.
export const OwnVars = createContext<Variable[]>([]);
const GROUP_LABEL: Record<string, string> = { Workflow: "Your variables" };
export function withOwn(builtins: TemplateVar[], own: Variable[]): TemplateVar[] {
  const names = new Set(builtins.map((v) => v.name));
  const mine = own.filter((v) => !names.has(v.name)).map((v): TemplateVar => ({ name: v.name, description: v.description ?? "", group: "Workflow", value: v.value == null ? null : String(v.value), fields: null }));
  return [...mine, ...builtins];
}

// The {{ being typed before the caret: where it starts and what follows it (names only, no spaces or braces).
function typedAt(value: string, caret: number): { start: number; query: string } | null {
  const before = value.slice(0, caret), p = before.lastIndexOf("{{");
  if (p < 0) return null;
  const after = before.slice(p + 2);
  return /^[A-Za-z0-9_]*$/.test(after) ? { start: p, query: after } : null;
}

type Pick = { mode: "typed" | "button"; start: number; end: number; query: string; active: number };

export function VarInput({ id, title, value, wf, set, description }: { id: string; title: string; value: string; wf: string; set: (v: string) => void; description?: string }) {
  const list = useData<VarList>(() => getVariables(wf), [wf]).data;
  const own = useContext(OwnVars);
  const uid = useId(), descId = id + "-desc";
  const [pick, setPick] = useState<Pick | null>(null);
  const [openKey, setOpenKey] = useState(0); // bumps on each Insert variable, to move focus to the search box
  const wrap = useRef<HTMLDivElement>(null), field = useRef<HTMLInputElement>(null), search = useRef<HTMLInputElement>(null), btn = useRef<HTMLButtonElement>(null);
  const caretAfter = useRef<number | null>(null);

  // after an insert, the caret goes just past the inserted name
  useEffect(() => {
    if (caretAfter.current != null && field.current) { field.current.setSelectionRange(caretAfter.current, caretAfter.current); caretAfter.current = null; }
  }, [value]);
  useEffect(() => { if (pick?.mode === "button") search.current?.focus(); }, [openKey]); // eslint-disable-line react-hooks/exhaustive-deps
  // a click anywhere else closes the picker
  useEffect(() => {
    if (!pick) return;
    const away = (e: MouseEvent) => { if (!wrap.current?.contains(e.target as Node)) setPick(null); };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [pick]);

  const vars = withOwn(list?.variables ?? [], own);
  const a = analyse(value, list ? vars : null);
  const q = (pick?.query ?? "").toLowerCase();
  // groups keep their order (the first name of each group sets its place); within a group, "starts with" comes first
  const rank = new Map<string, number>();
  vars.forEach((v) => rank.has(v.group) || rank.set(v.group, rank.size));
  const items = vars
    .filter((v) => !q || v.name.includes(q) || v.description.toLowerCase().includes(q))
    .sort((x, y) => rank.get(x.group)! - rank.get(y.group)! || Number(!x.name.startsWith(q)) - Number(!y.name.startsWith(q)));
  const active = pick ? Math.min(pick.active, Math.max(0, items.length - 1)) : 0;
  const optId = (i: number) => uid + "-o" + i;
  const sample = list ? (list.sample.builtIn ? "a sample file" : list.sample.path.split("/").pop()) : "";

  const insert = (name: string) => {
    if (!pick) return;
    const token = "{{" + name + "}}";
    set(value.slice(0, pick.start) + token + value.slice(pick.end));
    caretAfter.current = pick.start + token.length;
    setPick(null);
    field.current?.focus(); // focus goes back to the field, the caret after the name
  };
  const close = () => {
    const wasButton = pick?.mode === "button";
    setPick(null);
    if (wasButton) btn.current?.focus();
  };
  // keys shared by the field (typed) and the search box (from the button); both move through the same list
  const listKeys = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setPick((p) => p && { ...p, active: Math.max(0, Math.min(items.length - 1, active + (e.key === "ArrowDown" ? 1 : -1))) });
      return true;
    }
    if ((e.key === "Enter" || (e.key === "Tab" && pick?.mode === "typed")) && pick && items[active]) { e.preventDefault(); insert(items[active].name); return true; }
    if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); return true; }
    return false;
  };
  const onFieldKey = (e: KeyboardEvent<HTMLInputElement>) => { if (pick?.mode === "typed") listKeys(e); };
  // on a phone the sheet is modal: Tab moves between its search box and Close, and never leaves the sheet
  const trap = (e: KeyboardEvent<HTMLDivElement>) => {
    const close = e.currentTarget.querySelector<HTMLButtonElement>(".vp-close");
    if (e.key !== "Tab" || !search.current || !close) return;
    e.preventDefault();
    (document.activeElement === search.current ? close : search.current).focus();
  };
  const onFieldChange = (e: ChangeEvent<HTMLInputElement>) => {
    const nv = e.target.value, caret = e.target.selectionStart ?? nv.length, t = typedAt(nv, caret);
    set(nv);
    // typing moves the caret, so an open popover (from the button) closes rather than insert at a stale position
    if (t && list) setPick({ mode: "typed", start: t.start, end: caret, query: t.query, active: 0 });
    else if (pick) setPick(null);
  };
  const openFromButton = () => {
    const end = field.current?.selectionStart ?? value.length;
    // on a phone the sheet covers the bottom 60%: bring the field to the top first so the sheet never covers it
    if (phone()) field.current?.scrollIntoView({ block: "start" });
    setPick({ mode: "button", start: end, end: field.current?.selectionEnd ?? end, query: "", active: 0 });
    setOpenKey((k) => k + 1);
  };

  const phoneSheet = !!pick && pick.mode === "button" && phone();
  const count = items.length === 1 ? "1 variable" : items.length + " variables";
  const grouped = vars.length > 15;
  const rows: ReactElement[] = [];
  items.forEach((v, i) => {
    if ((grouped || v.group === "Workflow") && (i === 0 || items[i - 1].group !== v.group)) rows.push(<li key={"g" + v.group} role="presentation" className="vp-group">{GROUP_LABEL[v.group] ?? v.group}</li>);
    rows.push(
      <li key={v.name} id={optId(i)} role="option" aria-selected={i === active} className={"vp-row" + (i === active ? " on" : "")}
        onMouseDown={(e) => { e.preventDefault(); insert(v.name); }}>
        <span className="vp-name">{"{{" + v.name + "}}"}</span>
        <span className="vp-desc">{v.description}</span>
        <span className="vp-val muted">{v.value ?? "—"}</span>
      </li>);
  });

  return (
    // Tab out of the popover (or a click elsewhere) closes it, so the insert position can't go stale
    <div className="field vp-wrap" ref={wrap} onBlur={(e) => !wrap.current?.contains(e.relatedTarget as Node) && setPick(null)}>
      <div className="vp-head">
        <label htmlFor={id}>{title}</label>
        <button ref={btn} className="btn sm ghost" type="button" disabled={!list} aria-expanded={pick?.mode === "button"} aria-controls={uid + "-list"} onClick={openFromButton}>
          <Icon id="i-plus" />Insert variable</button>
      </div>
      <input id={id} ref={field} className="input" role="combobox" autoComplete="off" value={value}
        aria-autocomplete="list" aria-expanded={pick?.mode === "typed"} aria-controls={uid + "-list"}
        aria-activedescendant={pick?.mode === "typed" && items[active] ? optId(active) : undefined}
        aria-describedby={descId} onChange={onFieldChange} onKeyDown={onFieldKey} />
      <div id={descId} className="vp-desc-block">
        <span className="hint">{"You can use variables, like {{title}}."}</span>
        {description && <span className="hint">{description}</span>}
        {list && <span className="hint vp-ex">{`Example with ${sample}: ${a.example}`}</span>}
        {a.empty.map((n) => <span key={n} className="hint">{`{{${n}}} is empty for this file.`}</span>)}
        <div aria-live="polite">{(pick?.mode === "typed" ? a.errors.filter((m) => !m.startsWith("A {{ is missing")) : a.errors).map((m, i) => <span key={i}className="hint vp-err" style={{ color: "var(--error-text)" }}><Icon id="i-alert" />{m}</span>)}</div>
      </div>
      {/* always mounted, so the count is announced as it changes (the popover itself comes and goes) */}
      <div className="sr" role="status">{pick && list ? count : ""}</div>
      {pick && list && (
        <div className={"vp " + (phoneSheet ? "vp-sheet" : "vp-pop")} onKeyDown={(e) => {
          // Escape anywhere inside closes and returns focus to the origin (the search box or the Close button)
          if (e.key === "Escape") return (e.preventDefault(), e.stopPropagation(), close());
          if (phoneSheet) trap(e);
        }}>
          {pick.mode === "button" && (
            <div className="vp-search">
              <input ref={search} className="input" role="combobox" aria-label="Search variables" aria-expanded aria-controls={uid + "-list"} autoComplete="off"
                aria-activedescendant={items[active] ? optId(active) : undefined} value={pick.query} placeholder="Search variables"
                onChange={(e) => setPick((p) => p && { ...p, query: e.target.value, active: 0 })} onKeyDown={(e) => listKeys(e)} />
              <button className="btn sm ghost vp-close" type="button" onClick={close}>Close</button>
            </div>)}
          <div className="vp-count" aria-hidden="true">{count}</div>
          {items.length > 0
            ? <ul id={uid + "-list"} role="listbox" aria-label="Variables" className="vp-list">{rows}</ul>
            : <p className="hint vp-none" id={uid + "-list"}>{`No variable matches "${pick.query}".`}</p>}
        </div>)}
    </div>
  );
}
