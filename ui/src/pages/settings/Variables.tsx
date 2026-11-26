import { Fragment, useState } from "react";
import { api, type WorkflowVariable, type VariableType } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { Link } from "../../router.tsx";
import { ListInput, LoadState, Switch, errText } from "./parts.tsx";

// Plain words for each type. The stored type names stay as the API has them.
const TYPES: { value: VariableType; label: string }[] = [
  { value: "string", label: "Text" },
  { value: "number", label: "Number" },
  { value: "enum", label: "Choice" },
  { value: "path", label: "Folder" },
  { value: "boolean", label: "Yes or no" },
];
const WORD = Object.fromEntries(TYPES.map((t) => [t.value, t.label])) as Record<VariableType, string>;

// A value that fits its type, so a switch never holds "true" as text.
function valueFor(type: VariableType, options?: string[]): WorkflowVariable["value"] {
  if (type === "boolean") return false;
  if (type === "number") return 0;
  if (type === "enum") return options?.[0] ?? "";
  return "";
}

function ValueControl({ id, v, onChange }: { id: string; v: WorkflowVariable; onChange: (value: WorkflowVariable["value"]) => void }) {
  if (v.type === "boolean") return <Switch label={`Value of ${v.name || "variable"}`} on={v.value === true} onChange={onChange} />;
  if (v.type === "enum")
    return (
      <select id={id} className="sel" value={String(v.value ?? "")} onChange={(e) => onChange(e.target.value)}>
        {(v.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
      </select>
    );
  if (v.type === "number") return <input id={id} type="number" className="inp narrow" value={Number(v.value ?? 0)} onChange={(e) => onChange(e.target.valueAsNumber)} />;
  return <input id={id} className="inp" value={String(v.value ?? "")} onChange={(e) => onChange(e.target.value)} />;
}

function shown(v: WorkflowVariable): string {
  if (v.type === "boolean") return v.value === true ? "Yes" : "No";
  return String(v.value ?? "");
}

// The list is saved as a whole. Click a name to edit that variable in place; usedBy links to the workflows and libraries that read it.
export function Variables() {
  const s = useApi(() => api.getVariables(), []);
  const [draft, setDraft] = useState<WorkflowVariable[] | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [issues, setIssues] = useState<string[]>([]);
  const [affects, setAffects] = useState<string | null>(null);
  const list = draft ?? s.data?.items;
  if (!list) return <><h2 className="set-title">Variables</h2><LoadState s={s} /></>;
  const dirty = draft !== null && JSON.stringify(draft) !== JSON.stringify(s.data?.items);
  const edit = (next: WorkflowVariable[]) => {
    setAffects(null);
    setErr(null);
    setIssues([]);
    setDraft(next);
  };
  const patch = (i: number, p: Partial<WorkflowVariable>) => edit(list.map((x, j) => (j === i ? { ...x, ...p } : x)));
  const setType = (i: number, type: VariableType) => {
    const options = type === "enum" ? (list[i].options ?? []) : undefined;
    patch(i, { type, options, value: valueFor(type, options) });
  };
  const remove = (i: number) => {
    setOpen(null);
    edit(list.filter((_, j) => j !== i));
  };
  const save = () => {
    setBusy(true);
    setErr(null);
    setIssues([]);
    api.putVariables(list).then(
      (r) => {
        setDraft(null);
        setAffects(`Saved. ${r.affects.workflows.length} workflows and ${r.affects.libraries} libraries use these.`);
        s.reload();
      },
      (e: unknown) => {
        const list2 = (e as { issues?: { message: string }[] }).issues;
        if (list2) setIssues(list2.map((i) => i.message));
        else setErr(errText(e));
      },
    ).finally(() => setBusy(false));
  };
  return (
    <>
      <h2 className="set-title">Variables</h2>
      <p className="muted sm">Values that workflows and libraries read by name. Names are lowercase, with no spaces.</p>
      <table className="tbl vars">
        <thead>
          <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Value</th>
            <th>Used by</th>
            <th aria-label="Actions" />
          </tr>
        </thead>
        <tbody>
          {list.map((v, i) => (
            <Fragment key={i}>
              <tr>
                <td>
                  <button type="button" className="linklike mono" aria-expanded={open === i} onClick={() => setOpen(open === i ? null : i)}>
                    {v.name || "(no name)"}
                  </button>
                </td>
                <td>{WORD[v.type]}</td>
                <td className="mono sm">{shown(v)}</td>
                <td className="sm">
                  {v.usedBy.length === 0 && <span className="muted">Nothing yet</span>}
                  {v.usedBy.map((u) => (
                    <div key={`${u.type}-${u.id}`}>
                      <Link to={u.type === "workflow" ? `/workflows/${u.id}` : `/libraries/${u.id}`}>{u.name}</Link>{" "}
                      <span className="muted">{u.type}</span>
                    </div>
                  ))}
                </td>
                <td className="num">
                  <button type="button" className="btn ghost sm" aria-label={`Remove ${v.name || "variable"}`} onClick={() => remove(i)}>Remove</button>
                </td>
              </tr>
              {open === i && (
                <tr>
                  <td colSpan={5}>
                    <div className="set-row">
                      <div className="set-lab"><label htmlFor={`v-name-${i}`}>Name</label></div>
                      <div className="set-ctl"><input id={`v-name-${i}`} className="inp mono" value={v.name} onChange={(e) => patch(i, { name: e.target.value })} /></div>
                    </div>
                    <div className="set-row">
                      <div className="set-lab"><label htmlFor={`v-type-${i}`}>Type</label></div>
                      <div className="set-ctl">
                        <select id={`v-type-${i}`} className="sel" value={v.type} onChange={(e) => setType(i, e.target.value as VariableType)}>
                          {TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
                        </select>
                      </div>
                    </div>
                    {v.type === "enum" && (
                      <div className="set-row">
                        <div className="set-lab"><label htmlFor={`v-choices-${i}`}>Choices</label><div className="hint">Separate with commas.</div></div>
                        <div className="set-ctl">
                          <ListInput id={`v-choices-${i}`} label="Choices" value={v.options ?? []} onChange={(options) => patch(i, { options, value: options.includes(String(v.value ?? "")) ? v.value : (options[0] ?? "") })} />
                        </div>
                      </div>
                    )}
                    <div className="set-row">
                      <div className="set-lab"><label htmlFor={`v-val-${i}`}>Value</label>{v.description && <div className="hint">{v.description}</div>}</div>
                      <div className="set-ctl"><ValueControl id={`v-val-${i}`} v={v} onChange={(value) => patch(i, { value })} /></div>
                    </div>
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
      <div className="row">
        <button
          type="button"
          className="btn sm"
          onClick={() => {
            setOpen(list.length);
            edit([...list, { name: "", type: "string", value: "", usedBy: [] }]);
          }}
        >
          Add variable
        </button>
      </div>
      {issues.map((m) => <p className="bad" role="alert" key={m}>{m}</p>)}
      {err && <p className="bad" role="alert">{err}</p>}
      {affects && <p className="muted sm" role="status">{affects}</p>}
      {dirty && (
        <div className="savebar" role="region" aria-label="Unsaved changes">
          <span>Unsaved changes</span>
          <span className="sp" />
          <button type="button" className="btn ghost sm" onClick={() => setDraft(null)} disabled={busy}>Undo changes</button>
          <button type="button" className="btn primary sm" onClick={save} disabled={busy}>{busy ? "Saving…" : "Save"}</button>
        </div>
      )}
    </>
  );
}
