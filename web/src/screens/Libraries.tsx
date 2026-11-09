// V5 Libraries (SCREENS V5, mockups/libraries.html and library.html), on the mock. The library page has its General
// tab only; Discovery, Filters, Output and Statistics come in a later step, so no tabs are shown yet. Not shown because
// contract v2 has no data for them: description, tags, free space, other file types, and changing an existing library's
// folder (PATCH /libraries/{id} takes no paths).
import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { addLibrary, checkFolder, firstFiles, scanLibrary, setLibrary, useMock } from "../mock";
import { Link, navigate } from "../router";
import { bytes, clock, count, plural } from "../format";
import type { Library } from "../types/v2";

function lastScan(t: string | null): string {
  if (!t) return "Not yet";
  const d = new Date(t), day = (x: Date) => x.toDateString(), y = new Date(Date.now() - 864e5);
  return day(d) === day(new Date()) ? clock(d) + " today" : day(d) === day(y) ? "Yesterday " + clock(d) : d.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + clock(d);
}
const folder = (p: string) => p.trim().replace(/(.)\/+$/, "$1"); // "/media/kids/" is "/media/kids"
const priority = (p: number) => (p > 0 ? "High" : p < 0 ? "Low" : "Normal");
function Status({ l }: { l: Library }) {
  if (l.scan.state === "scanning") return <span className="st run"><Icon id="i-refresh" /><span>Scanning{l.scan.progress != null ? " " + l.scan.progress + "%" : ""}</span></span>;
  if (l.status === "path_missing") return <span className="st bad"><Icon id="i-alert" /><span>Folder missing</span></span>;
  if (l.status === "not_readable") return <span className="st bad"><Icon id="i-alert" /><span>Can't read the folder</span></span>;
  if (!l.enabled) return <span className="st"><Icon id="i-pause" /><span>Paused</span></span>;
  return <span className="st ok"><Icon id="i-check-c" /><span>Watching</span></span>;
}

export function Libraries() {
  const s = useMock();
  const [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState("");
  const picked = s.libraries.filter((l) => sel.includes(l.id));
  const each = (f: (l: Library) => void, words: string) => { picked.forEach(f); setSaid(words); };
  const access = () => {
    const bad = picked.flatMap((l) => l.paths.map((p) => [l.name, checkFolder(p, l.id).blockers[0]?.message] as const)).filter(([, m]) => m);
    setSaid(bad.length ? bad.map(([n, m]) => n + ": " + m).join(" ") : picked.length === 1 ? "The folder of " + picked[0].name + " can be read." : "All " + picked.length + " folders can be read.");
  };
  return (
    <div className="stack">
      <div className="pagehead"><div><h1>Libraries</h1><p className="sub">The folders that are watched for videos.</p></div><span className="sp" />
        <Link className="btn primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link></div>
      <section className="card">
        {sel.length > 0 && (
          <div className="tools" role="group" aria-label="Selected libraries">
            <strong>{sel.length} selected</strong>
            <button className="btn sm" type="button" onClick={() => each((l) => scanLibrary(l.id), "Scanning " + plural(picked.length, "library", "libraries") + ".")}><Icon id="i-refresh" />Scan now</button>
            <button className="btn sm" type="button" onClick={() => each((l) => setLibrary(l.id, { enabled: false }), "Paused: no new files are looked for or queued.")}><Icon id="i-pause" />Pause</button>
            <button className="btn sm" type="button" onClick={() => each((l) => setLibrary(l.id, { enabled: true }), "Resumed.")}><Icon id="i-play" />Resume</button>
            <button className="btn sm" type="button" onClick={access}><Icon id="i-shield" />Check folder access</button>
          </div>)}
        <p className="muted" role="status" style={said ? { padding: "0 16px" } : undefined}>{said}</p>
        {s.libraries.length === 0 ? <div className="empty">No libraries yet. <Link to="/libraries/new">Add one</Link> to start.</div> : (
          <div className="tscroll"><table className="tbl">
            <thead><tr>
              <th><input type="checkbox" aria-label="Select all libraries" checked={sel.length === s.libraries.length} onChange={(e) => setSel(e.target.checked ? s.libraries.map((l) => l.id) : [])} /></th>
              <th>Name</th><th>Path</th><th>File type</th><th>Workflow</th><th className="num">Files</th><th className="num">Waiting</th><th>Last scan</th><th>Schedule</th><th>Priority</th><th>Status</th>
            </tr></thead>
            <tbody>{s.libraries.map((l) => (
              <tr key={l.id}>
                <td><input type="checkbox" aria-label={"Select " + l.name} checked={sel.includes(l.id)} onChange={(e) => setSel(e.target.checked ? [...sel, l.id] : sel.filter((x) => x !== l.id))} /></td>
                <td><Link to={"/libraries/" + l.id}><strong>{l.name}</strong></Link></td>
                <td className="mono sm">{l.paths.join(", ")}</td>
                <td>Video</td>
                <td><Link to={"/workflows/" + l.workflowId}>{l.workflowName}</Link></td>
                <td className="num">{count(l.counts.files)}</td>
                <td className="num">{count(l.counts.waiting)}</td>
                <td className="nowrap">{lastScan(l.scan.lastScanAt)}</td>
                <td>{l.scan.scheduleLabel}</td>
                <td>{priority(l.priority)}</td>
                <td className="nowrap"><Status l={l} /></td>
              </tr>))}
            </tbody>
          </table></div>)}
      </section>
    </div>
  );
}

// /libraries/new and /libraries/:id. The same General form: a new library is POSTed, an existing one PATCHed.
export function LibraryPage({ id }: { id: string }) {
  const s = useMock();
  const l = id === "new" ? null : s.libraries.find((x) => x.id === id);
  if (id !== "new" && !l) return (
    <div className="stack"><div className="pagehead"><div><h1>Library not found</h1></div></div>
      <div className="card"><div className="bd"><p>There is no library with this address. <Link to="/libraries">See all libraries</Link>.</p></div></div></div>);
  return <Form key={id} l={l ?? null} />;
}

function Form({ l }: { l: Library | null }) {
  const s = useMock();
  const published = s.wf.filter((w) => w.publishedRevisionId != null);
  const [name, setName] = useState(l?.name ?? ""), [path, setPath] = useState(l?.paths[0] ?? ""), [checked, setChecked] = useState(l?.paths[0] ?? "");
  const [wf, setWf] = useState(l?.workflowId ?? published[0]?.id ?? ""), [error, setError] = useState(""), [said, setSaid] = useState("");
  useEffect(() => { const t = setTimeout(() => setChecked(folder(path)), 500); return () => clearTimeout(t); }, [path]); // the real check is a request: not on every key
  const check = checked ? checkFolder(checked, l?.id) : null, ok = check && !check.blockers.length;
  const files = ok ? firstFiles(checked) : [];
  const others = s.libraries.filter((x) => x.workflowId === wf && x.id !== l?.id).map((x) => x.name);
  const changed = !l || name !== l.name || wf !== l.workflowId;
  const save = () => {
    setSaid("");
    if (!l) {
      const r = addLibrary(name, path, wf);
      if (r.error) return setError(r.error);
      return navigate("/libraries/" + r.library!.id);
    }
    const e = setLibrary(l.id, { ...(name !== l.name && { name }), ...(wf !== l.workflowId && { workflowId: wf }) });
    setError(e ?? ""); if (!e) setSaid("Saved.");
  };
  return (
    <div className="stack">
      <div className="pagehead">
        <div><h1>{l ? l.name : "Add library"}</h1>{l ? <p className="sub"><Status l={l} /> <span className="mono sm">{l.paths.join(", ")}</span></p> : <p className="sub">A folder to watch for videos, and the workflow its files go through.</p>}</div>
        <span className="sp" />
        {l && <>
          <button className="btn" type="button" onClick={() => setLibrary(l.id, { enabled: !l.enabled })}><Icon id={l.enabled ? "i-pause" : "i-play"} />{l.enabled ? "Pause" : "Resume"}</button>
          <button className="btn" type="button" disabled={l.scan.state === "scanning"} onClick={() => scanLibrary(l.id)}><Icon id="i-refresh" />{l.scan.state === "scanning" ? "Scanning..." : "Scan now"}</button>
        </>}
      </div>
      <div className="grid cols2">
        <section className="card" aria-labelledby="gen-h">
          <div className="hd"><h2 id="gen-h">Library</h2></div>
          <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
            <div className="field"><label htmlFor="lib-name">Name</label>
              <input id="lib-name" className="input" required value={name} onChange={(e) => setName(e.target.value)} placeholder="Movies" /></div>
            <div className="field"><label htmlFor="lib-path">Folder</label>
              <input id="lib-path" className="input mono" value={path} readOnly={!!l} aria-describedby="lib-path-check" placeholder="/media/movies"
                onChange={(e) => setPath(e.target.value)} onBlur={() => setChecked(folder(path))} />
              <div id="lib-path-check" className="stack" style={{ gap: 4 }}>
                {!check ? <span className="hint">The folder is checked when you leave this field.</span>
                  : check.blockers.length ? check.blockers.map((b) => <span key={b.code} className="st bad" role="alert"><Icon id="i-alert" /><span>{b.message}</span></span>)
                  : <span className="st ok"><Icon id="i-check-c" /><span>The folder can be read</span></span>}
                {check?.notes.map((n) => <span key={n} className="st warn"><Icon id="i-alert" /><span>{n}</span></span>)}
                {l && <span className="hint">To use another folder, add a new library.</span>}
              </div></div>
            <div className="field"><span className="lab">File type</span><span>Video</span></div>
            {l && <div className="field"><label className="row" style={{ gap: 8 }}>
              <input type="checkbox" role="switch" className="switch" checked={l.enabled} onChange={(e) => setLibrary(l.id, { enabled: e.target.checked })} />Enabled</label>
              <span className="hint">When off, no new files are looked for or queued.</span></div>}
            <div className="field"><label htmlFor="lib-wf">Workflow</label>
              {s.wf.length === 0 ? <span>No workflows yet. <Link to="/workflows">Make one in Workflows</Link>, publish it, then come back.</span> : <>
                <div className="row" style={{ gap: 8 }}>
                  <select id="lib-wf" className="input" value={wf} onChange={(e) => setWf(e.target.value)} aria-describedby="lib-wf-hint" style={{ flex: 1 }}>
                    {!wf && <option value="">Choose a workflow</option>}
                    {s.wf.map((w) => <option key={w.id} value={w.id} disabled={w.publishedRevisionId == null}>{w.name}{w.publishedRevisionId == null ? " (not published yet)" : ""}</option>)}
                  </select>
                  {wf && <Link to={"/workflows/" + wf}>Edit this workflow</Link>}
                </div>
                <span id="lib-wf-hint" className="hint">{published.length === 0 ? "Publish a workflow before a library can use it." : others.length ? "Used by " + (others.length > 1 ? others.slice(0, -1).join(", ") + " and " + others.at(-1) : others[0]) + "." : "No other library uses it."}</span>
              </>}
            </div>
            {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
            <p className="muted" role="status">{said}</p>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" type="submit" disabled={!changed || !name.trim() || !wf || (!l && !ok)}>{l ? "Save" : "Add library"}</button>
              <Link className="btn" to="/libraries">Cancel</Link>
            </div>
          </form>
        </section>
        <section className="card" aria-labelledby="first-h">
          <div className="hd"><h2 id="first-h">Is this the right folder?</h2></div>
          <div className="bd stack">
            {!ok ? <p className="muted">Give a folder that can be read, and its first 5 videos show here.</p>
              : <p className="muted">The first 5 files found in <span className="mono">{checked}</span>. Nothing is converted by looking.</p>}
            {ok && (files.length === 0 ? <p>No video files found in this folder.</p> : (
              <div className="tscroll"><table className="tbl">
                <thead><tr><th>File</th><th>Video</th><th className="num">Size</th></tr></thead>
                <tbody>{files.map((f) => (
                  <tr key={f.path}><td className="mono sm">{f.path.slice(checked.length + 1)}</td>
                    <td className="nowrap">{[f.codec, f.resolution, f.dolbyVision && "Dolby Vision"].filter(Boolean).join(" · ")}</td><td className="num">{bytes(f.bytes)}</td></tr>))}
                </tbody>
              </table></div>))}
          </div>
        </section>
      </div>
    </div>
  );
}
