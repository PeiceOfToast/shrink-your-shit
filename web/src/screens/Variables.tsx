// Settings > Variables: the server-wide workflow variables (contract: internal/web/workflowvars.go). A step names one as
// {{name}}. Edit the list here; one Save first says what the change touches, then saves the whole list.
import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { failed, type Answer } from "../api";
import { PageHead } from "../PageHead";
import { SettingsTabs } from "../pages";
import { useData } from "../useData";
import { getWorkflowVars, saveWorkflowVars, type VarIssue, type VarKind, type VarSave, type WorkflowVar } from "../workflowVarsApi";

const KINDS: [VarKind, string][] = [["string", "Text"], ["number", "Number"], ["bool", "Yes or no"], ["enum", "Choice"], ["path", "Path"]];
const kindName = (t: string) => KINDS.find(([k]) => k === (t === "boolean" ? "bool" : t))?.[1] ?? t;
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const braces = (name: string) => "{{" + (name.trim() || "…") + "}}";

// One variable as the form edits it: the value as text, the choices as one comma-separated line.
interface Row { key: string; name: string; type: VarKind; text: string; options: string; description: string }
let seq = 0;
const fresh = (): Row => ({ key: "v" + ++seq, name: "", type: "string", text: "", options: "", description: "" });

function toRow(v: WorkflowVar): Row {
  const type = (v.type === "boolean" ? "bool" : v.type) as VarKind;
  const text = v.value == null ? "" : type === "bool" ? (v.value ? "yes" : "no") : String(v.value);
  return { ...fresh(), name: v.name, type, text, options: (v.options ?? []).join(", "), description: v.description ?? "" };
}

// What the server gets. A number that doesn't parse is sent as typed, so the server refuses it in its own words.
function toVar(r: Row): WorkflowVar {
  let value: WorkflowVar["value"] = null;
  if (r.text !== "") {
    if (r.type === "bool") value = r.text === "yes";
    else if (r.type === "number") value = r.text.trim() !== "" && Number.isFinite(Number(r.text)) ? Number(r.text) : r.text;
    else value = r.text;
  }
  const v: WorkflowVar = { name: r.name.trim(), type: r.type, value };
  if (r.type === "enum") v.options = r.options.split(",").map((s) => s.trim()).filter(Boolean);
  if (r.description.trim()) v.description = r.description.trim();
  return v;
}
const sig = (rows: Row[]) => JSON.stringify(rows.map(toVar));
const shownValue = (r: Row) => (r.text === "" ? "No value" : r.type === "bool" ? (r.text === "yes" ? "Yes" : "No") : r.text);

// A refused save names its rows: each issue goes to the row whose name it carries (the second of two duplicates).
type Field = "name" | "kind" | "value" | "options";
type Issues = Record<string, { msg: string; field: Field }>;
const FIELD: Record<string, Field> = { invalid_variable_type: "kind", variable_wrong_type: "value" };
function mapIssues(issues: VarIssue[], rows: Row[]) {
  const out: Issues = {}, loose: string[] = [];
  for (const is of issues) {
    const hits = rows.filter((r) => r.name.trim() === (is.name ?? ""));
    const at = is.code === "duplicate_variable" ? hits.slice(1) : hits;
    if (!at.length) loose.push(is.message);
    for (const r of at) {
      const prev = out[r.key];
      out[r.key] = { msg: prev ? prev.msg + " " + is.message : is.message, field: prev?.field ?? FIELD[is.code] ?? "name" };
    }
  }
  return { out, loose };
}

export function VariablesSettings() {
  const d = useData(getWorkflowVars, []), [said, setSaid] = useState("");
  const notHere = d.error && (d.error.code === "http_404" || d.error.code === "not_found");
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="variables" />
      <p className="muted" role="status" style={{ margin: 0 }}>{said}</p>
      {notHere ? <p className="muted">This server can't keep variables yet.</p>
        : d.error && !d.data ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{d.error.message}</span></p>
          : !d.data ? <div className="empty">Loading…</div>
            // keyed by what the server holds, so a save that lands starts the editor again from the saved list
            : <VariablesCard key={JSON.stringify(d.data.items)} items={d.data.items} reload={d.reload} said={setSaid} />}
    </div>);
}

function VariablesCard({ items, reload, said }: { items: WorkflowVar[]; reload: () => Promise<void>; said: (s: string) => void }) {
  const [saved] = useState<Row[]>(() => items.map(toRow)); // the list as the server holds it; Cancel goes back to these
  const [rows, setRows] = useState<Row[]>(saved);
  const [base] = useState(() => sig(saved));
  const [edit, setEdit] = useState<{ key: string } | null>(null);
  const [asking, setAsking] = useState<string | null>(null);
  const [phase, setPhase] = useState<VarSave["affects"] | null>(null); // the preview, while the person decides
  const [issues, setIssues] = useState<Issues>({});
  const [loose, setLoose] = useState("");
  const [busy, setBusy] = useState(false);
  const [focusId, setFocusId] = useState<string | null>(null);
  useEffect(() => { if (focusId) { document.getElementById(focusId)?.focus(); setFocusId(null); } }, [focusId]);
  const dirty = sig(rows) !== base;

  const clearIssue = (key: string) => setIssues((i) => { const n = { ...i }; delete n[key]; return n; });
  const touch = (key: string, patch: Partial<Row>) => {
    setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)));
    setPhase(null); setLoose(""); clearIssue(key);
  };
  const changeKind = (r: Row, type: VarKind) => touch(r.key, { type, text: type === "bool" || type === "enum" ? "" : r.text });
  const editRow = (r: Row) => { setAsking(null); setEdit({ key: r.key }); setFocusId(`${r.key}-name`); };
  const addRow = () => { const r = fresh(); setRows((rs) => [...rs, r]); setAsking(null); setPhase(null); setEdit({ key: r.key }); setFocusId(`${r.key}-name`); };
  // Done closes the form and returns focus to the row's Edit button; no choices keeps it open with an error.
  // A Choice with no choices can't be saved: the error goes by Choices and focus moves there.
  const noChoices = (r: Row) => r.type === "enum" && !r.options.split(",").some((s) => s.trim());
  const needChoices = (key: string) => {
    const r = rows.find((x) => x.key === key);
    if (!r || !noChoices(r)) return false;
    setEdit({ key }); // open that row's form, so the error and its Choices field are on screen
    setIssues((i) => ({ ...i, [key]: { msg: "Give it at least one choice.", field: "options" } }));
    setFocusId(`${key}-options`);
    return true;
  };
  const done = (key: string) => {
    if (needChoices(key)) return;
    setEdit(null); setFocusId(`${key}-edit`);
  };
  const cancelEdit = () => {
    if (!edit) return;
    const { key } = edit, was = saved.find((s) => s.key === key);
    if (was) setRows((rs) => rs.map((r) => (r.key === key ? was : r)));
    else setRows((rs) => rs.filter((r) => r.key !== key)); // a row added here and never saved
    clearIssue(key); setEdit(null); setPhase(null);
    setFocusId(was ? `${key}-edit` : "add-var");
  };
  const remove = (key: string) => {
    setRows((rs) => rs.filter((r) => r.key !== key));
    if (edit?.key === key) setEdit(null);
    setAsking(null); setPhase(null); setLoose(""); clearIssue(key);
    setFocusId("add-var");
  };

  const send = async (preview: boolean) => {
    const empty = rows.find(noChoices); // any Choice row with no choices, open or not, stops the save
    if (empty && needChoices(empty.key)) return;
    setBusy(true); setLoose(""); setIssues({});
    let r: Answer<VarSave>;
    try { r = await saveWorkflowVars(rows.map(toVar), preview); } catch { setBusy(false); setLoose("Couldn't reach Shrink Your Shit. Try again."); return; }
    setBusy(false);
    if (failed(r)) {
      const e = r.error as { message: string; issues?: VarIssue[] };
      const { out, loose: rest } = mapIssues(e.issues ?? [], rows);
      setIssues(out); setPhase(null);
      setLoose(rest.length || !Object.keys(out).length ? (rest.join(" ") || e.message) : "");
      const first = rows.find((x) => out[x.key]);
      if (first) { setEdit({ key: first.key }); setFocusId(`${first.key}-${out[first.key].field}`); }
      return;
    }
    if (preview) { setPhase(r.affects); return; }
    setPhase(null); setEdit(null);
    said("Saved.");
    await reload();
  };

  return (
    <section className="card" aria-labelledby="h-vars">
      <div className="hd"><h2 id="h-vars">Variables</h2></div>
      <div className="bd stack">
        {rows.length === 0 && <p className="muted" style={{ margin: 0 }}>No variables yet. A variable is a named value that every workflow can use. A step uses one by writing its name in double braces, like {"{{output_dir}}"}, and gets the value when a job starts.</p>}
        <ul className="stack" style={{ listStyle: "none", margin: 0, padding: 0 }}>
          {rows.map((r) => (
            <li key={r.key}>
              {edit?.key === r.key
                ? <VarForm r={r} issues={issues} onChange={(p) => touch(r.key, p)} onKind={(t) => changeKind(r, t)} onDone={() => done(r.key)} onCancel={cancelEdit} />
                : <VarLine r={r} issue={issues[r.key]} asking={asking === r.key} onEdit={() => editRow(r)} onAsk={() => setAsking(r.key)} onRemove={() => remove(r.key)} onKeep={() => { setAsking(null); setFocusId(`${r.key}-edit`); }} />}
            </li>))}
        </ul>
        <div className="row" style={{ gap: 8 }}>
          <button id="add-var" className="btn" type="button" onClick={addRow} disabled={busy}>Add a variable</button>
        </div>
        <span className="hint">A library's own value wins, then the workflow's, then this one.</span>
        {loose && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{loose}</span></p>}
        {phase
          ? <div className="stack" role="group" aria-label="Confirm the save">
            <p role="status" style={{ margin: 0 }}>This change affects {plural(phase.workflows.length, "workflow", "workflows")} and {plural(phase.libraries, "library", "libraries")}.</p>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" type="button" disabled={busy} onClick={() => send(false)}>{busy ? "Saving…" : "Save"}</button>
              <button className="btn ghost" type="button" disabled={busy} onClick={() => setPhase(null)}>Cancel</button>
            </div>
          </div>
          : <div className="row" style={{ gap: 8 }}>
            <button className="btn primary" type="button" disabled={busy || !dirty} onClick={() => send(true)}>{busy ? "Checking…" : "Save"}</button>
          </div>}
      </div>
    </section>
  );
}

function VarLine({ r, issue, asking, onEdit, onAsk, onRemove, onKeep }: {
  r: Row; issue?: { msg: string }; asking: boolean; onEdit: () => void; onAsk: () => void; onRemove: () => void; onKeep: () => void;
}) {
  const label = braces(r.name);
  return (
    <div className="stack" style={{ gap: 4 }}>
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        <span className="mono">{label}</span>
        <span className="muted">{kindName(r.type)}</span>
        <span>{shownValue(r)}</span>
      </div>
      {r.description.trim() && <span className="muted">{r.description}</span>}
      {issue && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{issue.msg}</span></p>}
      <div className="row" style={{ gap: 8 }}>
        <button id={`${r.key}-edit`} className="btn sm ghost" type="button" onClick={onEdit} aria-label={`Edit ${label}`}>Edit</button>
        <button className="btn sm ghost" type="button" onClick={onAsk} aria-label={`Remove ${label}`}>Remove</button>
      </div>
      {asking && (
        <div className="row" role="group" aria-label={`Remove ${label}`} style={{ gap: 8, flexWrap: "wrap" }}>
          <span>Remove {label}? It goes when you Save.</span>
          <button className="btn sm" type="button" onClick={onRemove}>Remove it</button>
          <button className="btn sm ghost" type="button" onClick={onKeep}>Keep it</button>
        </div>)}
    </div>);
}

function VarForm({ r, issues, onChange, onKind, onDone, onCancel }: {
  r: Row; issues: Issues; onChange: (p: Partial<Row>) => void; onKind: (t: VarKind) => void; onDone: () => void; onCancel: () => void;
}) {
  const id = r.key, bad = issues[id], errId = (f: Field) => (bad?.field === f ? `${id}-${f}-err` : undefined);
  const desc = (f: Field, hint: string) => [hint, errId(f)].filter(Boolean).join(" ");
  const err = (f: Field) => (bad?.field === f ? <p id={`${id}-${f}-err`} className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{bad.msg}</span></p> : null);
  const choices = r.options.split(",").map((s) => s.trim()).filter(Boolean);
  return (
    <form className="stack" style={{ gap: 8 }} onSubmit={(e) => { e.preventDefault(); onDone(); }}>
      <div className="field">
        <label htmlFor={`${id}-name`}>Name</label>
        <input className="input mono" id={`${id}-name`} style={{ maxWidth: 320 }} value={r.name} autoComplete="off" spellCheck={false} placeholder="output_dir"
          aria-invalid={bad?.field === "name" || undefined} aria-describedby={desc("name", `${id}-name-hint`)} onChange={(e) => onChange({ name: e.target.value })} />
        <span className="hint" id={`${id}-name-hint`}>Lowercase letters, numbers and _, starting with a letter. A step writes it as {"{{name}}"}.</span>
        {err("name")}
      </div>
      <div className="field">
        <label htmlFor={`${id}-kind`}>Kind</label>
        <select className="input" id={`${id}-kind`} style={{ maxWidth: 220 }} value={r.type} aria-invalid={bad?.field === "kind" || undefined} aria-describedby={errId("kind")}
          onChange={(e) => onKind(e.target.value as VarKind)}>
          {KINDS.map(([k, l]) => <option key={k} value={k}>{l}</option>)}
        </select>
        {err("kind")}
      </div>
      <div className="field">
        <label htmlFor={`${id}-value`}>Value</label>
        {r.type === "bool"
          ? <select className="input" id={`${id}-value`} style={{ maxWidth: 220 }} value={r.text} aria-invalid={bad?.field === "value" || undefined} aria-describedby={desc("value", `${id}-value-hint`)} onChange={(e) => onChange({ text: e.target.value })}>
            <option value="">No value</option><option value="yes">Yes</option><option value="no">No</option>
          </select>
          : r.type === "enum"
            ? <select className="input" id={`${id}-value`} style={{ maxWidth: 220 }} value={r.text} aria-invalid={bad?.field === "value" || undefined} aria-describedby={desc("value", `${id}-value-hint`)} onChange={(e) => onChange({ text: e.target.value })}>
              <option value="">No value</option>{choices.map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
            : <input className="input" id={`${id}-value`} style={{ maxWidth: 320 }} value={r.text} autoComplete="off" spellCheck={false} inputMode={r.type === "number" ? "decimal" : undefined}
              aria-invalid={bad?.field === "value" || undefined} aria-describedby={desc("value", `${id}-value-hint`)} onChange={(e) => onChange({ text: e.target.value })} />}
        <span className="hint" id={`${id}-value-hint`}>Leave it empty for no value.</span>
        {err("value")}
      </div>
      {r.type === "enum" && (
        <div className="field">
          <label htmlFor={`${id}-options`}>Choices</label>
          <input className="input" id={`${id}-options`} style={{ maxWidth: 320 }} value={r.options} placeholder="low, medium, high"
            aria-invalid={bad?.field === "options" || undefined} aria-describedby={desc("options", `${id}-options-hint`)}
            onChange={(e) => {
              // a value that is no longer one of the choices is cleared, as a kind change clears it
              const list = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
              onChange({ options: e.target.value, ...(list.includes(r.text) ? {} : { text: "" }) });
            }} />
          <span className="hint" id={`${id}-options-hint`}>Separate the choices with commas.</span>
          {err("options")}
        </div>)}
      <div className="field">
        <label htmlFor={`${id}-desc`}>Description (optional)</label>
        <input className="input" id={`${id}-desc`} style={{ maxWidth: 420 }} value={r.description} onChange={(e) => onChange({ description: e.target.value })} />
      </div>
      <div className="row" style={{ gap: 8 }}>
        <button className="btn primary" type="submit">Done</button>
        <button className="btn ghost" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </form>);
}
