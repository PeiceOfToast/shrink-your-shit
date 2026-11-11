// V3 Workflows list, Templates tab and the New workflow wizard (spec 5.1, 5.2; DESIGN/v2 SCREENS V3,
// mockups/workflows.html), on the typed mock. Not here yet: the Stack view (Open, V3b), Export, Test run, History,
// Import, the Cards view. Archive isn't in the contract.
import { PageHead } from "../PageHead";
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { plural } from "../format";
import { TEMPLATES, createWorkflow, deleteWorkflow, duplicateWorkflow, setWorkflow, useMock } from "../mock";
import { Link, navigate, useSearch } from "../router";
import type { Workflow } from "../types/v2";

// design review rule 4: "same resolution", and that originals stay. Also first run's step 2.
export const START_SIMPLE = "A list of steps with plain choices. HEVC, same resolution, stereo audio in your languages, MKV. Saves new files in a separate folder. Your originals stay where they are; no space is freed until you remove them.";

function RowMenu({ w, onSaid }: { w: Workflow; onSaid: (s: string) => void }) {
  const ref = useRef<HTMLDetailsElement>(null);
  const close = () => ref.current?.removeAttribute("open");
  useEffect(() => {
    const outside = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    const esc = (e: KeyboardEvent) => e.key === "Escape" && ref.current?.open && (close(), ref.current.querySelector("summary")?.focus());
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", esc);
    return () => (document.removeEventListener("mousedown", outside), document.removeEventListener("keydown", esc));
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
  const [name, setName] = useState(""), [desc, setDesc] = useState(""), [err, setErr] = useState("");
  const first = useRef<HTMLInputElement>(null);
  useEffect(() => first.current?.focus(), []);
  const create = () => {
    if (!name.trim()) return (setErr("Give the workflow a name."), first.current?.focus());
    onMade(createWorkflow(name.trim(), desc.trim()));
  };
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
        {/* How to start: the engine has the Start Simple preset only; Visual workflow (an empty canvas) and other
            templates come when the server has them, never as disabled choices (spec 5.2; review P3). */}
        <fieldset className="stack choice" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lab">How to start</legend>
          <div className="radio"><input type="radio" id="how-simple" name="how" checked readOnly />
            <div style={{ minWidth: 0, flex: 1 }}>
              <label htmlFor="how-simple">Start Simple: Shrink videos</label>
              {/* design review rule 4: "same resolution", and that originals stay */}
              <div className="hint" style={{ fontSize: "var(--fs)" }}>{START_SIMPLE}</div>
            </div></div>
        </fieldset>
        <div className="row"><button className="btn primary" type="button" onClick={create}>Create draft</button><button className="btn ghost" type="button" onClick={onClose}>Cancel</button></div>
      </div>
    </section>
  );
}

export function Workflows() {
  const s = useMock();
  const tab = new URLSearchParams(useSearch()).get("tab") === "templates" ? "templates" : "workflows"; // in the URL: Back and reload keep it
  const setTab = (t: string) => navigate("/workflows" + (t === "templates" ? "?tab=templates" : ""));
  const [q, setQ] = useState(""), [tag, setTag] = useState(""), [fav, setFav] = useState(false), [wizard, setWizard] = useState(false), [said, setSaid] = useState("");
  const tags = [...new Set(s.wf.flatMap((w) => w.tags))].sort();
  const rows = s.wf.filter((w) => (!q || (w.name + " " + w.description).toLowerCase().includes(q.toLowerCase())) && (!tag || w.tags.includes(tag)) && (!fav || w.favourite));
  return (
    <div className="stack">
      <PageHead icon="i-workflow" title="Workflows" help="What happens to each file. A library uses one workflow." />
      {wizard && <Wizard onClose={() => setWizard(false)} onMade={(w) => navigate("/workflows/" + w.id)} />}
      <div className="tabs" role="tablist">
        <button role="tab" type="button" aria-selected={tab === "workflows"} onClick={() => tab !== "workflows" && setTab("workflows")}>Workflows</button>
        <button role="tab" type="button" aria-selected={tab === "templates"} onClick={() => tab !== "templates" && setTab("templates")}>Templates <span className="n">{TEMPLATES.length}</span></button>
      </div>
      <p className="muted" role="status">{said}</p>
      {tab === "templates" ? (
        <div className="list"><table className="tbl">
          <thead><tr><th>Template</th><th data-p="3">What it does</th><th data-p="4">Steps it uses</th><th className="stc">Changes originals</th></tr></thead>
          <tbody>{TEMPLATES.map((t) => <tr key={t.id}><td className="idc"><span className="ttl">{t.name}</span><span className="sub phone">{t.description}</span></td>
            <td data-p="3">{t.description}</td><td className="sm" data-p="4">{t.steps.join(", ")}</td><td className="stc"><span className="pill">{t.changesOriginals ? "Changes originals" : "Originals stay"}</span></td></tr>)}</tbody>
        </table></div>
      ) : (<>
        {/* one toolbar (clean pass T4): New workflow first, filters, search on the right */}
        <div className="ltools" role="toolbar" aria-label="Workflows">
          {!wizard && <button className="btn sm primary" type="button" onClick={() => (setWizard(true), setSaid(""))}><Icon id="i-plus" />New workflow</button>}
          <select className="input sm" aria-label="Tag" value={tag} onChange={(e) => setTag(e.target.value)}>
            <option value="">Tag: any</option>{tags.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
          <button className="btn sm ghost" type="button" aria-pressed={fav} onClick={() => setFav(!fav)}><Icon id="i-star" />Favourites</button>
          <input className="input find" type="search" placeholder="Search workflows" aria-label="Search workflows" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {rows.length === 0 ? <div className="empty">{s.wf.length ? "No workflow matches." : "No workflows yet. Make one with New workflow."}</div> : (
          <div className="list"><table className="tbl">
            <thead><tr><th>Name</th><th data-p="2">Libraries</th><th data-p="3">Version</th><th className="num" data-p="4">Active jobs</th><th data-p="5">Tags</th><th className="stc">Check</th><th className="actc">Enabled</th><th className="actc"><span className="sr">Menu</span></th></tr></thead>
            <tbody>{rows.map((w) => {
              const problems = w.validation.errors + w.validation.warnings;
              const used = w.libraries.length ? w.libraries.map((l) => l.name).join(", ") : "No library";
              const version = w.version == null ? "Draft · not published yet" : "v" + w.version + (w.draftDiffers ? " · draft changes" : "");
              return (
                <tr key={w.id}>
                  <td className="idc"><span className="row nw">
                    <button className="btn sm icon ghost over" type="button" aria-pressed={w.favourite} aria-label={"Favourite: " + w.name} onClick={() => setWorkflow(w.id, { favourite: !w.favourite })}
                      style={w.favourite ? { color: "var(--warning-text)" } : undefined}><Icon id="i-star" /></button>
                    <span><Link className="ttl" to={"/workflows/" + w.id}>{w.name}</Link><span className="sub">{w.description}</span>
                      <span className="sub phone">{used} · {version}</span></span></span></td>
                  <td data-p="2">{w.libraries.length ? used : <span className="muted">None</span>}</td>
                  <td className="nowrap" data-p="3">{version}</td>
                  <td className="num" data-p="4">{w.activeJobs}</td>
                  <td data-p="5"><span className="chips">{w.tags.map((t) => <span className="mchip" key={t}>{t}</span>)}</span></td>
                  <td className="stc">{problems ? <span className={"pill " + (w.validation.errors ? "bad" : "warn")}><Icon id="i-alert" />{plural(problems, "problem", "problems")}</span>
                    : <span className="pill"><Icon id="i-check-c" />OK</span>}</td>
                  <td className="actc">{w.status === "draft" ? <span className="muted" aria-label="Not published yet">-</span>
                    : <input type="checkbox" role="switch" className="switch over" aria-label={"Enabled: " + w.name} checked={w.status === "enabled"} onChange={(e) => setWorkflow(w.id, { status: e.target.checked ? "enabled" : "disabled" })} />}</td>
                  <td className="actc"><RowMenu w={w} onSaid={setSaid} /></td>
                </tr>);
            })}</tbody>
          </table></div>)}
      </>)}
    </div>
  );
}
