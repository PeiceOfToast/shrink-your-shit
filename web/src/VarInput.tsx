// A text field that takes {{name}} variables (design-direction-job-log-variables section 3; a step field with
// "x-variables": true). It shows the hint with Insert variable, a live example from the sample file, the errors for an
// unknown name or an unclosed {{, and the picker: it opens when {{ is typed or from Insert variable. A popover under the
// field on desktop, a bottom sheet on phones (from Insert variable only, so it never covers the field being typed in).
// The names come from getVariables; the page checks them itself, with no request per keystroke.
import { useEffect, useId, useRef, useState, type ChangeEvent, type KeyboardEvent, type ReactElement } from "react";
import { Icon } from "./Icon";
import { useData } from "./useData";
import { getVariables } from "./workflowApi";
import type { TemplateVar, VarList } from "./mock/variables";
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
function analyse(value: string, vars: TemplateVar[]) {
  const byName = new Map(vars.map((v) => [v.name, v] as const)), names = vars.map((v) => v.name);
  const errors: string[] = [], empty = new Set<string>();
  const example = value.replace(TOKEN, (all, raw: string) => {
    const name = raw.trim(), v = byName.get(name);
    if (!v) { const sug = closest(name, names); errors.push(`{{${name}}} isn't a variable.` + (sug ? ` Did you mean {{${sug}}}?` : "")); return all; }
    if (v.value == null || v.value === "") { empty.add(name); return ""; }
    return v.value;
  });
  const open = value.lastIndexOf("{{");
  if (open >= 0 && value.indexOf("}}", open) < 0) errors.push("A {{ is missing its }}.");
  return { example, errors, empty: [...empty] };
}

// The {{ being typed before the caret: where it starts and what follows it (names only, no spaces or braces).
function typedAt(value: string, caret: number): { start: number; query: string } | null {
  const before = value.slice(0, caret), p = before.lastIndexOf("{{");
  if (p < 0) return null;
  const after = before.slice(p + 2);
  return /^[A-Za-z0-9_]*$/.test(after) ? { start: p, query: after } : null;
}

type Pick = { mode: "typed" | "button"; start: number; end: number; query: string; active: number };

export function VarInput({ id, title, value, wf, set }: { id: string; title: string; value: string; wf: string; set: (v: string) => void }) {
  const list = useData<VarList>(() => getVariables(wf), [wf]).data;
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

  const vars = list?.variables ?? [];
  const a = analyse(value, vars);
  const q = (pick?.query ?? "").toLowerCase();
  const items = vars
    .filter((v) => !q || v.name.includes(q) || v.description.toLowerCase().includes(q))
    .sort((x, y) => Number(!x.name.startsWith(q)) - Number(!y.name.startsWith(q)));
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
    if (t && list) setPick({ mode: "typed", start: t.start, end: caret, query: t.query, active: 0 });
    else if (pick?.mode === "typed") setPick(null);
  };
  const openFromButton = () => {
    const end = field.current?.selectionStart ?? value.length;
    setPick({ mode: "button", start: end, end: field.current?.selectionEnd ?? end, query: "", active: 0 });
    setOpenKey((k) => k + 1);
  };

  const phoneSheet = !!pick && pick.mode === "button" && phone();
  const count = items.length === 1 ? "1 variable" : items.length + " variables";
  const grouped = vars.length > 15;
  const rows: ReactElement[] = [];
  items.forEach((v, i) => {
    if (grouped && (i === 0 || items[i - 1].group !== v.group)) rows.push(<li key={"g" + v.group} role="presentation" className="vp-group">{v.group}</li>);
    rows.push(
      <li key={v.name} id={optId(i)} role="option" aria-selected={i === active} className={"vp-row" + (i === active ? " on" : "")}
        onMouseDown={(e) => { e.preventDefault(); insert(v.name); }}>
        <span className="vp-name">{"{{" + v.name + "}}"}</span>
        <span className="vp-desc">{v.description}</span>
        <span className="vp-val muted">{v.value ?? "—"}</span>
      </li>);
  });

  return (
    <div className="field vp-wrap" ref={wrap}>
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
        {list && <span className="hint vp-ex">{`Example with ${sample}: ${a.example}`}</span>}
        {a.empty.map((n) => <span key={n} className="hint">{`{{${n}}} is empty for this file.`}</span>)}
        <div aria-live="polite">{a.errors.map((m) => <span key={m} className="hint vp-err" style={{ color: "var(--error-text)" }}><Icon id="i-alert" />{m}</span>)}</div>
      </div>
      {pick && list && (
        <div className={"vp " + (phoneSheet ? "vp-sheet" : "vp-pop")} onKeyDown={phoneSheet ? trap : undefined}>
          {pick.mode === "button" && (
            <div className="vp-search">
              <input ref={search} className="input" role="combobox" aria-label="Search variables" aria-expanded aria-controls={uid + "-list"} autoComplete="off"
                aria-activedescendant={items[active] ? optId(active) : undefined} value={pick.query} placeholder="Search variables"
                onChange={(e) => setPick((p) => p && { ...p, query: e.target.value, active: 0 })} onKeyDown={(e) => listKeys(e)} />
              <button className="btn sm ghost vp-close" type="button" onClick={close}>Close</button>
            </div>)}
          <div className="vp-count" role="status">{count}</div>
          {items.length > 0
            ? <ul id={uid + "-list"} role="listbox" aria-label="Variables" className="vp-list">{rows}</ul>
            : <p className="hint vp-none" id={uid + "-list"}>{`No variable matches "${pick.query}".`}</p>}
        </div>)}
    </div>
  );
}
