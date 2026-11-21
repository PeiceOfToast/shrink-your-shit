// V5 Libraries (SCREENS V5, mockups/libraries.html, library.html, library-output.html) on /api/v2/libraries (V5b #135:
// Pause/Resume, priority, the folder for new files) and v1's routes for adding, renaming, moving and the workflow, or
// the mock with ?mock=1. The library page has the General, Discovery and Output tabs; Filters and Statistics come later.
// Not shown (CAN'T YET): description and tags, per-library scan settings, other file types.
import { Empty, LoadFailed, PageHead } from "../PageHead";
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { Skeleton } from "../Skeleton";
import { SignedOut, failed, type Answer } from "../api";
import { addLibrary, checkFolder, convertEarlierAgain, getEarlierWorkflow, firstFiles, getLibraryV2, getSettings, listLibraries, listLibrariesV2, listWorkflows, patchLibrary, patchLibraryV2, removeLibrary, scanLibrary, setLibraryWorkflow, type WorkflowChoice } from "../libraryApi";
import { listJobs } from "../jobsApi";
import { Link, navigate, useSearch } from "../router";
import { Browse } from "../FolderPicker";
import { onLive } from "../live";
import { getPublishedDoc, getWorkflow, listWorkflows as listWorkflowDocs } from "../workflowApi";
import { bytes, clock, count, duration, plural } from "../format";
import { useData } from "../useData";
import type { FirstFiles, FolderReport, LibraryItem } from "../types/v1";
import type { Library, Variable } from "../types/v2";

function lastScan(t: string | null): string {
  if (!t) return "Not yet";
  const d = new Date(t), day = (x: Date) => x.toDateString(), y = new Date(Date.now() - 864e5);
  return day(d) === day(new Date()) ? clock(d) + " today" : day(d) === day(y) ? "Yesterday " + clock(d) : d.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + clock(d);
}
const folder = (p: string) => p.trim().replace(/(.)\/+$/, "$1"); // "/media/kids/" is "/media/kids"
// A new library's name (design): the folder's last name with a capital first letter; one taken already gets " 2", " 3"...
export function suggestName(path: string, taken: string[]): string {
  const last = folder(path).split("/").pop() ?? "", base = last.charAt(0).toUpperCase() + last.slice(1);
  if (!base) return "";
  const used = new Set(taken.map((t) => t.toLowerCase()));
  let n = 1, name = base;
  while (used.has(name.toLowerCase())) name = base + " " + ++n;
  return name;
}
const names = (n: string[]) => (n.length > 1 ? n.slice(0, -1).join(", ") + " and " + n.at(-1) : n[0]);
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
// An action's errors: signed out goes to sign-in (api.ts), anything else is shown.
async function act<T>(f: () => Promise<Answer<T>>, onError: (m: string) => void): Promise<T | null> {
  try { const r = await f(); if (failed(r)) { onError(r.error.message); return null; } return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}
// Library priority (design SCREENS V5): High / Normal / Low = 10 / 0 / -10; any other number (set through the API) is
// "Custom (25)" and kept until someone picks a choice.
export const PRIORITIES: [number, string][] = [[10, "High"], [0, "Normal"], [-10, "Low"]];
export const priorityWord = (n: number) => PRIORITIES.find(([v]) => v === n)?.[1] ?? "Custom (" + n + ")";
// enabled is both "started" and Pause/Resume on the server: never scanned and not enabled reads as Not started.
// no_workflow (the server's status): a library with no workflow can't be started, and it wins over every other status.
const noWf = (l: Library) => l.status === "no_workflow";
const NOWF_HINT = "Assign a workflow to start this library.";
const notStarted = (l: Library) => !l.enabled && !l.scan.lastScanAt && !noWf(l);
const paused = (l: Library) => !l.enabled && !!l.scan.lastScanAt && !noWf(l);
// The server pauses a library whose share isn't connected (enabled stays true): it waits, it isn't converting.
const shareWait = (l: Library) => l.status === "paused_share";
const shareName = (l: Library) => l.pausedBy?.shareName ?? "the share";
// One status pill per row (S1): accent only while something is happening; normal states are neutral; Paused warns.
// folder: the list shows "Can't write" for an unwritable folder; the detail header doesn't (its Folder field says it once).
function Status({ l, folder = true }: { l: Library; folder?: boolean }) {
  if (noWf(l)) return <span className="pill warn"><Icon id="i-alert" />No workflow</span>;
  if (folder && !l.writable) return <span className="pill bad"><Icon id="i-alert" />Can't write</span>;
  if (l.scan.state === "scanning") return <span className="pill run"><Icon id="i-refresh" />Scanning{l.scan.progress ? " · " + count(l.scan.progress) + " found" : ""}</span>;
  if (notStarted(l)) return <span className="pill"><Icon id="i-pause" />Not started</span>;
  if (shareWait(l)) return <span className="pill warn"><Icon id="i-pause" />Share not connected yet</span>;
  if (paused(l)) return <span className="pill warn"><Icon id="i-pause" />Paused</span>;
  if (l.status === "path_missing") return <span className="pill bad"><Icon id="i-alert" />Folder missing</span>;
  if (l.counts.running) return <span className="pill run"><Icon id="i-play" />Converting {count(l.counts.running)}</span>;
  return <span className="pill"><Icon id="i-check-c" />Watching</span>;
}
// Pause/Resume's words (design SCREENS V5): Pause confirms nothing, Resume undoes it.
export async function pauseResume(ls: Library[], enabled: boolean, say: (m: string) => void) {
  const bad: string[] = [];
  for (const l of ls) {
    if (noWf(l)) { bad.push(l.name + ": no workflow."); continue; } // can't be started: say so rather than send a 409
    await act(() => patchLibraryV2(l.id, { enabled }), (m) => bad.push(l.name + ": " + m));
  }
  const done = ls.length - bad.length, who = ls.length === 1 ? ls[0].name : plural(done, "library", "libraries");
  say((done ? who + (enabled ? " resumed." : " paused. Files already converting will finish.") : "") + (bad.length ? " Not changed: " + bad.join(" ") : ""));
}
const Problem = ({ m }: { m?: string }) => (m ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{m}</span></p> : null);
// the muted workflow line keeps its first two parts ("Standard · Balanced"); the full summary is the tooltip
const shortTag = (t: string) => t.split(" · ").slice(0, 2).join(" · ");

// the line the Libraries list shows once after a library was removed (G1: "Movies removed. Nothing in ... was changed.")
const flash = { said: "" };

export function Libraries() {
  const libs = useData(listLibrariesV2, [], 3000);
  // user item 3 (design 7.1): each row shows its workflow's summary in full ("Standard · Balanced · ..."), from the workflows list
  const docs = useData(listWorkflowDocs, [], 10000), tag = (l: Library) => { const t = docs.data?.find((w) => w.id === l.workflowId)?.summaryTag ?? ""; return t.trim() === l.workflowName.trim() ? "" : t; }; // review 628ebc3 P2: a workflow named by its summary (the default) shows it once
  useEffect(() => onLive((t) => { if (t === "library.changed") libs.reload(); }), []); // eslint-disable-line react-hooks/exhaustive-deps -- #150: at once
  const [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState(flash.said), [q, setQ] = useState(""), [selecting, setSelecting] = useState(false);
  useEffect(() => { flash.said = ""; }, []);
  const all = libs.data ?? [], items = all.filter((l) => !q || (l.name + " " + l.paths.join(" ")).toLowerCase().includes(q.toLowerCase()));
  const picked = all.filter((l) => sel.includes(l.id)), none = picked.length === 0;
  const scan = async () => {
    setSaid("");
    const go = picked.filter((l) => !noWf(l)), skip = picked.filter(noWf).map((l) => l.name + ": " + NOWF_HINT);
    for (const l of go) await act(() => scanLibrary(l.id), (m) => setSaid(l.name + ": " + m));
    setSaid((s) => [s || (go.length ? "Scanning " + plural(go.length, "library", "libraries") + "." : ""), ...skip].filter(Boolean).join(" ")); libs.reload();
  };
  const access = async () => {
    const bad: string[] = [];
    for (const l of picked) { const r = await act(() => checkFolder(l.paths[0]), (m) => bad.push(l.name + ": " + m)); if (r && r.blockers[0]) bad.push(l.name + ": " + r.blockers[0].message); }
    setSaid(bad.length ? bad.join(" ") : picked.length === 1 ? "The folder of " + picked[0].name + " is fine." : "All " + picked.length + " folders are fine.");
  };
  const needSel = none ? "Select libraries first" : undefined;
  const allNoWf = !none && picked.every(noWf); // a library with no workflow can't be scanned (409)
  const pr = async (enabled: boolean) => { setSaid(""); await pauseResume(picked.filter((l) => l.enabled !== enabled && !notStarted(l)), enabled, setSaid); setSel([]); libs.reload(); };
  // design (SCREENS V5): only the action that applies; both only when the selection mixes paused and watching libraries
  const canPause = picked.some((l) => l.enabled), canResume = picked.some((l) => paused(l) || noWf(l)); // a no-workflow one is named as not changed
  const actions = <>
    <button className="btn sm ghost" type="button" disabled={none || allNoWf} title={allNoWf ? NOWF_HINT : needSel} onClick={scan}><Icon id="i-refresh" />Scan now</button>
    {canPause && <button className="btn sm ghost" type="button" onClick={() => pr(false)}><Icon id="i-pause" />Pause</button>}
    {canResume && <button className="btn sm ghost" type="button" disabled={allNoWf} title={allNoWf ? NOWF_HINT : undefined} onClick={() => pr(true)}><Icon id="i-play" />Resume</button>}
    <button className="btn sm ghost" type="button" disabled={none} title={needSel} onClick={access}><Icon id="i-shield" />Check folder access</button>
  </>;
  return (
    <div className="stack">
      <PageHead icon="i-library" title="Libraries" help="The folders that are watched for videos." />
      <Problem m={libs.data ? libs.error?.message : undefined} />
      {/* one toolbar: Add, then Select; while selecting, All and the bulk actions (on phones they sit under "...") ; filter on the right */}
      <div className="ltools" role="toolbar" aria-label="Libraries">
        <Link className="btn sm primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link>
        <button className="btn sm ghost" type="button" aria-pressed={selecting} onClick={() => (setSelecting(!selecting), setSel([]))}>{selecting ? "Done" : "Select"}</button>
        {selecting && <label className="hit selall"><input type="checkbox" aria-label="Select all libraries" checked={sel.length === all.length} onChange={(e) => setSel(e.target.checked ? all.map((l) => l.id) : [])} /><span className="sm">All</span></label>}
        {selecting && <span className="desk">{actions}</span>}
        {selecting && <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{actions}</div></details>}
        {sel.length > 0 && <span className="muted sm">{sel.length} selected</span>}
        <input className="input find" type="search" placeholder="Filter libraries" aria-label="Filter libraries" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <p className="muted" role="status">{said}</p>
      {!libs.loaded ? <Skeleton rows={4} /> : libs.error && !libs.data ? <LoadFailed error={libs.error} reload={libs.reload} /> : all.length === 0 ? <Empty icon="i-library" text="No libraries yet. A library is a folder that is watched for videos."><Link className="btn primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link></Empty>
        : items.length === 0 ? <div className="empty">No library matches.</div> : (
        // No table header (U3, C1): a 40 px tile, the name and its folder, one chip line of facts, and one status pill.
        <div className={"list rows" + (selecting ? " selecting" : "")}><table className="tbl">
          <tbody>{items.map((l) => (
            <tr key={l.id} className={sel.includes(l.id) ? "sel" : undefined}>
              <td className="selc"><label className="hit"><input type="checkbox" aria-label={"Select " + l.name} checked={sel.includes(l.id)} onChange={(e) => setSel(e.target.checked ? [...sel, l.id] : sel.filter((x) => x !== l.id))} /></label></td>
              <td className="tile"><span className="ltile" aria-hidden="true"><Icon id="i-library" /></span></td>
              <td className="idc">
                <div className="lname"><Link className="ttl" to={"/libraries/" + l.id}>{l.name}</Link><span className="lpath mono" title={l.paths.join(", ")}>{l.paths.join(", ")}</span></div>
                <div className="chips">
                  {noWf(l) ? <span className="mchip warnchip">No workflow · assign one to start it</span> : <Link className="mchip over" to={"/workflows/" + l.workflowId}>{l.workflowName}</Link>}
                  {tag(l) && <span className="mchip" title={tag(l)}>{shortTag(tag(l))}</span>}
                  <span className="mchip">{priorityWord(l.priority)} priority</span>
                  <span className="mchip">{shareWait(l) ? "Waiting for " + shareName(l) + " to connect · " : ""}{count(l.counts.waiting)} waiting{paused(l) ? ", not converting" : ""}</span>
                  <span className="mchip">{l.scan.lastScanAt ? "Scanned " + lastScan(l.scan.lastScanAt) : "Not scanned yet"}</span>
                  <span className="mchip">{plural(l.counts.files, "file", "files")}</span>
                  {l.counts.failed > 0 && <Link className="mchip errchip over" to={"/jobs?tab=failed&libraryId=" + encodeURIComponent(l.id)}>{count(l.counts.failed)} failed</Link>}
                </div>
              </td>
              <td className="stc"><Status l={l} /></td>
            </tr>))}
          </tbody>
        </table></div>)}
    </div>
  );
}

// /libraries/new and /libraries/:id(?tab=discovery|output). The General form: a new library is POSTed, an existing one
// PATCHed (name, folder; v1, which also gives its free space) and its workflow PUT; priority, Pause/Resume and the
// folder for new files are V5b's PATCH /api/v2/libraries/{id}.
// "See its jobs" (V6b): Jobs filtered to this library, on Failed when it has failed jobs, else Completed (the counts follow libraryId)
function SeeJobs({ id }: { id: string }) {
  const c = useData(() => listJobs("failed", "", id, 1), [id]); // only the counts are wanted
  const tab = c.data && !failed(c.data) && c.data.counts.failed > 0 ? "failed" : "completed";
  return <Link className="btn" to={"/jobs?tab=" + tab + "&libraryId=" + encodeURIComponent(id)}><Icon id="i-jobs" />See its jobs</Link>;
}

export function LibraryPage({ id }: { id: string }) {
  const libs = useData(listLibraries, [], 3000), wfs = useData(listWorkflows, [], 10000);
  const v2 = useData(() => (id === "new" ? Promise.resolve(null) : getLibraryV2(id)), [id], 3000);
  useEffect(() => onLive((t, d) => { if (t === "library.changed" && (d as { libraryId?: string })?.libraryId === id) (v2.reload(), libs.reload()); }), [id]); // eslint-disable-line react-hooks/exhaustive-deps
  const l = id === "new" ? null : libs.data?.find((x) => x.id === id);
  if (id !== "new" && !libs.loaded) return <div className="stack"><PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title="Library" /><div className="empty">Loading…</div></div>;
  if (id !== "new" && !l && libs.error && !libs.data) return <div className="stack"><PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title="Library" /><LoadFailed error={libs.error} reload={libs.reload} /></div>;
  if (id !== "new" && !l) return (
    <div className="stack"><PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title="Library not found" />
      <Problem m={libs.error?.message} />
      <div className="card"><div className="bd"><p>There is no library with this address. <Link to="/libraries">See all libraries</Link>.</p></div></div></div>);
  return <Form key={id} l={l ?? null} v={v2.data ?? null} workflows={wfs.data} reload={() => (libs.reload(), v2.reload())} taken={(libs.data ?? []).map((x) => x.name)} />;
}

// The folder field with its check, and the first 5 files of a folder that passes. Also first run's step 1. The check
// and the first 5 are requests, so they wait until typing stops; the first 5 come in as they're found (stillLooking).
export function useFolder(start: string) {
  const [path, setPath] = useState(start), [checked, setChecked] = useState(folder(start));
  const [report, setReport] = useState<Answer<FolderReport> | null>(null), [first, setFirst] = useState<Answer<FirstFiles> | null>(null);
  useEffect(() => { const t = setTimeout(() => setChecked(folder(path)), 500); return () => clearTimeout(t); }, [path]);
  useEffect(() => {
    let live = true;
    setReport(null); setFirst(null);
    if (checked) (async () => {
      try {
        const r = await checkFolder(checked);
        if (!live) return;
        setReport(r);
        if (failed(r) || !r.ok) return;
        for (let i = 0; i < 60; i++) { // about a minute: a huge folder still shows what was found by then
          const f = await firstFiles(checked);
          if (!live) return;
          setFirst(f);
          if (failed(f) || !f.stillLooking) return;
          await sleep(1000);
        }
      } catch (e) { if (!(e instanceof SignedOut)) throw e; }
    })();
    return () => { live = false; };
  }, [checked]);
  const ok = !!report && !failed(report) && report.ok;
  return { path, setPath, checked, report, ok, first, now: () => setChecked(folder(path)) };
}
// no_hw_encoder is about this computer's graphics card, not the folder: it isn't shown here (review 75a84d3, 1).
const FOLDER_NOTE = (code: string) => code !== "no_hw_encoder";
// warn: a saved library's unwritable folder, once, as one line under the field (U3, C13); the list shows it as "Can't write"
export function FolderField({ f, free, warn }: { f: ReturnType<typeof useFolder>; free?: number | null; warn?: string }) {
  const r = f.report;
  return (
    <div className="field"><label htmlFor="lib-path">Folder</label>
      <div className="folderrow"><input id="lib-path" className="input mono" value={f.path} aria-describedby="lib-path-check" placeholder="/media/movies"
        onChange={(e) => f.setPath(e.target.value)} onBlur={f.now} />
        <Browse value={f.path} onPick={(p) => f.setPath(p)} /></div>
      <div id="lib-path-check" className="stack" style={{ gap: 4 }}>
        {!f.checked ? <span className="hint">The folder is checked as you type.</span>
          : !r ? <span className="hint">Checking the folder…</span>
          : failed(r) ? <span className="st bad" role="alert"><Icon id="i-alert" /><span>{r.error.message}</span></span>
          : r.blockers.length ? r.blockers.map((b) => <span key={b.code} className="st bad" role="alert"><Icon id="i-alert" /><span>{b.message}</span></span>)
          : <span className="st ok"><Icon id="i-check-c" /><span>{plural(r.videoCount, "video", "videos")}, {bytes(r.totalBytes)}{free != null ? " · " + bytes(free) + " free" : ""}</span></span>}
        {r && !failed(r) && r.notes.filter((n) => FOLDER_NOTE(n.code)).map((n) => <span key={n.code} className="st warn"><Icon id="i-alert" /><span>{n.message}</span></span>)}
        {warn && <p className="st bad" role="note"><Icon id="i-alert" /><span>{warn} <Link to="/settings/health">How to fix it</Link></span></p>}
      </div></div>
  );
}
// scanned: the library has had its first scan (pass 5 #3). Before it, and in first run, the panel asks whether the folder is right.
export function FirstFive({ f, scanned = false }: { f: ReturnType<typeof useFolder>; scanned?: boolean }) {
  const r = f.first;
  return (
    <section className="card" aria-labelledby="first-h">
      <div className="hd"><h2 id="first-h">{scanned ? "Files in this folder" : "Is this the right folder?"}</h2></div>
      <div className="bd stack" aria-live="polite">
        {!f.ok ? <p className="muted">Give a folder that passes the check, and its first 5 videos show here.</p>
          : !r ? <p className="muted">Looking for videos in <span className="mono">{f.checked}</span>…</p>
          : failed(r) ? <p className="st bad"><Icon id="i-alert" /><span>{r.error.message}</span></p>
          : <>
            <p className="muted">{r.items.length < 5 ? "Files found in" : "The first 5 files found in"} <span className="mono">{f.checked}</span>.{scanned ? "" : " Nothing is converted by looking."}</p>
            {r.items.length === 0 ? <p>{r.stillLooking ? "Still looking…" : "No video files found in this folder."}</p> : (
              <div className="tscroll"><table className="tbl">
                <thead><tr><th>File</th><th>Video</th><th className="num">Size</th></tr></thead>
                <tbody>{r.items.map((x) => (
                  <tr key={x.path}><td className="mono sm">{x.folder === "." ? x.name : x.folder + "/" + x.name}</td>
                    <td className="nowrap">{x.state === "unreadable" ? "Can't be read" : x.codec || x.resolution ? <span className="chips">{[x.codec, x.resolution].filter(Boolean).map((c) => <span className="mchip" key={c}>{c}</span>)}</span> : "Checking…"}</td><td className="num">{bytes(x.bytes)}</td></tr>))}
                </tbody>
              </table></div>)}
            {r.items.length > 0 && r.stillLooking && <p className="muted sm">Still looking…</p>}
          </>}
      </div>
    </section>
  );
}

// The Workflow select. A workflow that can't be used yet (on the mock: never published, 87faa4b) is listed but disabled.
// none: "No workflow yet" (flowId "0") is offered: a new library, or one that already has no workflow. Never for one that has one.
export function WorkflowField({ value, onChange, workflows, self, none }: { value: string; onChange: (id: string) => void; workflows?: WorkflowChoice[]; self?: string; none?: boolean }) {
  if (!workflows) return <div className="field"><span className="lab">Workflow</span><span className="muted">Loading…</span></div>;
  const others = (workflows.find((w) => w.id === value)?.usedBy ?? []).filter((n) => n !== self);
  const hint = value === "0" ? "The library won't start until you assign a workflow." : !workflows.some((w) => w.usable) ? "Publish a workflow before a library can use it." : others.length ? "Used by " + names(others) + "." : "No other library uses it.";
  return (
    <div className="field"><label htmlFor="lib-wf">Workflow</label>
      {workflows.length === 0 && !none ? <span>No workflows yet. <Link to="/workflows">Make one in Workflows</Link>, then come back.</span> : <>
        <div className="row" style={{ gap: 8 }}>
          <select id="lib-wf" className="input" value={value} onChange={(e) => onChange(e.target.value)} aria-describedby="lib-wf-hint" style={{ flex: 1 }}>
            {!value && <option value="">Choose a workflow</option>}
            {none && <option value="0">No workflow yet</option>}
            {workflows.map((w) => <option key={w.id} value={w.id} disabled={!w.usable}>{w.name}{w.usable ? "" : " (not published yet)"}</option>)}
          </select>
          {value && value !== "0" && <Link className="btn sm ghost" to={"/workflows/" + value + "?view=simple"}>Edit this workflow</Link>}
        </div>
        <span id="lib-wf-hint" className="hint">{hint}</span>
        {workflows.length === 0 && <span className="hint">No workflows yet. <Link to="/workflows">Make one in Workflows</Link>.</span>}
      </>}
    </div>
  );
}

function Form({ l, v, workflows, reload, taken }: { l: LibraryItem | null; v: Library | null; workflows?: WorkflowChoice[]; reload: () => void; taken: string[] }) {
  const search = useSearch(), tab = (l && new URLSearchParams(search).get("tab")) || "general";
  const [note, setNote] = useState("");
  const [prio, setPrio] = useState<number | null>(null), priority = prio ?? v?.priority ?? 0; // null: as the server has it
  const f = useFolder(l?.path ?? "");
  // a new library's name follows the folder until it is edited (design)
  const [typed, setTyped] = useState(!!l), [typedName, setName] = useState(l?.name ?? "");
  const name = typed ? typedName : suggestName(f.checked, taken);
  const [wf, setWf] = useState(l?.flowId ?? "");
  const [error, setError] = useState(""), [said, setSaid] = useState(""), [busy, setBusy] = useState(false);
  const noWfNow = v?.status === "no_workflow"; // a library with no workflow yet: its select shows "No workflow yet" until one is chosen
  const cur = l ? (noWfNow ? "0" : l.flowId) : "";
  const pick = wf || (!l && workflows?.find((w) => w.usable)?.id) || (noWfNow ? "0" : "") || ""; // a new library starts on the first usable one
  const moved = !!l && f.checked !== l.path;
  const prioChanged = !!v && priority !== v.priority;
  const changed = !l || name !== l.name || moved || pick !== cur || prioChanged;
  const save = async () => {
    setSaid(""); setError(""); setBusy(true);
    try {
      if (!l) { const r = await act(() => addLibrary(name, f.checked, pick), setError); if (r) navigate("/libraries/" + r.id); return; }
      if (name !== l.name || moved) { if (!(await act(() => patchLibrary(l.id, { ...(name !== l.name && { name }), ...(moved && { path: f.checked }) }), setError))) return; }
      if (pick !== cur) { if (!(await act(() => setLibraryWorkflow(l.id, pick), setError))) return; }
      if (prioChanged) { if (!(await act(() => patchLibraryV2(l.id, { priority }), setError))) return; setPrio(null); }
      setSaid("Saved."); reload();
    } finally { setBusy(false); }
  };
  const scan = async () => { if (l && (await act(() => scanLibrary(l.id), setError))) reload(); };
  return (
    <div className="stack">
      <PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title={l ? l.name : "Add library"} help={l ? undefined : "A folder to watch for videos, and the workflow its files go through."}>
        {v && <Status l={v} folder={false} />}
        {v && v.enabled && <button className="btn" type="button" onClick={() => pauseResume([v], false, setNote).then(reload)}><Icon id="i-pause" />Pause</button>}
        {v && paused(v) && <button className="btn" type="button" onClick={() => pauseResume([v], true, setNote).then(reload)}><Icon id="i-play" />Resume</button>}
        {l && <SeeJobs id={l.id} />}
        {l && <button className="btn" type="button" disabled={noWfNow || v?.scan.state === "scanning"} aria-describedby={noWfNow ? "lib-nowf-hint" : undefined} onClick={scan}><Icon id="i-refresh" />{v?.scan.state === "scanning" ? "Scanning…" : "Scan now"}</button>}
      </PageHead>
      {note && <p className="muted" role="status">{note}</p>}
      {noWfNow && <p className="muted" role="note" id="lib-nowf-hint">{NOWF_HINT}</p>}
      {v && notStarted(v) && <p className="muted" role="note">This library isn't started yet: nothing is looked for or converted until you press Scan now.</p>}
      {v && paused(v) && <p className="muted" role="note">While paused, its files wait in the queue and aren't converted. Scans add no new files. Files already converting finish.</p>}
      {v && shareWait(v) && <p className="muted" role="note">Share not connected yet. Its files wait in the queue and aren't converted until {shareName(v)} connects.</p>}
      {l && v && !noWfNow && <EarlierWorkflow v={v} />}
      {l && <nav className="tabs" aria-label="Library sections">
        {[["general", "General"], ["discovery", "Discovery"], ["output", "Output"]].map(([k, t]) =>
          <Link key={k} to={"/libraries/" + l.id + (k === "general" ? "" : "?tab=" + k)} aria-current={tab === k ? "page" : undefined}>{t}</Link>)}
      </nav>}
      {tab === "discovery" ? <Discovery every={v?.discovery.scanEverySeconds} /> : tab === "output" ? (v ? (v.status === "no_workflow" ? <NoWorkflowOutput /> : <Output v={v} reload={reload} />) : <div className="empty">Loading…</div>) :
      <div className="grid cols2">
        <section className="card" aria-labelledby="gen-h">
          <div className="hd"><h2 id="gen-h">Library</h2></div>
          <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
            <div className="field"><label htmlFor="lib-name">Name</label>
              <input id="lib-name" className="input" required value={name} onChange={(e) => (setName(e.target.value), setTyped(true))} placeholder="Movies" /></div>
            <FolderField f={f} free={l && !moved ? l.freeBytes : undefined} warn={v && !v.writable && !moved ? v.unwritableMessage || "Can't write to this folder." : undefined} />
            <div className="field"><span className="lab">File type</span><span>Video</span></div>
            <WorkflowField value={pick} onChange={setWf} workflows={workflows} self={l?.name} none={!l || noWfNow} />
            {v && <div className="field"><label htmlFor="lib-prio">Priority</label>
              <select id="lib-prio" className="input" value={priority} onChange={(e) => setPrio(Number(e.target.value))} aria-describedby="lib-prio-h">
                {PRIORITIES.map(([n, w]) => <option key={n} value={n}>{w}</option>)}
                {!PRIORITIES.some(([n]) => n === priority) && <option value={priority}>{priorityWord(priority)}</option>}
              </select>
              <span className="hint" id="lib-prio-h">High: this library's files are converted before other libraries'. Low: after them. A single file set to First or Last in Jobs still goes first or last.</span></div>}
            <Problem m={error} />
            <p className="muted" role="status">{said}</p>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" type="submit" disabled={busy || !changed || !name.trim() || !pick || ((!l || moved) && !f.ok)}>{l ? "Save" : "Add library"}</button>
              <Link className="btn" to="/libraries">Cancel</Link>
            </div>
          </form>
        </section>
        {l && v && <LibraryVars v={v} reload={reload} />}
        <FirstFive f={f} scanned={!!v?.scan.lastScanAt && f.checked === l?.path} />
        {l && v && <RemoveSection l={l} v={v} />}
      </div>}
    </div>
  );
}

// Remove a library (one-UI gap G1; design SCREENS, 19:08Z): at the bottom of the General tab. The question says only
// what applies (waiting files, the file being converted, converted files); Keep it has the focus and Escape. After:
// the Libraries list, with the line saying nothing in the folder changed. A 409 (busy) keeps the question, in its words.
// Contract 088aa569: a quiet line, never a popup, when files were converted with an earlier workflow; the dialog shows
// what the server counted (possible / not possible with reasons) and only its button queues them.
function EarlierWorkflow({ v }: { v: Library }) {
  const id = v.id, e = useData(() => getEarlierWorkflow(id), [id]), opener = useRef<HTMLButtonElement>(null), status = useRef<HTMLParagraphElement>(null);
  const [ask, setAsk] = useState(false), [busy, setBusy] = useState(false), [err, setErr] = useState(""), [said, setSaid] = useState("");
  const wf = useData(() => (ask ? getWorkflow(v.workflowId) : Promise.resolve(null)), [ask, v.workflowId]); // the title names it when known (design 02:49Z)
  const wfName = wf.data && !failed(wf.data) ? " with " + wf.data.name + (wf.data.version ? " v" + wf.data.version : "") : "";
  const n = e.data && !failed(e.data) ? e.data : null; // an error (an older server) shows nothing: there is nothing to offer
  const back = () => (setAsk(false), opener.current?.focus());
  const go = async () => {
    setErr(""); setBusy(true);
    try {
      const r = await act(() => convertEarlierAgain(id), setErr);
      if (!r) return;
      setAsk(false); setTimeout(() => status.current?.focus()); // the button may be gone once the GET is re-read (review #445)
      setSaid([plural(r.queued, "file", "files") + " put back in the queue.", ...r.notPossible.map((x) => x.count + (x.count === 1 ? " wasn't: " : " weren't: ") + x.message)].join(" "));
      e.reload();
    } finally { setBusy(false); }
  };
  const done = <p ref={status} tabIndex={-1} className="muted" role="status" style={said ? { margin: 0 } : { display: "contents" }}>{said}</p>; // always present so it is announced
  if (!n?.earlier) return done;
  // none can be converted: say why, with no link (design 02:49Z); the sentence assumes deleted originals, else the server's words
  if (!n.possible) return <><p className="muted" role="note">{plural(n.earlier, "file was", "files were")} converted with an earlier workflow. {n.notPossible.every((x) => x.code === "original_deleted")
    ? (n.earlier === 1 ? "Its original was deleted, so it can't" : "Their originals were deleted, so they can't") + " be converted again." : "They can't be converted again: " + n.notPossible.map((x) => x.message).join(" ")}</p>{done}</>;
  return <div className="stack">
    <p className="muted" role="note">{plural(n.earlier, "file was", "files were")} converted with an earlier workflow. <button ref={opener} className="linkbtn" style={{ color: "var(--accent-text)" }} type="button" aria-expanded={ask} onClick={() => (setErr(""), setAsk(!ask))}>Convert them again…</button></p>
    {ask && <div role="alertdialog" aria-labelledby="ea-q" aria-describedby="ea-d" className="stack" onKeyDown={(k) => k.key === "Escape" && (k.stopPropagation(), back())}
      style={{ padding: 12, border: "1px solid var(--border-strong)", borderRadius: "var(--radius)", background: "var(--raised)" }}>
      <strong id="ea-q">Convert {plural(n.possible, "file", "files")} again{wfName}?</strong>
      <div id="ea-d"><p style={{ margin: "0 0 6px" }}>Each starts from its original with this library's current workflow, never from the new file. The earlier new file is replaced once the new one passes its check.{v.output.policy === "replace" && " The originals come from the trash, and each one's days in the trash start again."}</p>
        {n.notPossible.length > 0 && <ul style={{ margin: 0, paddingLeft: 18 }}>
          {n.notPossible.map((x) => <li key={x.code}>{plural(x.count, "file", "files")} can't: {x.message}</li>)}
        </ul>}</div>
      {err && <p className="st bad" role="alert" style={{ whiteSpace: "normal" }}><Icon id="i-alert" /><span>{err}</span></p>}
      <div className="row"><button className="btn primary" type="button" autoFocus disabled={busy} onClick={go}>Convert {plural(n.possible, "file", "files")} again</button>
        <button className="btn ghost" type="button" onClick={back}>Cancel</button></div>
    </div>}
    {done}
  </div>;
}

function RemoveSection({ l, v }: { l: LibraryItem; v: Library }) {
  const [ask, setAsk] = useState(false), [err, setErr] = useState(""), [busy, setBusy] = useState(false), opener = useRef<HTMLButtonElement>(null);
  const back = () => (setAsk(false), opener.current?.focus()); // Escape and Keep it: the focus goes back to the button that opened the question (review d2e81ef P3)
  const running = useData(() => (ask ? listJobs("running", "") : Promise.resolve(null)), [ask]);
  const conv = (running.data?.items ?? []).filter((j) => j.libraryId === l.id);
  const go = async () => {
    setErr(""); setBusy(true);
    try {
      const r = await removeLibrary(l.id);
      if (failed(r)) return setErr(r.error.message);
      flash.said = l.name + " removed. Nothing in " + l.path + " was changed.";
      navigate("/libraries");
    } catch (e) { if (!(e instanceof SignedOut)) throw e; } finally { setBusy(false); }
  };
  return (
    <section className="card w2" aria-labelledby="rm-h">
      <div className="hd"><h2 id="rm-h">Remove this library</h2></div>
      <div className="bd stack">
        <p className="muted">Stops watching this folder. Nothing in it is changed or deleted.</p>
        <div className="row"><button ref={opener} className="btn danger" type="button" aria-expanded={ask} onClick={() => (setErr(""), setAsk(!ask))}>Remove library…</button></div>
        {ask && <div role="alertdialog" aria-labelledby="rm-q" aria-describedby="rm-d" className="stack" onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), back())}
          style={{ padding: 12, border: "1px solid var(--border-strong)", borderRadius: "var(--radius)", background: "var(--raised)" }}>
          <strong id="rm-q">Remove {l.name}?</strong>
          <div id="rm-d"><p style={{ margin: "0 0 6px" }}>Shrink Your Shit stops watching {l.path}. Nothing in the folder is changed or deleted.</p>
            <ul style={{ margin: 0, paddingLeft: 18 }}>
              {v.counts.waiting > 0 && <li>{v.counts.waiting === 1 ? "1 waiting file is" : count(v.counts.waiting) + " waiting files are"} taken off the queue.</li>}
              {conv.map((j) => <li key={j.id}>{j.name} is being converted: it is stopped, and its original isn't touched.</li>)}
              {v.counts.completed > 0 && <li>Converted files stay as they are. Kept originals can still be got back from Jobs until their date.</li>}
            </ul></div>
          {err && <p className="st bad" role="alert" style={{ whiteSpace: "normal" }}><Icon id="i-alert" /><span>{err}</span></p>}
          <div className="row"><button className="btn danger" type="button" disabled={busy} onClick={go}>{err ? "Try again" : "Remove library"}</button>
            <button className="btn primary" type="button" autoFocus onClick={back}>Keep it</button></div>
        </div>}
      </div>
    </section>
  );
}

// Discovery (SCREENS V5): how often libraries are looked at is one setting for all of them (#136); the line shows the
// library's effective interval (V5b discovery.scanEverySeconds, else settings) and links to where it is changed. No
// line when the server doesn't say (never a made-up number). The tab's other settings are CAN'T YET.
function Discovery({ every }: { every?: number }) {
  const st = useData(getSettings, [], 30000), n = every ?? st.data?.scanEvery;
  return (
    <section className="card" aria-labelledby="disc-h">
      <div className="hd"><h2 id="disc-h">Discovery</h2></div>
      <div className="bd">{n ? <p>Libraries are checked for new files every {duration(n)}. <Link to="/settings/processing">Change it in Settings &gt; Processing.</Link></p>
        : <p className="muted">{st.loaded ? "This server doesn't say how often it looks for new files." : "Loading…"}</p>}</div>
    </section>);
}

// Output (SCREENS V5, mockups/library-output.html): read-only, from the workflow's Save the result; the one place for
// this library's own folder for new files (overrideFolder). The server's problem, if any, in its words.
const POLICY: Record<string, string> = {
  replace: "Replace the original. The original is kept for a while, then deleted. This frees space.",
  beside: "Put the new file next to the original. Both stay; nothing is deleted.",
  separate_folder: "Save to a separate folder. Originals are left in place, so no space is freed until you remove them.",
};
// No workflow: nothing sets where results go yet, so Output (which reads v.output and the workflow) never mounts (review2 #509).
const NoWorkflowOutput = () => <section className="card" aria-labelledby="out-h"><div className="hd"><h2 id="out-h">Where results go</h2></div><div className="bd"><p className="muted">A workflow sets where results go. Assign one to this library first.</p></div></section>;
function Output({ v, reload }: { v: Library; reload: () => void }) {
  const o = v.output, [dir, setDir] = useState(o.overrideFolder ?? ""), [said, setSaid] = useState(""), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  // design (SCREENS V5, "Own folder with a One-folder-for-all workflow"): the library's own folder only applies with Next
  // to each library; the published workflow's Save step says which (its id links to that step)
  const pub = useData(() => getPublishedDoc(v.workflowId), [v.workflowId]);
  const commit = pub.data?.nodes.find((n) => n.type === "output.commit"), oneFolder = commit?.config.folderMode === "one_folder";
  const stepLink = "/workflows/" + v.workflowId + (commit ? "?step=" + encodeURIComponent(commit.id) : "");
  const save = async (to: string | null) => {
    setSaid(""); setError(""); setBusy(true);
    try { if (await act(() => patchLibraryV2(v.id, { overrideFolder: to }), setError)) { setSaid(to ? "Saved. New files for " + v.name + " go to " + to + "." : "Saved. " + v.name + " uses the workflow's folder again."); if (!to) setDir(""); reload(); } }
    finally { setBusy(false); }
  };
  return (
    <section className="card" aria-labelledby="out-h">
      <div className="hd"><h2 id="out-h">Where results go</h2></div>
      <div className="bd stack">
        <p>Set by the workflow <Link to={"/workflows/" + v.workflowId}>{v.workflowName}</Link>: {o.policy ? POLICY[o.policy] ?? o.policy : "it can't be read right now."}</p>
        {o.folder && <p>New files go to <span className="mono" style={{ overflowWrap: "anywhere" }}>{o.folder}</span>.</p>}
        {o.problem && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{o.problem.message}</span></p>}
        <div className="row"><Link className="btn sm" to={"/workflows/" + v.workflowId}>Edit in workflow</Link></div>
        {o.policy === "separate_folder" && oneFolder && <>
          <p>This workflow puts every library's new files in one folder. To give this library its own folder, change the workflow's <Link to={stepLink}>Output step</Link> to Next to each library.</p>
          {o.overrideFolder && <div className="row" style={{ gap: 8, alignItems: "center" }}><p className="muted sp" style={{ margin: 0 }}>Its own folder <span className="mono" style={{ overflowWrap: "anywhere" }}>{o.overrideFolder}</span> is saved but not used with this workflow.</p>
            <button className="btn sm" type="button" disabled={busy} onClick={() => save(null)}>Remove</button></div>}
          {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
          <p className="muted" role="status">{said}</p></>}
        {o.policy === "separate_folder" && !oneFolder && pub.loaded && <form className="stack" onSubmit={(e) => (e.preventDefault(), save(dir.trim() || null))}>
          <div className="field"><label htmlFor="lib-over">Use a different folder for this library</label>
            <div className="folderrow"><input id="lib-over" className="input mono" value={dir} placeholder={v.paths[0] + " (shrunk)"} onChange={(e) => setDir(e.target.value)} aria-describedby="lib-over-h" />
              <Browse value={dir} start={v.paths[0] ? v.paths[0].replace(/[\\/][^\\/]*[\\/]?$/, "") || "/" : ""} onPick={setDir} /></div>
            <span className="hint" id="lib-over-h">This is the one place to override the folder for {v.name} only.</span></div>
          {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
          <p className="muted" role="status">{said}</p>
          <div className="row" style={{ gap: 8 }}>
            <button className="btn primary" type="submit" disabled={busy || dir.trim() === (o.overrideFolder ?? "")}>Save</button>
            {o.overrideFolder && <button className="btn" type="button" disabled={busy} onClick={() => save(null)}>Use the workflow's folder</button>}
          </div>
        </form>}
      </div>
    </section>);
}

// W3 (design): "Settings this library can change". The workflow's variables with templateParam, read from its published
// revision (the one the server checks). A library's own value wins; a row with none uses the workflow's value. Saving sends
// the whole list (PATCH variables), so a cleared row is simply left out.
type Draft = string | boolean | null; // null: the workflow's value
const WORKFLOW_VALUE = (x: Variable) => (x.value === undefined || x.value === null || x.value === "" ? "empty" : typeof x.value === "boolean" ? (x.value ? "yes" : "no") : String(x.value));
const draftOf = (value: unknown): Draft => (value === undefined ? null : typeof value === "boolean" ? value : String(value));
// the value to send: null when the row uses the workflow's value (an empty text box says the same); a number that doesn't
// parse goes as text, so the server refuses it as variable_wrong_type by the field
function valueOf(x: Variable, d: Draft): unknown {
  if (d === null) return null;
  if (typeof d === "boolean") return d;
  const t = d.trim();
  if (t === "") return null;
  return x.type === "number" && /^-?\d+(\.\d+)?$/.test(t) ? Number(t) : t;
}
const sortedJSON = (l: { name: string; value: unknown }[]) => JSON.stringify([...l].sort((a, b) => a.name.localeCompare(b.name)));

function LibraryVars({ v, reload }: { v: Library; reload: () => void }) {
  const noWf = v.status === "no_workflow";
  const pub = useData(() => (noWf ? Promise.resolve(null) : getPublishedDoc(v.workflowId)), [v.workflowId, noWf]);
  // the status lives here, not in the form: a save's reload remounts the form (keyed on the saved values), and "Saved." must survive it
  const [said, setSaid] = useState("");
  useEffect(() => setSaid(""), [v.workflowId]);
  const head =<div className="hd"><h2 id="lv-h">Settings this library can change</h2></div>;
  if (noWf) return <section className="card" aria-labelledby="lv-h">{head}<div className="bd"><p className="muted">Pick a workflow first.</p></div></section>;
  if (!pub.loaded) return <section className="card" aria-labelledby="lv-h">{head}<div className="bd"><p className="muted">Loading…</p></div></section>;
  if (pub.error && !pub.data) return <section className="card" aria-labelledby="lv-h">{head}<div className="bd"><p className="st bad" role="alert"><Icon id="i-alert" /><span>{pub.error.message}</span></p></div></section>;
  const vars = (pub.data?.variables ?? []).filter((x) => x.templateParam && x.type !== "secret"); // the server refuses a library value for a secret
  if (!vars.length) return <section className="card" aria-labelledby="lv-h">{head}<div className="bd"><p className="muted">This library's workflow has nothing a library can change.</p></div></section>;
  // keyed by the saved values: a successful save remounts from the list the server answered with
  return <LibraryVarsForm key={v.workflowId + sortedJSON(v.variables)} v={v} vars={vars} reload={reload} said={said} setSaid={setSaid} />;
}

function LibraryVarsForm({ v, vars, reload, said, setSaid }: { v: Library; vars: Variable[]; reload: () => void; said: string; setSaid: (s: string) => void }) {
  const [draft, setDraft] = useState<Record<string, Draft>>(() => Object.fromEntries(vars.map((x) => [x.name, draftOf(v.variables.find((o) => o.name === x.name)?.value)])));
  const [errs, setErrs] = useState<Record<string, string>>({}), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  const set = (name: string, d: Draft) => setDraft((all) => ({ ...all, [name]: d }));
  const toSend = vars.flatMap((x) => { const val = valueOf(x, draft[x.name] ?? null); return val === null ? [] : [{ name: x.name, value: val }]; });
  const changed = sortedJSON(toSend) !== sortedJSON(v.variables.map((o) => ({ name: o.name, value: o.value })));
  const save = async () => {
    setSaid(""); setError(""); setErrs({}); setBusy(true);
    try {
      const r = await patchLibraryV2(v.id, { variables: toSend });
      if (failed(r)) {
        const issues = (r.error as { issues?: { name?: string; message: string }[] }).issues ?? [];
        const byField: Record<string, string> = {}, rest: string[] = [];
        for (const i of issues) (i.name && vars.some((x) => x.name === i.name) ? (byField[i.name] ??= i.message) : rest.push(i.message));
        setErrs(byField); setError(rest.length || !Object.keys(byField).length ? [...rest, ...(Object.keys(byField).length ? [] : [r.error.message])].join(" ") : "");
        const first = vars.find((x) => byField[x.name]);
        if (first) document.getElementById("lv-" + first.name)?.focus();
        return;
      }
      setSaid(toSend.length ? "Saved." : "Saved. " + v.name + " uses the workflow's values.");
      reload();
    } catch (e) { if (e instanceof SignedOut) return; throw e; }
    finally { setBusy(false); }
  };
  return (
    <section className="card" aria-labelledby="lv-h">
      <div className="hd"><h2 id="lv-h">Settings this library can change</h2></div>
      <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
        {vars.map((x) => {
          const d = draft[x.name] ?? null, id = "lv-" + x.name, hint = id + "-h", err = id + "-err";
          const describedBy = [hint, errs[x.name] ? err : ""].filter(Boolean).join(" ");
          const label = <>{x.description ? x.description + " " : ""}<span className="mono">{"{{" + x.name + "}}"}</span></>;
          const input = x.type === "bool" ? (
            <label className="row" style={{ gap: 8, alignItems: "center" }}>
              <input id={id} type="checkbox" role="switch" checked={typeof d === "boolean" ? d : !!x.value} onChange={(e) => set(x.name, e.target.checked)} aria-describedby={describedBy}
                aria-labelledby={`${id}-name ${id}-state`} />
              <span id={id + "-state"}>{typeof d === "boolean" ? (d ? "Yes" : "No") : "Workflow's value"}</span>
            </label>
          ) : x.type === "enum" ? (
            <select id={id} className="input" value={typeof d === "string" ? d : ""} onChange={(e) => set(x.name, e.target.value || null)} aria-describedby={describedBy} aria-invalid={!!errs[x.name]}>
              <option value="">Workflow's value</option>
              {(x.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
            </select>
          ) : (
            <input id={id} className={"input" + (x.type === "path" ? " mono" : "")} type="text" inputMode={x.type === "number" ? "numeric" : undefined}
              value={typeof d === "string" ? d : typeof d === "boolean" ? String(d) : ""} placeholder={WORKFLOW_VALUE(x)}
              onChange={(e) => set(x.name, e.target.value)} aria-describedby={describedBy} aria-invalid={!!errs[x.name]} />
          );
          return (
            <div className="field" key={x.name}>
              {x.type === "bool" ? <span className="lab" id={id + "-name"}>{label}</span> : <label htmlFor={id}>{label}</label>}
              {input}
              <span className="hint" id={hint}>Workflow's value: {WORKFLOW_VALUE(x)}.{x.type === "bool" || x.type === "enum" ? "" : " Leave it empty to use the workflow's value."}</span>
              {errs[x.name] && <p className="st bad" id={err} role="alert"><Icon id="i-alert" /><span>{errs[x.name]}</span></p>}
              {/* aria-disabled, not disabled: a disabled button drops focus to the page; clicks are ignored while it is disabled */}
              <div><button className="btn sm" type="button" aria-disabled={busy || d === null} aria-label={"Use the workflow's value for {{" + x.name + "}}"} onClick={() => { if (!busy && d !== null) set(x.name, null); }}>Use the workflow's value</button></div>
            </div>
          );
        })}
        {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
        <p className="muted" role="status">{said}</p>
        <div className="row" style={{ gap: 8 }}><button className="btn primary" type="submit" disabled={busy || !changed}>Save settings</button></div>
      </form>
    </section>
  );
}
