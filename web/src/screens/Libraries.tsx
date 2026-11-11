// V5 Libraries (SCREENS V5, mockups/libraries.html and library.html) on v1's library routes (there is no
// /api/v2/libraries yet; contract v2 "Libraries: what exists now"), or the mock with ?mock=1. The library page has its
// General tab only; Discovery, Filters, Output and Statistics come later, so no tabs are shown yet. Not shown, because
// v1 has no data or route for them: description and tags (CAN'T YET), schedule, priority, pause and resume, other file
// types.
import { PageHead } from "../PageHead";
import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed, type Answer } from "../api";
import { addLibrary, checkFolder, firstFiles, listLibraries, listWorkflows, patchLibrary, scanLibrary, setLibraryWorkflow, type WorkflowChoice } from "../libraryApi";
import { Link, navigate } from "../router";
import { bytes, clock, count, plural } from "../format";
import { useData } from "../useData";
import type { FirstFiles, FolderReport, LibraryItem } from "../types/v1";

function lastScan(t: string | null): string {
  if (!t) return "Not yet";
  const d = new Date(t), day = (x: Date) => x.toDateString(), y = new Date(Date.now() - 864e5);
  return day(d) === day(new Date()) ? clock(d) + " today" : day(d) === day(y) ? "Yesterday " + clock(d) : d.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + clock(d);
}
const folder = (p: string) => p.trim().replace(/(.)\/+$/, "$1"); // "/media/kids/" is "/media/kids"
const names = (n: string[]) => (n.length > 1 ? n.slice(0, -1).join(", ") + " and " + n.at(-1) : n[0]);
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
// An action's errors: signed out goes to sign-in (api.ts), anything else is shown.
async function act<T>(f: () => Promise<Answer<T>>, onError: (m: string) => void): Promise<T | null> {
  try { const r = await f(); if (failed(r)) { onError(r.error.message); return null; } return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}
// One status pill per row (S1): accent only while something is happening; normal states are neutral.
function Status({ l }: { l: LibraryItem }) {
  const k = l.status.kind;
  if (k === "scanning") return <span className="pill run"><Icon id="i-refresh" />Scanning{l.status.foundSoFar ? " · " + count(l.status.foundSoFar) + " found" : ""}</span>;
  if (!l.started) return <span className="pill"><Icon id="i-pause" />Not started</span>;
  if (k === "converting") return <span className="pill run"><Icon id="i-play" />Converting {count(l.status.converting)}</span>;
  if (k === "waiting_for_worker") return <span className="pill"><Icon id="i-pause" />Waiting for a worker</span>;
  return <span className="pill"><Icon id="i-check-c" />Watching</span>;
}
const Problem = ({ m }: { m?: string }) => (m ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{m}</span></p> : null);

export function Libraries() {
  const libs = useData(listLibraries, [], 3000), wfs = useData(listWorkflows, [], 10000);
  const items = libs.data ?? [], wfName = (id: string) => wfs.data?.find((w) => w.id === id)?.name ?? "";
  const [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState("");
  const picked = items.filter((l) => sel.includes(l.id));
  const scan = async () => {
    for (const l of picked) await act(() => scanLibrary(l.id), (m) => setSaid(l.name + ": " + m));
    setSaid((s) => s || "Scanning " + plural(picked.length, "library", "libraries") + "."); libs.reload();
  };
  const access = async () => {
    const bad: string[] = [];
    for (const l of picked) { const r = await act(() => checkFolder(l.path), (m) => bad.push(l.name + ": " + m)); if (r && r.blockers[0]) bad.push(l.name + ": " + r.blockers[0].message); }
    setSaid(bad.length ? bad.join(" ") : picked.length === 1 ? "The folder of " + picked[0].name + " is fine." : "All " + picked.length + " folders are fine.");
  };
  return (
    <div className="stack">
      <PageHead icon="i-library" title="Libraries" help="The folders that are watched for videos.">
        <Link className="btn primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link></PageHead>
      <Problem m={libs.error?.message} />
      <section className="card">
        {sel.length > 0 && (
          <div className="tools" role="group" aria-label="Selected libraries">
            <strong>{sel.length} selected</strong>
            <button className="btn sm" type="button" onClick={() => (setSaid(""), scan())}><Icon id="i-refresh" />Scan now</button>
            <button className="btn sm" type="button" onClick={access}><Icon id="i-shield" />Check folder access</button>
          </div>)}
        <p className="muted" role="status" style={said ? { padding: "0 16px" } : undefined}>{said}</p>
        {!libs.loaded ? <div className="empty">Loading...</div> : items.length === 0 ? <div className="empty">No libraries yet. <Link to="/libraries/new">Add one</Link> to start.</div> : (
          <div className="tscroll"><table className="tbl">
            <thead><tr>
              <th><input type="checkbox" aria-label="Select all libraries" checked={sel.length === items.length} onChange={(e) => setSel(e.target.checked ? items.map((l) => l.id) : [])} /></th>
              <th>Name</th><th>Path</th><th>File type</th><th>Workflow</th><th className="num">Files</th><th className="num">Waiting</th><th>Last scan</th><th>Status</th>
            </tr></thead>
            <tbody>{items.map((l) => (
              <tr key={l.id}>
                <td><input type="checkbox" aria-label={"Select " + l.name} checked={sel.includes(l.id)} onChange={(e) => setSel(e.target.checked ? [...sel, l.id] : sel.filter((x) => x !== l.id))} /></td>
                <td><Link to={"/libraries/" + l.id}><strong>{l.name}</strong></Link></td>
                <td className="mono sm">{l.path}</td>
                <td>Video</td>
                <td><Link to={"/workflows/" + l.flowId}>{wfName(l.flowId) || l.settingLabel}</Link></td>
                <td className="num">{count(l.status.videos)}</td>
                <td className="num">{count(l.status.toDo)}</td>
                <td className="nowrap">{lastScan(l.status.lastScan)}</td>
                <td className="nowrap"><Status l={l} /></td>
              </tr>))}
            </tbody>
          </table></div>)}
      </section>
    </div>
  );
}

// /libraries/new and /libraries/:id. The same General form: a new library is POSTed, an existing one PATCHed (name,
// folder) and its workflow PUT.
export function LibraryPage({ id }: { id: string }) {
  const libs = useData(listLibraries, [], 3000), wfs = useData(listWorkflows, [], 10000);
  const l = id === "new" ? null : libs.data?.find((x) => x.id === id);
  if (id !== "new" && !libs.loaded) return <div className="empty">Loading...</div>;
  if (id !== "new" && !l) return (
    <div className="stack"><PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title="Library not found" />
      <Problem m={libs.error?.message} />
      <div className="card"><div className="bd"><p>There is no library with this address. <Link to="/libraries">See all libraries</Link>.</p></div></div></div>);
  return <Form key={id} l={l ?? null} workflows={wfs.data} reload={libs.reload} />;
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
export function FolderField({ f, free }: { f: ReturnType<typeof useFolder>; free?: number | null }) {
  const r = f.report;
  return (
    <div className="field"><label htmlFor="lib-path">Folder</label>
      <input id="lib-path" className="input mono" value={f.path} aria-describedby="lib-path-check" placeholder="/media/movies"
        onChange={(e) => f.setPath(e.target.value)} onBlur={f.now} />
      <div id="lib-path-check" className="stack" style={{ gap: 4 }}>
        {!f.checked ? <span className="hint">The folder is checked as you type.</span>
          : !r ? <span className="hint">Checking the folder...</span>
          : failed(r) ? <span className="st bad" role="alert"><Icon id="i-alert" /><span>{r.error.message}</span></span>
          : r.blockers.length ? r.blockers.map((b) => <span key={b.code} className="st bad" role="alert"><Icon id="i-alert" /><span>{b.message}</span></span>)
          : <span className="st ok"><Icon id="i-check-c" /><span>{plural(r.videoCount, "video", "videos")}, {bytes(r.totalBytes)}{free != null ? " · " + bytes(free) + " free" : ""}</span></span>}
        {r && !failed(r) && r.notes.filter((n) => FOLDER_NOTE(n.code)).map((n) => <span key={n.code} className="st warn"><Icon id="i-alert" /><span>{n.message}</span></span>)}
      </div></div>
  );
}
export function FirstFive({ f }: { f: ReturnType<typeof useFolder> }) {
  const r = f.first;
  return (
    <section className="card" aria-labelledby="first-h">
      <div className="hd"><h2 id="first-h">Is this the right folder?</h2></div>
      <div className="bd stack" aria-live="polite">
        {!f.ok ? <p className="muted">Give a folder that passes the check, and its first 5 videos show here.</p>
          : !r ? <p className="muted">Looking for videos in <span className="mono">{f.checked}</span>...</p>
          : failed(r) ? <p className="st bad"><Icon id="i-alert" /><span>{r.error.message}</span></p>
          : <>
            <p className="muted">The first 5 files found in <span className="mono">{f.checked}</span>. Nothing is converted by looking.</p>
            {r.items.length === 0 ? <p>{r.stillLooking ? "Still looking..." : "No video files found in this folder."}</p> : (
              <div className="tscroll"><table className="tbl">
                <thead><tr><th>File</th><th>Video</th><th className="num">Size</th></tr></thead>
                <tbody>{r.items.map((x) => (
                  <tr key={x.path}><td className="mono sm">{x.folder === "." ? x.name : x.folder + "/" + x.name}</td>
                    <td className="nowrap">{x.state === "unreadable" ? "Can't be read" : [x.codec, x.resolution].filter(Boolean).join(" · ") || "Checking..."}</td><td className="num">{bytes(x.bytes)}</td></tr>))}
                </tbody>
              </table></div>)}
            {r.items.length > 0 && r.stillLooking && <p className="muted sm">Still looking...</p>}
          </>}
      </div>
    </section>
  );
}

// The Workflow select. A workflow that can't be used yet (on the mock: never published, 87faa4b) is listed but disabled.
export function WorkflowField({ value, onChange, workflows, self }: { value: string; onChange: (id: string) => void; workflows?: WorkflowChoice[]; self?: string }) {
  if (!workflows) return <div className="field"><span className="lab">Workflow</span><span className="muted">Loading...</span></div>;
  const others = (workflows.find((w) => w.id === value)?.usedBy ?? []).filter((n) => n !== self);
  return (
    <div className="field"><label htmlFor="lib-wf">Workflow</label>
      {workflows.length === 0 ? <span>No workflows yet. <Link to="/workflows">Make one in Workflows</Link>, then come back.</span> : <>
        <div className="row" style={{ gap: 8 }}>
          <select id="lib-wf" className="input" value={value} onChange={(e) => onChange(e.target.value)} aria-describedby="lib-wf-hint" style={{ flex: 1 }}>
            {!value && <option value="">Choose a workflow</option>}
            {workflows.map((w) => <option key={w.id} value={w.id} disabled={!w.usable}>{w.name}{w.usable ? "" : " (not published yet)"}</option>)}
          </select>
          {value && <Link to={"/workflows/" + value}>Edit this workflow</Link>}
        </div>
        <span id="lib-wf-hint" className="hint">{!workflows.some((w) => w.usable) ? "Publish a workflow before a library can use it." : others.length ? "Used by " + names(others) + "." : "No other library uses it."}</span>
      </>}
    </div>
  );
}

function Form({ l, workflows, reload }: { l: LibraryItem | null; workflows?: WorkflowChoice[]; reload: () => void }) {
  const f = useFolder(l?.path ?? "");
  const [name, setName] = useState(l?.name ?? ""), [wf, setWf] = useState(l?.flowId ?? "");
  const [error, setError] = useState(""), [said, setSaid] = useState(""), [busy, setBusy] = useState(false);
  const pick = wf || (!l && workflows?.find((w) => w.usable)?.id) || ""; // a new library starts on the first usable one
  const moved = !!l && f.checked !== l.path;
  const changed = !l || name !== l.name || moved || pick !== l.flowId;
  const save = async () => {
    setSaid(""); setError(""); setBusy(true);
    try {
      if (!l) { const r = await act(() => addLibrary(name, f.checked, pick), setError); if (r) navigate("/libraries/" + r.id); return; }
      if (name !== l.name || moved) { if (!(await act(() => patchLibrary(l.id, { ...(name !== l.name && { name }), ...(moved && { path: f.checked }) }), setError))) return; }
      if (pick !== l.flowId) { if (!(await act(() => setLibraryWorkflow(l.id, pick), setError))) return; }
      setSaid("Saved."); reload();
    } finally { setBusy(false); }
  };
  const scan = async () => { if (l && (await act(() => scanLibrary(l.id), setError))) reload(); };
  return (
    <div className="stack">
      <PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title={l ? l.name : "Add library"} help={l ? undefined : "A folder to watch for videos, and the workflow its files go through."}>
        {l && <Status l={l} />}
        {l && <button className="btn" type="button" disabled={l.status.kind === "scanning"} onClick={scan}><Icon id="i-refresh" />{l.status.kind === "scanning" ? "Scanning..." : "Scan now"}</button>}
      </PageHead>
      {l && !l.started && <p className="muted" role="note">This library isn't started yet: nothing is looked for or converted until you press Scan now.</p>}
      <div className="grid cols2">
        <section className="card" aria-labelledby="gen-h">
          <div className="hd"><h2 id="gen-h">Library</h2></div>
          <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
            <div className="field"><label htmlFor="lib-name">Name</label>
              <input id="lib-name" className="input" required value={name} onChange={(e) => setName(e.target.value)} placeholder="Movies" /></div>
            <FolderField f={f} free={l && !moved ? l.freeBytes : undefined} />
            <div className="field"><span className="lab">File type</span><span>Video</span></div>
            <WorkflowField value={pick} onChange={setWf} workflows={workflows} self={l?.name} />
            <Problem m={error} />
            <p className="muted" role="status">{said}</p>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" type="submit" disabled={busy || !changed || !name.trim() || !pick || ((!l || moved) && !f.ok)}>{l ? "Save" : "Add library"}</button>
              <Link className="btn" to="/libraries">Cancel</Link>
            </div>
          </form>
        </section>
        <FirstFive f={f} />
      </div>
    </div>
  );
}
