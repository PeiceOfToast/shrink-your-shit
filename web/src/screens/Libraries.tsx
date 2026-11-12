// V5 Libraries (SCREENS V5, mockups/libraries.html, library.html, library-output.html) on /api/v2/libraries (V5b #135:
// Pause/Resume, priority, the folder for new files) and v1's routes for adding, renaming, moving and the workflow, or
// the mock with ?mock=1. The library page has the General, Discovery and Output tabs; Filters and Statistics come later.
// Not shown (CAN'T YET): description and tags, per-library scan settings, other file types.
import { Empty, PageHead } from "../PageHead";
import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed, type Answer } from "../api";
import { addLibrary, checkFolder, firstFiles, getLibraryV2, getSettings, listLibraries, listLibrariesV2, listWorkflows, patchLibrary, patchLibraryV2, scanLibrary, setLibraryWorkflow, type WorkflowChoice } from "../libraryApi";
import { Link, navigate, useSearch } from "../router";
import { bytes, clock, count, duration, plural } from "../format";
import { useData } from "../useData";
import type { FirstFiles, FolderReport, LibraryItem } from "../types/v1";
import type { Library } from "../types/v2";

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
const notStarted = (l: Library) => !l.enabled && !l.scan.lastScanAt;
const paused = (l: Library) => !l.enabled && !!l.scan.lastScanAt;
// One status pill per row (S1): accent only while something is happening; normal states are neutral; Paused warns.
function Status({ l }: { l: Library }) {
  if (l.scan.state === "scanning") return <span className="pill run"><Icon id="i-refresh" />Scanning{l.scan.progress ? " · " + count(l.scan.progress) + " found" : ""}</span>;
  if (notStarted(l)) return <span className="pill"><Icon id="i-pause" />Not started</span>;
  if (paused(l)) return <span className="pill warn"><Icon id="i-pause" />Paused</span>;
  if (l.status === "path_missing") return <span className="pill bad"><Icon id="i-alert" />Folder missing</span>;
  if (l.counts.running) return <span className="pill run"><Icon id="i-play" />Converting {count(l.counts.running)}</span>;
  return <span className="pill"><Icon id="i-check-c" />Watching</span>;
}
// Pause/Resume's words (design SCREENS V5): Pause confirms nothing, Resume undoes it.
export async function pauseResume(ls: Library[], enabled: boolean, say: (m: string) => void) {
  const bad: string[] = [];
  for (const l of ls) await act(() => patchLibraryV2(l.id, { enabled }), (m) => bad.push(l.name + ": " + m));
  const done = ls.length - bad.length, who = ls.length === 1 ? ls[0].name : plural(done, "library", "libraries");
  say((done ? who + (enabled ? " resumed." : " paused. Files already converting will finish.") : "") + (bad.length ? " Not changed: " + bad.join(" ") : ""));
}
const Problem = ({ m }: { m?: string }) => (m ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{m}</span></p> : null);

export function Libraries() {
  const libs = useData(listLibrariesV2, [], 3000);
  const [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState(""), [q, setQ] = useState(""), [selecting, setSelecting] = useState(false);
  const all = libs.data ?? [], items = all.filter((l) => !q || (l.name + " " + l.paths.join(" ")).toLowerCase().includes(q.toLowerCase()));
  const picked = all.filter((l) => sel.includes(l.id)), none = picked.length === 0;
  const scan = async () => {
    setSaid("");
    for (const l of picked) await act(() => scanLibrary(l.id), (m) => setSaid(l.name + ": " + m));
    setSaid((s) => s || "Scanning " + plural(picked.length, "library", "libraries") + "."); libs.reload();
  };
  const access = async () => {
    const bad: string[] = [];
    for (const l of picked) { const r = await act(() => checkFolder(l.paths[0]), (m) => bad.push(l.name + ": " + m)); if (r && r.blockers[0]) bad.push(l.name + ": " + r.blockers[0].message); }
    setSaid(bad.length ? bad.join(" ") : picked.length === 1 ? "The folder of " + picked[0].name + " is fine." : "All " + picked.length + " folders are fine.");
  };
  const needSel = none ? "Select libraries first" : undefined;
  const pr = async (enabled: boolean) => { setSaid(""); await pauseResume(picked.filter((l) => l.enabled !== enabled && !notStarted(l)), enabled, setSaid); setSel([]); libs.reload(); };
  const canPause = picked.some((l) => l.enabled), canResume = picked.some(paused);
  const actions = <>
    <button className="btn sm ghost" type="button" disabled={none} title={needSel} onClick={scan}><Icon id="i-refresh" />Scan now</button>
    <button className="btn sm ghost" type="button" disabled={!canPause} title={needSel} onClick={() => pr(false)}><Icon id="i-pause" />Pause</button>
    <button className="btn sm ghost" type="button" disabled={!canResume} title={needSel} onClick={() => pr(true)}><Icon id="i-play" />Resume</button>
    <button className="btn sm ghost" type="button" disabled={none} title={needSel} onClick={access}><Icon id="i-shield" />Check folder access</button>
  </>;
  return (
    <div className="stack">
      <PageHead icon="i-library" title="Libraries" help="The folders that are watched for videos." />
      <Problem m={libs.error?.message} />
      {/* one toolbar (clean pass T4): Add first, selection actions, filter on the right; on phones Add, Select and "..." */}
      <div className="ltools" role="toolbar" aria-label="Libraries">
        <Link className="btn sm primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link>
        <span className="desk">{actions}</span>
        <button className="btn sm ghost phone" type="button" aria-pressed={selecting} onClick={() => (setSelecting(!selecting), setSel([]))}>{selecting ? "Done" : "Select"}</button>
        <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{actions}</div></details>
        {sel.length > 0 && <span className="muted sm">{sel.length} selected</span>}
        <input className="input find" type="search" placeholder="Filter libraries" aria-label="Filter libraries" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <p className="muted" role="status">{said}</p>
      {!libs.loaded ? <div className="empty">Loading...</div> : all.length === 0 ? <Empty icon="i-library" text="No libraries yet. A library is a folder that is watched for videos."><Link className="btn primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link></Empty>
        : items.length === 0 ? <div className="empty">No library matches.</div> : (
        <div className={"list" + (selecting ? " selecting" : "")}><table className="tbl">
          <thead><tr>
            <th className="selc"><label className="hit"><input type="checkbox" aria-label="Select all libraries" checked={sel.length === all.length} onChange={(e) => setSel(e.target.checked ? all.map((l) => l.id) : [])} /></label></th>
            <th>Name</th><th data-p="2">Workflow</th><th className="num" data-p="3">Waiting</th><th data-p="4">Last scan</th><th data-p="5">Priority</th><th className="num" data-p="6">Files</th><th className="stc">Status</th>
          </tr></thead>
          <tbody>{items.map((l) => (
            <tr key={l.id} className={sel.includes(l.id) ? "sel" : undefined}>
              <td className="selc"><label className="hit"><input type="checkbox" aria-label={"Select " + l.name} checked={sel.includes(l.id)} onChange={(e) => setSel(e.target.checked ? [...sel, l.id] : sel.filter((x) => x !== l.id))} /></label></td>
              <td className="idc"><Link className="ttl" to={"/libraries/" + l.id}>{l.name}</Link>
                <span className="sub mono">{l.paths.join(", ")}</span>
                {paused(l) && <span className="sub">Paused · {count(l.counts.waiting)} waiting, not converting</span>}
                <span className="sub phone">{paused(l) ? "Paused · " + count(l.counts.waiting) + " waiting, not converting"
                  : [l.workflowName, count(l.counts.waiting) + " waiting", l.scan.lastScanAt ? "scanned " + lastScan(l.scan.lastScanAt) : "not scanned yet"].join(" · ")}</span></td>
              <td data-p="2"><Link to={"/workflows/" + l.workflowId}>{l.workflowName}</Link></td>
              <td className="num" data-p="3">{count(l.counts.waiting)}</td>
              <td className="nowrap" data-p="4">{lastScan(l.scan.lastScanAt)}</td>
              <td className="nowrap" data-p="5">{priorityWord(l.priority)}</td>
              <td className="num" data-p="6">{count(l.counts.files)}</td>
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
export function LibraryPage({ id }: { id: string }) {
  const libs = useData(listLibraries, [], 3000), wfs = useData(listWorkflows, [], 10000);
  const v2 = useData(() => (id === "new" ? Promise.resolve(null) : getLibraryV2(id)), [id], 3000);
  const l = id === "new" ? null : libs.data?.find((x) => x.id === id);
  if (id !== "new" && !libs.loaded) return <div className="empty">Loading...</div>;
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
                    <td className="nowrap">{x.state === "unreadable" ? "Can't be read" : x.codec || x.resolution ? <span className="chips">{[x.codec, x.resolution].filter(Boolean).map((c) => <span className="mchip" key={c}>{c}</span>)}</span> : "Checking..."}</td><td className="num">{bytes(x.bytes)}</td></tr>))}
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
          {value && <Link className="btn sm ghost" to={"/workflows/" + value}>Edit this workflow</Link>}
        </div>
        <span id="lib-wf-hint" className="hint">{!workflows.some((w) => w.usable) ? "Publish a workflow before a library can use it." : others.length ? "Used by " + names(others) + "." : "No other library uses it."}</span>
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
  const pick = wf || (!l && workflows?.find((w) => w.usable)?.id) || ""; // a new library starts on the first usable one
  const moved = !!l && f.checked !== l.path;
  const prioChanged = !!v && priority !== v.priority;
  const changed = !l || name !== l.name || moved || pick !== l.flowId || prioChanged;
  const save = async () => {
    setSaid(""); setError(""); setBusy(true);
    try {
      if (!l) { const r = await act(() => addLibrary(name, f.checked, pick), setError); if (r) navigate("/libraries/" + r.id); return; }
      if (name !== l.name || moved) { if (!(await act(() => patchLibrary(l.id, { ...(name !== l.name && { name }), ...(moved && { path: f.checked }) }), setError))) return; }
      if (pick !== l.flowId) { if (!(await act(() => setLibraryWorkflow(l.id, pick), setError))) return; }
      if (prioChanged) { if (!(await act(() => patchLibraryV2(l.id, { priority }), setError))) return; setPrio(null); }
      setSaid("Saved."); reload();
    } finally { setBusy(false); }
  };
  const scan = async () => { if (l && (await act(() => scanLibrary(l.id), setError))) reload(); };
  return (
    <div className="stack">
      <PageHead icon="i-library" crumbs={[["Libraries", "/libraries"]]} title={l ? l.name : "Add library"} help={l ? undefined : "A folder to watch for videos, and the workflow its files go through."}>
        {v && <Status l={v} />}
        {v && v.enabled && <button className="btn" type="button" onClick={() => pauseResume([v], false, setNote).then(reload)}><Icon id="i-pause" />Pause</button>}
        {v && paused(v) && <button className="btn" type="button" onClick={() => pauseResume([v], true, setNote).then(reload)}><Icon id="i-play" />Resume</button>}
        {l && <button className="btn" type="button" disabled={v?.scan.state === "scanning"} onClick={scan}><Icon id="i-refresh" />{v?.scan.state === "scanning" ? "Scanning..." : "Scan now"}</button>}
      </PageHead>
      {note && <p className="muted" role="status">{note}</p>}
      {v && notStarted(v) && <p className="muted" role="note">This library isn't started yet: nothing is looked for or converted until you press Scan now.</p>}
      {v && paused(v) && <p className="muted" role="note">Paused: {plural(v.counts.waiting, "file waits", "files wait")} in the queue but isn't converted, and scans add nothing new until you press Resume.</p>}
      {l && <nav className="tabs" aria-label="Library sections">
        {[["general", "General"], ["discovery", "Discovery"], ["output", "Output"]].map(([k, t]) =>
          <Link key={k} to={"/libraries/" + l.id + (k === "general" ? "" : "?tab=" + k)} aria-current={tab === k ? "page" : undefined}>{t}</Link>)}
      </nav>}
      {tab === "discovery" ? <Discovery every={v?.discovery.scanEverySeconds} /> : tab === "output" ? (v ? <Output v={v} reload={reload} /> : <div className="empty">Loading...</div>) :
      <div className="grid cols2">
        <section className="card" aria-labelledby="gen-h">
          <div className="hd"><h2 id="gen-h">Library</h2></div>
          <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
            <div className="field"><label htmlFor="lib-name">Name</label>
              <input id="lib-name" className="input" required value={name} onChange={(e) => (setName(e.target.value), setTyped(true))} placeholder="Movies" /></div>
            <FolderField f={f} free={l && !moved ? l.freeBytes : undefined} />
            <div className="field"><span className="lab">File type</span><span>Video</span></div>
            <WorkflowField value={pick} onChange={setWf} workflows={workflows} self={l?.name} />
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
        <FirstFive f={f} />
      </div>}
    </div>
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
        : <p className="muted">{st.loaded ? "This server doesn't say how often it looks for new files." : "Loading..."}</p>}</div>
    </section>);
}

// Output (SCREENS V5, mockups/library-output.html): read-only, from the workflow's Save the result; the one place for
// this library's own folder for new files (overrideFolder). The server's problem, if any, in its words.
const POLICY: Record<string, string> = {
  replace: "Replace the original. The original is kept for a while, then deleted. This frees space.",
  beside: "Put the new file next to the original. Both stay; nothing is deleted.",
  separate_folder: "Save to a separate folder. Originals are left in place, so no space is freed until you remove them.",
};
function Output({ v, reload }: { v: Library; reload: () => void }) {
  const o = v.output, [dir, setDir] = useState(o.overrideFolder ?? ""), [said, setSaid] = useState(""), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  const save = async (to: string | null) => {
    setSaid(""); setError(""); setBusy(true);
    try { if (await act(() => patchLibraryV2(v.id, { overrideFolder: to }), setError)) { setSaid(to ? "Saved. New files for " + v.name + " go to " + to + "." : "Saved. " + v.name + " uses the workflow's folder again."); if (!to) setDir(""); reload(); } }
    finally { setBusy(false); }
  };
  const unused = o.overrideFolder && o.folder !== o.overrideFolder; // the workflow saves every library's files to one folder
  return (
    <section className="card" aria-labelledby="out-h">
      <div className="hd"><h2 id="out-h">Where results go</h2></div>
      <div className="bd stack">
        <p>Set by the workflow <Link to={"/workflows/" + v.workflowId}>{v.workflowName}</Link>: {o.policy ? POLICY[o.policy] ?? o.policy : "it can't be read right now."}</p>
        {o.folder && <p>New files go to <span className="mono" style={{ overflowWrap: "anywhere" }}>{o.folder}</span>.</p>}
        {o.problem && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{o.problem.message}</span></p>}
        <div className="row"><Link className="btn sm" to={"/workflows/" + v.workflowId}>Edit in workflow</Link></div>
        {o.policy === "separate_folder" && <form className="stack" onSubmit={(e) => (e.preventDefault(), save(dir.trim() || null))}>
          <div className="field"><label htmlFor="lib-over">Use a different folder for this library</label>
            <input id="lib-over" className="input mono" value={dir} placeholder={v.paths[0] + " (shrunk)"} onChange={(e) => setDir(e.target.value)} aria-describedby="lib-over-h" />
            <span className="hint" id="lib-over-h">This is the one place to override the folder for {v.name} only.{unused ? " The workflow saves every library's files to one folder, so this folder is used only with Next to each library." : ""}</span></div>
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
