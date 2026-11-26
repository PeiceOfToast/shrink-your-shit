import { useState } from "react";
import { api, type WorkflowVariable, type VariableType } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { Link } from "../../router.tsx";
import { LoadState, errText } from "./parts.tsx";

const TYPES: VariableType[] = ["string", "number", "boolean", "enum", "path"];

// The list is saved as a whole. usedBy links to the workflows and libraries that read each variable.
export function Variables() {
  const s = useApi(() => api.getVariables(), []);
  const [draft, setDraft] = useState<WorkflowVariable[] | null>(null);
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
      {list.map((v, i) => (
        <section className="set-group" key={i}>
          <div className="set-row">
            <div className="set-lab"><label htmlFor={`v-name-${i}`}>Name</label></div>
            <div className="set-ctl"><input id={`v-name-${i}`} className="inp mono" value={v.name} onChange={(e) => edit(list.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} /></div>
          </div>
          <div className="set-row">
            <div className="set-lab"><label htmlFor={`v-type-${i}`}>Type</label></div>
            <div className="set-ctl">
              <select id={`v-type-${i}`} className="sel" value={v.type} onChange={(e) => edit(list.map((x, j) => (j === i ? { ...x, type: e.target.value as VariableType } : x)))}>
                {TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
              </select>
            </div>
          </div>
          <div className="set-row">
            <div className="set-lab"><label htmlFor={`v-val-${i}`}>Value</label>{v.description && <div className="hint">{v.description}</div>}</div>
            <div className="set-ctl"><input id={`v-val-${i}`} className="inp" value={String(v.value ?? "")} onChange={(e) => edit(list.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} /></div>
          </div>
          <div className="set-row">
            <div className="set-lab"><span>Used by</span></div>
            <div className="set-ctl">
              {v.usedBy.length === 0 && <span className="muted sm">Nothing yet.</span>}
              {v.usedBy.map((u) => (
                <div key={`${u.type}-${u.id}`} className="sm">
                  <Link to={u.type === "workflow" ? `/workflows/${u.id}` : `/libraries/${u.id}`}>{u.name}</Link>{" "}
                  <span className="muted">{u.type}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="row"><button type="button" className="btn ghost sm" onClick={() => edit(list.filter((_, j) => j !== i))}>Remove variable</button></div>
        </section>
      ))}
      <div className="row">
        <button type="button" className="btn ghost sm" onClick={() => edit([...list, { name: "", type: "string", value: "", usedBy: [] }])}>Add variable</button>
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
