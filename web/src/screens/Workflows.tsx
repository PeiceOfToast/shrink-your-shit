// V3 Workflows list, Templates tab and the New workflow wizard (spec 5.1, 5.2; DESIGN/v2 SCREENS V3,
// mockups/workflows.html), on the real /api/v2 (#109) or the mock with ?mock=1. Opening one goes to its step list (WorkflowStack.tsx). Import is here (a file becomes a draft); Export is on the
// workflow page. Not here yet: Test run, History, the Cards view. Archive isn't in the contract.
import { Empty, LoadFailed, PageHead } from "../PageHead";
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { count, plural } from "../format";
import { SignedOut, failed, type Answer } from "../api";
import { useData } from "../useData";
import { deleteWorkflow, duplicateWorkflow, getDraft, importWorkflow, listTemplates, listWorkflows, patchWorkflow } from "../workflowApi";
import { importLines, type ReportLine } from "../importReport";
import { shownWorkflows, wfFilterCounts, type WfFilter } from "../workflowList";
import { getWorkflowVars } from "../workflowVarsApi";
import { VariablesSettings } from "./Variables";
import { Link, navigate, useSearch } from "../router";
import type { Workflow } from "../types/v2";
import { closeMenuToSummary, menuEscape } from "../menuFocus";

// design review rule 4: "same resolution", and that originals stay. Also first run's step 2.
export const START_SIMPLE = "A list of steps with plain choices. HEVC, same resolution, stereo audio in your languages, MKV. Saves new files in a separate folder. Your originals stay where they are; no space is freed until you remove them.";

// An action's answer: its error message goes to `say`; signed out goes to sign-in (api.ts).
async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}

// Recent runs (spec V3, 18:50Z): one quiet line from lastRun, which covers the past 7 days. Zero parts are left out;
// "failed" uses the error text colour; "skipped" comes after it, muted (lead 18:53Z). On a card it is a chip; its text is cut
// with an ellipsis and the full line is in title.
function Runs({ r }: { r: Workflow["lastRun"] }) {
  if (!r.at) return <span className="mchip"><span className="cut">never ran</span></span>;
  const min = Math.max(0, Math.round((Date.now() - new Date(r.at).getTime()) / 60000));
  const when = min < 1 ? "just now" : min < 60 ? `${min} min ago` : min < 1440 ? `${Math.round(min / 60)} h ago` : plural(Math.round(min / 1440), "day", "days") + " ago";
  const done = r.completed > 0 ? `${count(r.completed)} done` : "", failed = r.failed > 0 ? `${count(r.failed)} failed` : "", skipped = r.skipped > 0 ? `${count(r.skipped)} skipped` : "";
  const line = "ran " + when + (done || failed || skipped ? " · past 7 days: " : "") + [done, failed, skipped].filter(Boolean).join(", ");
  return (<span className="mchip" title={line}><span className="cut">ran {when}{(done || failed || skipped) && " · past 7 days: "}{done}{done && (failed || skipped) && ", "}{failed && <span style={{ color: "var(--error-text)" }}>{failed}</span>}{failed && skipped && ", "}{skipped}</span></span>);
}

function RowMenu({ w, onSaid, reload }: { w: Workflow; onSaid: (s: string) => void; reload: () => void }) {
  const ref = useRef<HTMLDetailsElement>(null);
  const close = () => ref.current?.removeAttribute("open");
  useEffect(() => {
    const outside = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close();
    document.addEventListener("mousedown", outside);
    return () => document.removeEventListener("mousedown", outside);
  }, []);
  const used = w.libraries.map((l) => l.name).join(", ");
  return (
    <details className="menu" ref={ref} onKeyDown={menuEscape} onBlur={(e) => e.relatedTarget instanceof Node && !e.currentTarget.contains(e.relatedTarget) && close()}>
      <summary className="btn sm icon ghost" aria-label={"More for " + w.name}><Icon id="i-more" /></summary>
      <div className="pop" role="menu">
        <button type="button" className="it" role="menuitem" onClick={async (e) => { closeMenuToSummary(e.currentTarget); if (await act(patchWorkflow(w.id, { favourite: !w.favourite }), onSaid)) reload(); }}><Icon id="i-star" />{w.favourite ? "Remove from favourites" : "Add to favourites"}</button>
        <button type="button" className="it" role="menuitem" onClick={async (e) => { closeMenuToSummary(e.currentTarget); if (await act(duplicateWorkflow(w.id), onSaid)) (onSaid(w.name + " duplicated."), reload()); }}><Icon id="i-copy" />Duplicate</button>
        <button type="button" className="it" role="menuitem" aria-disabled={used ? true : undefined} aria-describedby={used ? "used-" + w.id : undefined}
          onClick={async (e) => { if (used) return; closeMenuToSummary(e.currentTarget); if (!window.confirm("Delete the workflow " + w.name + "? This can't be undone.")) return;
            // The server answers 204 with no body (null), which act() can't tell apart from a failure.
            let r; try { r = await deleteWorkflow(w.id); } catch { return; }
            if (failed(r)) onSaid(r.error.message); else { onSaid(w.name + " deleted."); reload(); } }}>
          <Icon id="i-trash" />Delete
        </button>
        {used && <div className="hint" id={"used-" + w.id} style={{ padding: "0 10px 6px" }}>Used by {used}</div>}
      </div>
    </details>
  );
}

// The filter buttons: the names, with counts once the list has loaded without error (M3a's rule, same group semantics)
const WF_FILTERS: [WfFilter, string][] = [["all", "All"], ["enabled", "Enabled"], ["problems", "With problems"]];

// Loading: the shape of a card (tile, a row of pill bars, a status pill on the right), grey blocks, no motion
function WfSkeleton() {
  return (
    <div className="skel lskel">
      <span role="status"><span className="sr">Loading</span></span>
      <ul className="lcards" aria-hidden="true">
        {[0, 1, 2, 3].map((i) => (
          <li className="lcard" key={i}>
            <span className="ltile skel-tile" />
            <div className="lmain"><span className="skel-b skel-w40" /><div className="chips"><span className="skel-b skel-pill" /><span className="skel-b skel-pill" /><span className="skel-b skel-pill" /></div></div>
            <div className="lside"><span className="skel-b skel-pill" /></div>
          </li>
        ))}
      </ul>
    </div>
  );
}

// One workflow as a card (design workflows-list): tile, name (a stretched link opens the whole card), a draft chip, the
// description on one muted line, then the chip row: used by, version, last runs, tags. On the right: the status pill, the
// Enabled switch and the menu; a "★ favourite" note under them. Only the name link is under the card;
// the switch, menu and chips sit above it.
function WfCard({ w, hasTags, setSaid, reload }: { w: Workflow; hasTags: boolean; setSaid: (s: string) => void; reload: () => void }) {
  const problems = w.validation.errors + w.validation.warnings;
  const used = w.libraries.map((l) => l.name).join(", ");
  const usedText = used ? "used by " + used : "used by nothing yet";
  return (
    <li className="lcard wfcard">
      <span className="ltile" aria-hidden="true"><Icon id="i-workflow" /></span>
      <div className="lmain">
        <div className="lname">
          <Link className="ttl" to={"/workflows/" + w.id} title={w.name}>{w.name}</Link>
          {w.status === "draft" && <span className="mchip">draft</span>}
        </div>
        {w.description && <span className="sub cut" title={w.description}>{w.description}</span>}
        <div className="chips">
          <span className="mchip" title={usedText}><Icon id="i-folder" /><span className="cut">{usedText}</span></span>
          {w.version != null && <span className="mchip" title={"v" + w.version + (w.draftDiffers ? " · draft changes" : "")}><span className="cut">{"v" + w.version + (w.draftDiffers ? " · draft changes" : "")}</span></span>}
          <Runs r={w.lastRun} />
          {hasTags && w.tags.map((t) => <span className="mchip" key={t} title={t}><span className="cut">{t}</span></span>)}
        </div>
      </div>
      <span className="lchev" aria-hidden="true">›</span>
      <div className="lside">
        <div className="wfact">
          {problems ? <span className="pill warn"><Icon id="i-alert" />{plural(problems, "problem", "problems")}</span>
            : <span className="pill ok"><Icon id="i-check-c" />OK</span>}
          {w.status !== "draft" && <label className="hit"><input type="checkbox" role="switch" className="switch" aria-label={"Enabled: " + w.name} checked={w.status === "enabled"} onChange={async (e) => { if (await act(patchWorkflow(w.id, { status: e.target.checked ? "enabled" : "disabled" }), setSaid)) reload(); }} /></label>}
          <RowMenu w={w} onSaid={setSaid} reload={reload} />
        </div>
        {w.favourite && <div className="lnote"><span>★ favourite</span></div>}
      </div>
    </li>
  );
}

export function Workflows() {
  const wfs = useData(listWorkflows, [], 10000), tpl = useData(listTemplates, []), vars = useData(getWorkflowVars, []);
  const all = wfs.data ?? [], templates = tpl.data ?? [];
  const tabParam = new URLSearchParams(useSearch()).get("tab"), tab = tabParam === "templates" || tabParam === "variables" ? tabParam : "workflows"; // in the URL: Back and reload keep it
  const setTab = (t: string) => navigate("/workflows" + (t === "workflows" ? "" : "?tab=" + t));
  const [q, setQ] = useState(""), [tag, setTag] = useState(""), [fav, setFav] = useState(false), [filter, setFilter] = useState<WfFilter>("all"), [said, setSaid] = useState("");
  const tags = [...new Set(all.flatMap((w) => w.tags))].sort();
  // Pass 5 #3: with no tag on any workflow, the Tag filter is hidden and the card shows no tag chips. Based on the list the API returned, so filtering never hides them.
  const hasTags = tags.length > 0, activeTag = hasTags ? tag : "";
  // counts are shown only once the list has loaded without error; the empty state has no filters or search at all
  const counts = wfFilterCounts(all), counted = !!wfs.data && !wfs.error, empty = wfs.loaded && !wfs.error && all.length === 0;
  const rows = shownWorkflows(all, { filter, q, tag: activeTag, fav });
  // Import: a file becomes a new draft. The panel says what to fix before it runs; a refused file sends nothing new.
  const fileIn = useRef<HTMLInputElement>(null);
  const [imp, setImp] = useState<{ id: string; name: string; lines: ReportLine[] } | null>(null), [impErr, setImpErr] = useState("");
  const pickFile = async (f: File) => {
    setImp(null); setImpErr(""); setSaid("");
    if (f.size > 8 * 1024 * 1024) return setImpErr("This file is larger than 8 MiB, so nothing was sent.");
    let json: unknown;
    try { json = JSON.parse(await f.text()); } catch { return setImpErr("This file isn't a workflow export."); }
    const r = await act(importWorkflow(json), setImpErr);
    if (!r) return;
    const d = await getDraft(r.id);
    const secrets = failed(d) ? null : d.workflow.variables.filter((v) => v.type === "secret" && !v.hasValue).map((v) => v.name);
    setImp({ id: r.id, name: r.name, lines: importLines(r.import, secrets, "Workflows > Variables") });
    wfs.reload();
  };
  // A finished import moves focus to its result, so keyboard users land on what it says (a failure keeps focus on Import…; its alert speaks)
  const impFocus = useRef<HTMLParagraphElement>(null);
  useEffect(() => { if (imp) impFocus.current?.focus(); }, [imp]);
  return (
    <div className="stack">
      <PageHead icon="i-workflow" title="Workflows" sub="What happens to each file. A library uses one workflow.">
        {tab === "workflows" && <>
          <button className="btn sm ghost" type="button" onClick={() => fileIn.current?.click()}>Import…</button>
          <input ref={fileIn} type="file" accept=".json,application/json" hidden onChange={(e) => { const f = e.target.files?.[0]; e.target.value = ""; if (f) void pickFile(f); }} />
          <button className="btn sm ghost" type="button" aria-pressed={fav} onClick={() => setFav(!fav)}><Icon id="i-star" />Favourites</button>
          <button className="btn sm primary" type="button" onClick={() => navigate("/workflows/guided")}><Icon id="i-plus" />New workflow</button>
        </>}
      </PageHead>
      <div className="tabs" role="tablist">
        <button role="tab" type="button" aria-selected={tab === "workflows"} onClick={() => tab !== "workflows" && setTab("workflows")}>Workflows {wfs.data && <span className="n">{all.length}</span>}</button>
        <button role="tab" type="button" aria-selected={tab === "templates"} onClick={() => tab !== "templates" && setTab("templates")}>Templates {tpl.data && <span className="n">{templates.length}</span>}</button>
        <button role="tab" type="button" aria-selected={tab === "variables"} onClick={() => tab !== "variables" && setTab("variables")}>Variables {vars.data && <span className="n">{vars.data.total}</span>}</button>
      </div>
      <p className="muted" role="status">{said}</p>
      {impErr && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{impErr}</span></p>}
      {/* always mounted, so the screen reader announces the result when it arrives; hidden (not removed) when there is none */}
      <div role="status" className={imp ? "card" : "sr"} aria-label={imp ? "Import result" : undefined}>{imp && <div className="bd stack">
        <div className="stack" style={{ gap: 6 }}>
          <p ref={impFocus} tabIndex={-1}>Imported as a draft: {imp.name}. <Link to={"/workflows/" + encodeURIComponent(imp.id)}>Open it</Link></p>
          {imp.lines.map((l, i) => <p key={i} className={"st " + (l.kind === "error" ? "bad" : l.kind === "warn" ? "warn" : "ok")}><Icon id={l.kind === "ok" ? "i-check-c" : "i-alert"} /><span>{l.kind === "error" ? <span className="sr">Error: </span> : l.kind === "warn" ? <span className="sr">Warning: </span> : null}{l.text}</span></p>)}
        </div>
        <div className="row"><button className="btn sm ghost" type="button" onClick={() => setImp(null)}>Dismiss</button></div>
      </div>}</div>
      {tab === "variables" ? <VariablesSettings /> : tab === "templates" ? (tpl.error && !tpl.data ? <LoadFailed error={tpl.error} reload={tpl.reload} /> :
        <div className="list"><table className="tbl">
          <thead><tr><th>Template</th><th data-p="3">What it does</th><th data-p="4">Steps it uses</th><th className="stc">Changes originals</th></tr></thead>
          <tbody>{templates.map((t) => <tr key={t.id}><td className="idc"><span className="ttl">{t.name}</span><span className="sub phone">{t.description}</span></td>
            <td data-p="3">{t.description}</td><td className="sm" data-p="4">{t.steps.join(", ")}</td><td className="stc"><span className="pill">{t.changesOriginals ? "Changes originals" : "Originals stay"}</span></td></tr>)}</tbody>
        </table></div>
      ) : (<>
        {/* one toolbar (M3a's): the filter buttons on the left, the tag pick and the search; hidden on the empty state */}
        {!empty && <div className="ltools" role="toolbar" aria-label="Workflows">
          <div className="seg" role="group" aria-label="Show workflows">
            {WF_FILTERS.map(([k, t]) => <button key={k} type="button" aria-pressed={filter === k} onClick={() => setFilter(k)}>{counted ? t + " · " + count(counts[k]) : t}</button>)}
          </div>
          {hasTags && <select className="input sm tagsel" aria-label="Tag" value={activeTag} onChange={(e) => setTag(e.target.value)}>
            <option value="">Tag: any</option>{tags.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>}
          <input className="input find" type="search" placeholder="Search workflows" aria-label="Search workflows" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>}
        {wfs.error && wfs.data && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{wfs.error.message}</span></p>}
        {!wfs.loaded ? <WfSkeleton /> : wfs.error && !wfs.data ? <LoadFailed error={wfs.error} reload={wfs.reload} /> : all.length === 0 ? <Empty icon="i-workflow" text="No workflows yet. A workflow says what happens to each file."><button className="btn primary" type="button" onClick={() => navigate("/workflows/guided")}><Icon id="i-plus" />New workflow</button></Empty>
          : rows.length === 0 ? <div className="empty">No workflow matches.</div> : (
          <ul className="lcards" aria-label="Workflow list">
            {rows.map((w) => <WfCard key={w.id} w={w} hasTags={hasTags} setSaid={setSaid} reload={wfs.reload} />)}
          </ul>)}
      </>)}
    </div>
  );
}
