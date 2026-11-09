// V3 Workflows list, Templates tab and the New workflow wizard (spec 5.1, 5.2; DESIGN/v2 SCREENS V3,
// mockups/workflows.html), on the typed mock. Not here yet: the Stack view (Open, V3b), Export, Test run, History,
// Import, the Cards view. Archive isn't in the contract.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { plural } from "../format";
import { TEMPLATES, createWorkflow, deleteWorkflow, duplicateWorkflow, setWorkflow, useMock } from "../mock";
import type { Workflow } from "../types/v2";

function RowMenu({ w, onSaid }: { w: Workflow; onSaid: (s: string) => void }) {
  const ref = useRef<HTMLDetailsElement>(null);
  const close = () => ref.current?.removeAttribute("open");
  useEffect(() => {
    const outside = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    document.addEventListener("mousedown", outside);
    return () => document.removeEventListener("mousedown", outside);
  }, []);
  const used = w.libraries.map((l) => l.name).join(", ");
  return (
    <details className="menu" ref={ref} onBlur={(e) => e.relatedTarget instanceof Node && !e.currentTarget.contains(e.relatedTarget) && close()}>
      <summary className="btn sm icon ghost" aria-label={"More for " + w.name}><Icon id="i-more" /></summary>
      <div className="pop" role="menu">
        <button type="button" className="it" role="menuitem" onClick={() => (duplicateWorkflow(w.id), close(), onSaid(w.name + " duplicated."))}><Icon id="i-copy" />Duplicate</button>
        <button type="button" className="it" role="menuitem" disabled={!!used} aria-describedby={used ? "used-" + w.id : undefined}
          onClick={() => { close(); if (window.confirm("Delete the workflow " + w.name + "? This can't be undone.")) onSaid(deleteWorkflow(w.id) ?? w.name + " deleted."); }}>
          <Icon id="i-trash" />Delete
        </button>
        {used && <div className="hint" id={"used-" + w.id} style={{ padding: "0 10px 6px" }}>Used by {used}</div>}
      </div>
    </details>
  );
}

function Wizard({ onClose, onMade }: { onClose: () => void; onMade: (w: Workflow) => void }) {
  const [name, setName] = useState(""), [desc, setDesc] = useState(""), [how, setHow] = useState<"simple" | "visual" | "template">("simple");
  const [tpl, setTpl] = useState(TEMPLATES[1].id), [err, setErr] = useState("");
  const first = useRef<HTMLInputElement>(null);
  useEffect(() => first.current?.focus(), []);
  const create = () => {
    if (!name.trim()) return (setErr("Give the workflow a name."), first.current?.focus());
    onMade(createWorkflow(name.trim(), desc.trim(), how === "simple" ? "shrink_videos" : how === "template" ? tpl : null));
  };
  const card = (value: typeof how, label: string, hint: string, extra?: ReactNode) => (
    <div className="radio">
      <input type="radio" id={"how-" + value} name="how" checked={how === value} onChange={() => setHow(value)} />
      <div style={{ minWidth: 0, flex: 1 }}>
        <label htmlFor={"how-" + value}>{label}</label>
        <div className="hint" style={{ fontSize: "var(--fs)" }}>{hint}</div>
        {how === value && extra}
      </div>
    </div>
  );
  return (
    <section className="card" aria-labelledby="new-h">
      <div className="hd"><h2 id="new-h">New workflow</h2></div>
      <div className="bd stack">
        <div className="grid cols2">
          <div className="field"><label htmlFor="wf-name">Name</label>
            <input id="wf-name" ref={first} className="input" value={name} onChange={(e) => (setName(e.target.value), setErr(""))} aria-invalid={!!err} aria-describedby={err ? "wf-err" : undefined} />
            {err && <span className="hint" id="wf-err" style={{ color: "var(--error-text)" }}>{err}</span>}</div>
          <div className="field"><label htmlFor="wf-type">File type</label><select id="wf-type" className="input"><option>Video</option></select></div>
        </div>
        <div className="field"><label htmlFor="wf-desc">Description <span className="muted" style={{ fontWeight: 400 }}>(optional)</span></label>
          <input id="wf-desc" className="input" placeholder="What this workflow is for" value={desc} onChange={(e) => setDesc(e.target.value)} /></div>
        <fieldset className="stack choice" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lab">How to start</legend>
          {card("simple", "Start Simple", "A list of steps with plain choices. Good for most libraries.",
            <div className="stack" style={{ marginTop: 10 }}><span className="hint">Starting point</span>
              <div className="radio"><input type="radio" id="preset-shrink" name="preset" checked readOnly /><div style={{ minWidth: 0, flex: 1 }}>
                <label htmlFor="preset-shrink">Shrink videos</label>
                {/* design review rule 4: "same resolution", and that originals stay */}
                <div className="hint" style={{ fontSize: "var(--fs)" }}>HEVC, same resolution, stereo audio in your languages, MKV. Saves new files in a separate folder. Your originals stay where they are; no space is freed until you remove them.</div>
                <span className="tag">default</span></div></div></div>)}
          {card("visual", "Visual workflow", "An empty canvas. Connect steps and add conditions.")}
          {card("template", "From template", "Pick one below.",
            <div className="tscroll" style={{ marginTop: 10 }}><table className="tbl"><caption className="sr">Templates</caption>
              <thead><tr><th><span className="sr">Choose</span></th><th>Template</th><th>What it does</th><th>Steps it uses</th><th>Changes originals</th></tr></thead>
              <tbody>{TEMPLATES.map((t) => (
                <tr key={t.id}><td><input type="radio" name="tpl" id={"tpl-" + t.id} checked={tpl === t.id} onChange={() => setTpl(t.id)} /></td>
                  <td><label htmlFor={"tpl-" + t.id}><strong>{t.name}</strong></label></td><td>{t.description}</td><td className="sm">{t.steps.join(", ")}</td><td>{t.changesOriginals ? "Yes" : "No"}</td></tr>))}</tbody>
            </table></div>)}
        </fieldset>
        <div className="row"><button className="btn primary" type="button" onClick={create}>Create draft</button><button className="btn ghost" type="button" onClick={onClose}>Cancel</button></div>
      </div>
    </section>
  );
}

export function Workflows() {
  const s = useMock();
  const [tab, setTab] = useState(new URLSearchParams(location.search).get("tab") === "templates" ? "templates" : "workflows");
  const [q, setQ] = useState(""), [tag, setTag] = useState(""), [fav, setFav] = useState(false), [wizard, setWizard] = useState(false), [said, setSaid] = useState("");
  const tags = [...new Set(s.wf.flatMap((w) => w.tags))].sort();
  const rows = s.wf.filter((w) => (!q || (w.name + " " + w.description).toLowerCase().includes(q.toLowerCase())) && (!tag || w.tags.includes(tag)) && (!fav || w.favourite));
  return (
    <div className="stack">
      <div className="pagehead"><div><h1>Workflows</h1><p className="sub">What happens to each file. A library uses one workflow.</p></div><span className="sp" />
        {!wizard && <button className="btn primary" type="button" onClick={() => (setWizard(true), setSaid(""))}><Icon id="i-plus" />New workflow</button>}</div>
      {wizard && <Wizard onClose={() => setWizard(false)} onMade={(w) => (setWizard(false), setTab("workflows"), setSaid("Draft created: " + w.name + ". Its steps open in the Stack view, which comes in the next step of the new interface."))} />}
      <div className="tabs" role="tablist">
        <button role="tab" type="button" aria-selected={tab === "workflows"} onClick={() => setTab("workflows")}>Workflows</button>
        <button role="tab" type="button" aria-selected={tab === "templates"} onClick={() => setTab("templates")}>Templates <span className="n">{TEMPLATES.length}</span></button>
      </div>
      <p className="muted" role="status">{said}</p>
      {tab === "templates" ? (
        <section className="card"><div className="tscroll"><table className="tbl">
          <thead><tr><th>Template</th><th>File type</th><th>What it does</th><th>Steps it uses</th><th>Changes originals</th></tr></thead>
          <tbody>{TEMPLATES.map((t) => <tr key={t.id}><td><strong>{t.name}</strong></td><td>Video</td><td>{t.description}</td><td className="sm">{t.steps.join(", ")}</td><td>{t.changesOriginals ? "Yes" : "No"}</td></tr>)}</tbody>
        </table></div></section>
      ) : (
        <section className="card">
          <div className="tools">
            <div className="field" style={{ flex: 1, minWidth: 180 }}><input className="input" type="search" placeholder="Search workflows" aria-label="Search workflows" value={q} onChange={(e) => setQ(e.target.value)} /></div>
            <select className="input" aria-label="Tag" value={tag} onChange={(e) => setTag(e.target.value)} style={{ width: "auto" }}>
              <option value="">Tag: any</option>{tags.map((t) => <option key={t} value={t}>{t}</option>)}
            </select>
            <button className="chip" type="button" aria-pressed={fav} onClick={() => setFav(!fav)}><Icon id="i-star" />Favourites</button>
          </div>
          {rows.length === 0 ? <div className="empty">{s.wf.length ? "No workflow matches." : "No workflows yet. Make one with New workflow."}</div> : (
            <div className="tscroll"><table className="tbl">
              <thead><tr><th>Name</th><th>File type</th><th>Tags</th><th>Libraries</th><th className="num">Active jobs</th><th>Version</th><th>Check</th><th>Enabled</th><th><span className="sr">Menu</span></th></tr></thead>
              <tbody>{rows.map((w) => {
                const problems = w.validation.errors + w.validation.warnings;
                return (
                  <tr key={w.id}>
                    <td><span className="row nw">
                      <button className="btn sm icon ghost" type="button" aria-pressed={w.favourite} aria-label={"Favourite: " + w.name} onClick={() => setWorkflow(w.id, { favourite: !w.favourite })}
                        style={w.favourite ? { color: "var(--warning-text)" } : undefined}><Icon id="i-star" /></button>
                      <span><strong>{w.name}</strong><span className="sub">{w.description}</span></span></span></td>
                    <td>Video</td>
                    <td>{w.tags.map((t) => <span className="tag" key={t}>{t}</span>)}</td>
                    <td>{w.libraries.length ? w.libraries.map((l) => l.name).join(", ") : <span className="muted">None</span>}</td>
                    <td className="num">{w.activeJobs}</td>
                    <td className="nowrap">{w.version == null ? "Not published" : "v" + w.version + (w.draftDiffers ? " · draft changes" : "")}</td>
                    <td>{problems ? <span className={"st " + (w.validation.errors ? "bad" : "warn")}><Icon id="i-alert" /><span>{plural(problems, "problem", "problems")}</span></span>
                      : <span className="st ok"><Icon id="i-check-c" /><span>OK</span></span>}</td>
                    <td>{w.status === "draft" ? <span className="muted">Draft</span>
                      : <input type="checkbox" role="switch" className="switch" aria-label={"Enabled: " + w.name} checked={w.status === "enabled"} onChange={(e) => setWorkflow(w.id, { status: e.target.checked ? "enabled" : "disabled" })} />}</td>
                    <td><RowMenu w={w} onSaid={setSaid} /></td>
                  </tr>);
              })}</tbody>
            </table></div>)}
        </section>
      )}
    </div>
  );
}
