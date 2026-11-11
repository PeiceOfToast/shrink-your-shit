// First run (/next/start; DESIGN/v2 IA.md "First run", mockups/start*.html), on the mock. Three steps: Test one file
// waits for the engine's test run (SCREENS "Contract gaps"), so its Start the worker / Not yet end step 3.
// Step 1 can't make its library yet: a library needs a published workflow (V1 87faa4b), which step 2 makes. So step 1's
// name and folder wait in the server's settings.firstRunLibrary (workshop first-run-library 9cf8a25), or in
// sessionStorage on a server without that field. Step 2 saves the workflow (it publishes; first run never says
// "Publish", design 02:41Z) and then adds the library on it. Finishing scans the library, as v1's first run does.
import { useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { addLibrary, createWorkflow, getDraft, publish, saveDraft, scanLibrary, setSettings, startAll, useMock } from "../mock";
import { Link, navigate } from "../router";
import type { Settings } from "../types/v2";
import { FirstFive, FolderField, useFolder } from "./Libraries";
import { START_SIMPLE } from "./Workflows";

type Kept = NonNullable<Settings["firstRunLibrary"]>;
const KEY = "syp.firstRunLibrary";
function fromSession(): Kept | null {
  try { const v = JSON.parse(sessionStorage.getItem(KEY) ?? "null"); return v && typeof v.name === "string" && typeof v.path === "string" ? v : null; } catch { return null; }
}
function toSession(v: Kept | null) {
  try { v ? sessionStorage.setItem(KEY, JSON.stringify(v)) : sessionStorage.removeItem(KEY); } catch { /* private window: kept for this page only */ }
}
const base = (p: string) => p.replace(/\/+$/, "").split("/").pop() ?? "";
const title = (s: string) => s.replace(/[._-]+/g, " ").replace(/^\w/, (c) => c.toUpperCase());

export function Start() {
  const s = useMock();
  const server = s.settings.firstRunLibrary !== undefined; // the field is there: the server keeps step 1
  const [local, setLocal] = useState<Kept | null>(() => (server ? null : fromSession()));
  const kept = server ? s.settings.firstRunLibrary ?? null : local;
  const keep = (v: Kept | null) => (server ? setSettings({ firstRunLibrary: v }) : (toSession(v), setLocal(v)));
  const lib = s.libraries[0], wf = lib && s.wf.find((w) => w.id === lib.workflowId);
  const [editing, setEditing] = useState(false);
  const step = editing || (!lib && !kept) ? 1 : !lib ? 2 : 3;
  const done = step - 1;
  if (s.settings.firstRunDone) return (
    <div className="stack"><div className="pagehead"><div><h1>Set up Shrink Your Shit</h1></div></div>
      <div className="card"><div className="bd"><p>Setup is done. <Link to="/">Go to the Dashboard</Link>.</p></div></div></div>);
  return (
    <div className="stack" style={{ maxWidth: 880 }}>
      <div className="pagehead"><div><h1>Set up Shrink Your Shit</h1><p className="sub">Three short steps. Each is saved as you go. Nothing converts until you start a worker.</p></div>
        <span className="sp" /><span className="muted">{done} of 3 done</span></div>
      <div className="bar" role="progressbar" aria-label="Setup" aria-valuemin={0} aria-valuemax={3} aria-valuenow={done}><i style={{ width: (100 * done) / 3 + "%" }} /></div>
      {step > 1 && <Done n={1} what="Library" text={(lib ? lib.name + " · " + lib.paths[0] : kept!.name + " · " + kept!.path)}
        change={lib ? <Link to={"/libraries/" + lib.id}>Change</Link> : <button className="btn sm ghost" type="button" onClick={() => setEditing(true)}>Change</button>} />}
      {step > 2 && <Done n={2} what="Workflow" text={(wf?.name ?? "") + " (Start Simple)"} change={wf && <Link to={"/workflows/" + wf.id}>Change</Link>} />}
      {step === 1 && <StepLibrary kept={kept} onDone={(v) => (keep(v), setSettings({ firstRunStep: 2 }), setEditing(false))} />}
      {step === 2 ? <StepWorkflow kept={kept!} onDone={() => (toSession(null), setLocal(null), setSettings({ firstRunStep: 3 }))} />
        : step < 2 && <Later n={2} name="Create a workflow" />}
      {step === 3 ? <StepWorker libraryId={lib.id} /> : <Later n={3} name="Your worker" />}
    </div>
  );
}

function Done({ n, what, text, change }: { n: number; what: string; text: string; change?: ReactNode }) {
  return <section className="card"><div className="bd row" style={{ gap: 8 }}><span className="st ok"><Icon id="i-check-c" /><span>Done: {n} · {what}</span></span><span>{text}</span><span className="sp" />{change}</div></section>;
}
function Later({ n, name }: { n: number; name: string }) {
  return <section className="card" aria-disabled="true"><div className="hd"><h2 className="muted">{n} · {name}</h2></div></section>;
}

function StepLibrary({ kept, onDone }: { kept: Kept | null; onDone: (v: Kept) => void }) {
  const f = useFolder(kept?.path ?? "");
  const [name, setName] = useState(kept?.name ?? ""), [typed, setTyped] = useState(!!kept);
  const shown = typed ? name : title(base(f.checked));
  return (
    <section className="card" aria-labelledby="s1-h">
      <div className="hd"><h2 id="s1-h">1 · Add a library</h2></div>
      <form className="bd stack" onSubmit={(e) => (e.preventDefault(), f.ok && shown.trim() && onDone({ name: shown.trim(), path: f.checked }))}>
        <p className="muted">The folder with your videos.</p>
        <FolderField f={f} />
        <div className="field"><label htmlFor="s1-name">Name</label>
          <input id="s1-name" className="input" value={shown} placeholder="Movies" onChange={(e) => (setName(e.target.value), setTyped(true))} /></div>
        <FirstFive f={f} />
        <div className="row"><button className="btn primary" type="submit" disabled={!f.ok || !shown.trim()}>Continue</button></div>
      </form>
    </section>
  );
}

function StepWorkflow({ kept, onDone }: { kept: Kept; onDone: () => void }) {
  const s = useMock();
  const [name, setName] = useState("Shrink videos"), [out, setOut] = useState(kept.path + " (shrunk)"), [error, setError] = useState("");
  const save = () => {
    setError("");
    if (!name.trim()) return setError("Give the workflow a name.");
    // A draft left by an earlier try (a reload, an error) is used again rather than making a second one.
    const w = s.wf.find((x) => x.presetId === "shrink_videos" && x.publishedRevisionId == null && x.name === name.trim()) ?? createWorkflow(name.trim(), "");
    const d = getDraft(w.id)!, doc = structuredClone(d.workflow);
    const commit = doc.nodes.find((n) => n.type === "output.commit")!;
    commit.config = { ...commit.config, policy: "separate_folder", folderMode: "one_folder", folder: out.trim() };
    const r = saveDraft(w.id, doc, d.etag);
    if (r.error) return setError(r.error);
    const problem = r.draft!.validation.find((v) => v.severity === "error");
    if (problem) return setError(problem.message);
    if (publish(w.id, r.draft!.etag).error) return setError("Something changed while saving. Try again.");
    const l = addLibrary(kept.name, kept.path, w.id);
    if ("error" in l) return setError(l.error.message);
    onDone();
  };
  return (
    <section className="card" aria-labelledby="s2-h">
      <div className="hd"><h2 id="s2-h">2 · Create a workflow</h2></div>
      <form className="bd stack" onSubmit={(e) => (e.preventDefault(), save())}>
        <p className="muted">What happens to each video in {kept.name}.</p>
        <div className="field"><label htmlFor="s2-name">Name</label><input id="s2-name" className="input" value={name} onChange={(e) => setName(e.target.value)} /></div>
        <fieldset className="stack choice" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lab">How to start</legend>
          <div className="radio"><input type="radio" id="s2-simple" name="how" checked readOnly />
            <div style={{ minWidth: 0, flex: 1 }}><label htmlFor="s2-simple">Start Simple: Shrink videos</label>
              <div className="hint" style={{ fontSize: "var(--fs)" }}>{START_SIMPLE}</div></div></div>
        </fieldset>
        <div className="field"><label htmlFor="s2-out">Where new files go</label>
          <input id="s2-out" className="input mono" value={out} onChange={(e) => setOut(e.target.value)} aria-describedby="s2-out-hint" />
          <span id="s2-out-hint" className="hint">A folder outside {kept.path}. You can change this and every other choice later in Workflows.</span></div>
        {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
        <div className="row"><button className="btn primary" type="submit">Save and use it</button></div>
      </form>
    </section>
  );
}

function StepWorker({ libraryId }: { libraryId: string }) {
  const s = useMock();
  const w = s.workers.find((x) => x.kind === "local") ?? s.workers[0];
  const finish = (start: boolean) => {
    scanLibrary(libraryId); // both buttons scan (reading only), as v1's first run
    if (start) startAll(); // first run has the one worker: this computer
    setSettings({ firstRunDone: true, firstRunStep: 4 });
    navigate("/");
  };
  return (
    <section className="card" aria-labelledby="s3-h">
      <div className="hd"><h2 id="s3-h">3 · Your worker</h2></div>
      <div className="bd stack">
        <p>{w ? w.name : "This computer"} is already set up as a worker. Nothing to do.</p>
        {w && <dl className="kv">
          <dt>Computer</dt><dd>{w.host} · {w.os} {w.arch}</dd>
          <dt>Files at once</dt><dd>{w.slots.cpu.total}</dd>
          {w.ffmpeg && <><dt>Converter</dt><dd>FFmpeg {w.ffmpeg.version}</dd></>}
        </dl>}
        <div className="row"><button className="btn primary" type="button" onClick={() => finish(true)}><Icon id="i-play" />Start the worker</button>
          <button className="btn" type="button" onClick={() => finish(false)}>Not yet</button></div>
        <p className="muted sm">Start the worker begins converting this library. Not yet finishes setup; nothing converts until you press Start on Workers.</p>
      </div>
    </section>
  );
}
