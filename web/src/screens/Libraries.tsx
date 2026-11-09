// V5 Libraries (SCREENS V5, mockups/libraries.html and library.html), on the mock of v1's library routes, which the page
// uses until /api/v2/libraries comes (contract v2, "Libraries: what exists now"). The library page has its General tab
// only; Discovery, Filters, Output and Statistics come later, so no tabs are shown yet. Not shown, because v1 has no
// data or route for them: description and tags (CAN'T YET), schedule, priority, pause and resume, other file types.
import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { addLibrary, checkFolder, firstFiles, libraryItem, patchLibrary, scanLibrary, setLibraryWorkflow, useMock } from "../mock";
import { Link, navigate } from "../router";
import { bytes, clock, count, plural } from "../format";
import type { LibraryItem } from "../types/v1";

function lastScan(t: string | null): string {
  if (!t) return "Not yet";
  const d = new Date(t), day = (x: Date) => x.toDateString(), y = new Date(Date.now() - 864e5);
  return day(d) === day(new Date()) ? clock(d) + " today" : day(d) === day(y) ? "Yesterday " + clock(d) : d.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + clock(d);
}
const folder = (p: string) => p.trim().replace(/(.)\/+$/, "$1"); // "/media/kids/" is "/media/kids"
const names = (n: string[]) => (n.length > 1 ? n.slice(0, -1).join(", ") + " and " + n.at(-1) : n[0]);
function Status({ l }: { l: LibraryItem }) {
  const k = l.status.kind;
  if (k === "scanning") return <span className="st run"><Icon id="i-refresh" /><span>Scanning{l.status.foundSoFar ? " · " + count(l.status.foundSoFar) + " found" : ""}</span></span>;
  if (!l.started) return <span className="st wait"><Icon id="i-pause" /><span>Not started</span></span>;
  if (k === "converting") return <span className="st run"><Icon id="i-play" /><span>Converting {count(l.status.converting)}</span></span>;
  if (k === "waiting_for_worker") return <span className="st wait"><Icon id="i-pause" /><span>Waiting for a worker</span></span>;
  return <span className="st ok"><Icon id="i-check-c" /><span>Watching</span></span>;
}

export function Libraries() {
  const s = useMock();
  const items = s.libraries.map(libraryItem), wfName = (id: string) => s.wf.find((w) => w.id === id)?.name ?? "";
  const [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState("");
  const picked = items.filter((l) => sel.includes(l.id));
  const access = () => {
    const bad = picked.map((l) => [l.name, checkFolder(l.path)] as const).map(([n, r]) => [n, "error" in r ? r.error.message : r.blockers[0]?.message] as const).filter(([, m]) => m);
    setSaid(bad.length ? bad.map(([n, m]) => n + ": " + m).join(" ") : picked.length === 1 ? "The folder of " + picked[0].name + " is fine." : "All " + picked.length + " folders are fine.");
  };
  return (
    <div className="stack">
      <div className="pagehead"><div><h1>Libraries</h1><p className="sub">The folders that are watched for videos.</p></div><span className="sp" />
        <Link className="btn primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link></div>
      <section className="card">
        {sel.length > 0 && (
          <div className="tools" role="group" aria-label="Selected libraries">
            <strong>{sel.length} selected</strong>
            <button className="btn sm" type="button" onClick={() => (picked.forEach((l) => scanLibrary(l.id)), setSaid("Scanning " + plural(picked.length, "library", "libraries") + "."))}><Icon id="i-refresh" />Scan now</button>
            <button className="btn sm" type="button" onClick={access}><Icon id="i-shield" />Check folder access</button>
          </div>)}
        <p className="muted" role="status" style={said ? { padding: "0 16px" } : undefined}>{said}</p>
        {items.length === 0 ? <div className="empty">No libraries yet. <Link to="/libraries/new">Add one</Link> to start.</div> : (
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
                <td><Link to={"/workflows/" + l.flowId}>{wfName(l.flowId)}</Link></td>
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
  const s = useMock();
  const l = id === "new" ? null : s.libraries.find((x) => x.id === id);
  if (id !== "new" && !l) return (
    <div className="stack"><div className="pagehead"><div><h1>Library not found</h1></div></div>
      <div className="card"><div className="bd"><p>There is no library with this address. <Link to="/libraries">See all libraries</Link>.</p></div></div></div>);
  return <Form key={id} l={l ? libraryItem(l) : null} />;
}

// The folder field with its check, and the first 5 files of a folder that passes. Also first run's step 1.
export function useFolder(start: string) {
  const [path, setPath] = useState(start), [checked, setChecked] = useState(folder(start));
  useEffect(() => { const t = setTimeout(() => setChecked(folder(path)), 500); return () => clearTimeout(t); }, [path]); // the real check is a request: not on every key
  const report = checked ? checkFolder(checked) : null;
  const ok = !!report && !("error" in report) && report.ok;
  const first = ok ? firstFiles(checked) : null;
  return { path, setPath, checked, report, ok, first, now: () => setChecked(folder(path)) };
}
export function FolderField({ f, free }: { f: ReturnType<typeof useFolder>; free?: number | null }) {
  const r = f.report;
  return (
    <div className="field"><label htmlFor="lib-path">Folder</label>
      <input id="lib-path" className="input mono" value={f.path} aria-describedby="lib-path-check" placeholder="/media/movies"
        onChange={(e) => f.setPath(e.target.value)} onBlur={f.now} />
      <div id="lib-path-check" className="stack" style={{ gap: 4 }}>
        {!r ? <span className="hint">The folder is checked as you type.</span>
          : "error" in r ? <span className="st bad" role="alert"><Icon id="i-alert" /><span>{r.error.message}</span></span>
          : r.blockers.length ? r.blockers.map((b) => <span key={b.code} className="st bad" role="alert"><Icon id="i-alert" /><span>{b.message}</span></span>)
          : <span className="st ok"><Icon id="i-check-c" /><span>{plural(r.videoCount, "video", "videos")}, {bytes(r.totalBytes)}{free != null ? " · " + bytes(free) + " free" : ""}</span></span>}
        {r && !("error" in r) && r.notes.map((n) => <span key={n.code} className="st warn"><Icon id="i-alert" /><span>{n.message}</span></span>)}
      </div></div>
  );
}
export function FirstFive({ f }: { f: ReturnType<typeof useFolder> }) {
  const r = f.first;
  return (
    <section className="card" aria-labelledby="first-h">
      <div className="hd"><h2 id="first-h">Is this the right folder?</h2></div>
      <div className="bd stack">
        {!r ? <p className="muted">Give a folder that passes the check, and its first 5 videos show here.</p>
          : "error" in r ? <p className="st bad"><Icon id="i-alert" /><span>{r.error.message}</span></p>
          : <>
            <p className="muted">The first 5 files found in <span className="mono">{f.checked}</span>. Nothing is converted by looking.</p>
            {r.items.length === 0 ? <p>No video files found in this folder.</p> : (
              <div className="tscroll"><table className="tbl">
                <thead><tr><th>File</th><th>Video</th><th className="num">Size</th></tr></thead>
                <tbody>{r.items.map((x) => (
                  <tr key={x.path}><td className="mono sm">{x.folder === "." ? x.name : x.folder + "/" + x.name}</td>
                    <td className="nowrap">{x.state === "unreadable" ? "Can't be read" : [x.codec, x.resolution].filter(Boolean).join(" · ") || "Checking..."}</td><td className="num">{bytes(x.bytes)}</td></tr>))}
                </tbody>
              </table></div>)}
          </>}
      </div>
    </section>
  );
}

// The Workflow select: never-published workflows are listed but can't be chosen (V1 87faa4b: 409 workflow_not_published).
export function WorkflowField({ value, onChange, except }: { value: string; onChange: (id: string) => void; except?: string }) {
  const s = useMock();
  const published = s.wf.filter((w) => w.publishedRevisionId != null);
  const others = s.libraries.filter((x) => x.workflowId === value && x.id !== except).map((x) => x.name);
  return (
    <div className="field"><label htmlFor="lib-wf">Workflow</label>
      {s.wf.length === 0 ? <span>No workflows yet. <Link to="/workflows">Make one in Workflows</Link>, then come back.</span> : <>
        <div className="row" style={{ gap: 8 }}>
          <select id="lib-wf" className="input" value={value} onChange={(e) => onChange(e.target.value)} aria-describedby="lib-wf-hint" style={{ flex: 1 }}>
            {!value && <option value="">Choose a workflow</option>}
            {s.wf.map((w) => <option key={w.id} value={w.id} disabled={w.publishedRevisionId == null}>{w.name}{w.publishedRevisionId == null ? " (not published yet)" : ""}</option>)}
          </select>
          {value && <Link to={"/workflows/" + value}>Edit this workflow</Link>}
        </div>
        <span id="lib-wf-hint" className="hint">{published.length === 0 ? "Publish a workflow before a library can use it." : others.length ? "Used by " + names(others) + "." : "No other library uses it."}</span>
      </>}
    </div>
  );
}

function Form({ l }: { l: LibraryItem | null }) {
  const s = useMock();
  const f = useFolder(l?.path ?? "");
  const [name, setName] = useState(l?.name ?? ""), [wf, setWf] = useState(l?.flowId ?? s.wf.find((w) => w.publishedRevisionId != null)?.id ?? "");
  const [error, setError] = useState(""), [said, setSaid] = useState("");
  const moved = !!l && f.checked !== l.path;
  const changed = !l || name !== l.name || moved || wf !== l.flowId;
  const save = () => {
    setSaid(""); setError("");
    if (!l) {
      const r = addLibrary(name, f.checked, wf);
      return "error" in r ? setError(r.error.message) : navigate("/libraries/" + r.id);
    }
    if (name !== l.name || moved) {
      const r = patchLibrary(l.id, { ...(name !== l.name && { name }), ...(moved && { path: f.checked }) });
      if ("error" in r) return setError(r.error.message);
    }
    if (wf !== l.flowId) {
      const r = setLibraryWorkflow(l.id, wf);
      if ("error" in r) return setError(r.error.message);
    }
    setSaid("Saved.");
  };
  return (
    <div className="stack">
      <div className="pagehead">
        <div><h1>{l ? l.name : "Add library"}</h1>{l ? <p className="sub"><Status l={l} /> <span className="mono sm">{l.path}</span></p> : <p className="sub">A folder to watch for videos, and the workflow its files go through.</p>}</div>
        <span className="sp" />
        {l && <button className="btn" type="button" disabled={l.status.kind === "scanning"} onClick={() => scanLibrary(l.id)}><Icon id="i-refresh" />{l.status.kind === "scanning" ? "Scanning..." : "Scan now"}</button>}
      </div>
      {l && !l.started && <p className="muted" role="note">This library isn't started yet: nothing is looked for or converted until you press Scan now.</p>}
      <div className="grid cols2">
        <section className="card" aria-labelledby="gen-h">
          <div className="hd"><h2 id="gen-h">Library</h2></div>
          <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
            <div className="field"><label htmlFor="lib-name">Name</label>
              <input id="lib-name" className="input" required value={name} onChange={(e) => setName(e.target.value)} placeholder="Movies" /></div>
            <FolderField f={f} free={l && !moved ? l.freeBytes : undefined} />
            <div className="field"><span className="lab">File type</span><span>Video</span></div>
            <WorkflowField value={wf} onChange={setWf} except={l?.id} />
            {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
            <p className="muted" role="status">{said}</p>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" type="submit" disabled={!changed || !name.trim() || !wf || ((!l || moved) && !f.ok)}>{l ? "Save" : "Add library"}</button>
              <Link className="btn" to="/libraries">Cancel</Link>
            </div>
          </form>
        </section>
        <FirstFive f={f} />
      </div>
    </div>
  );
}
