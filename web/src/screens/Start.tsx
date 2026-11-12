// First run (/next/start; DESIGN/v2 IA.md "First run", mockups/start*.html) on the real server's /api/settings,
// /api/libraries, /api/flows and /api/workers (or the mock with ?mock=1). Three steps: Test one file waits for the
// engine's test run (SCREENS "Contract gaps"), so its Start the worker / Not yet end step 3.
// Step 1 can't make its library yet (a library needs a workflow, which step 2 makes), so its name and folder wait in
// settings.firstRunLibrary (#116); making the library clears it. Step 2 makes the workflow and then the library; first
// run never says "Publish" (design 02:41Z). Finishing scans the library, as v1's first run does.
import { PageHead } from "../PageHead";
import { useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed, type Answer } from "../api";
import { addLibrary, getSettings, listLibraries, listWorkers, listWorkflows, patchSettings, scanLibrary, startSimpleWorkflow, startWorker } from "../libraryApi";
import { Link, navigate } from "../router";
import { useData } from "../useData";
import type { FirstRunSettings } from "../types/v1";
import { FirstFive, FolderField, suggestName, useFolder } from "./Libraries";
import { START_SIMPLE } from "./Workflows";

type Kept = NonNullable<FirstRunSettings["firstRunLibrary"]>;
// Runs one request; its error message goes to `onError`. False when it failed (or the user is signed out).
async function ok<T>(p: Promise<Answer<T>>, onError: (m: string) => void): Promise<T | false> {
  try { const r = await p; if (failed(r)) return (onError(r.error.message), false); return r; } catch (e) { if (e instanceof SignedOut) return false; throw e; }
}

export function Start() {
  const st = useData(getSettings, []), libs = useData(listLibraries, []), wfs = useData(listWorkflows, []);
  const [editing, setEditing] = useState(false), [error, setError] = useState("");
  if (!st.loaded || !libs.loaded) return <div className="empty">{st.error?.message ?? libs.error?.message ?? "Loading..."}</div>;
  const s = st.data!, lib = libs.data?.[0], kept = s.firstRunLibrary;
  const wfName = lib && (wfs.data?.find((w) => w.id === lib.flowId)?.name ?? lib.settingLabel);
  const refresh = () => (st.reload(), libs.reload(), wfs.reload());
  const step = editing || (!lib && !kept) ? 1 : !lib ? 2 : 3, done = step - 1;
  if (s.firstRunDone) return (
    <div className="stack"><PageHead title="Set up Shrink Your Shit" />
      <div className="card"><div className="bd"><p>Setup is done. <Link to="/">Go to the Dashboard</Link>.</p></div></div></div>);
  return (
    <div className="stack" style={{ maxWidth: 880 }}>
      <PageHead title="Set up Shrink Your Shit"><span className="muted">{done} of 3 done</span></PageHead>
      <p className="muted">Three short steps. Each is saved as you go. Nothing converts until you start a worker.</p>
      <div className="bar" role="progressbar" aria-label="Setup" aria-valuemin={0} aria-valuemax={3} aria-valuenow={done}><i style={{ width: (100 * done) / 3 + "%" }} /></div>
      {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
      {step > 1 && <Done n={1} what="Library" text={lib ? lib.name + " · " + lib.path : kept!.name + " · " + kept!.path}
        change={lib ? <Link to={"/libraries/" + lib.id}>Change</Link> : <button className="btn sm ghost" type="button" onClick={() => setEditing(true)}>Change</button>} />}
      {step > 2 && <Done n={2} what="Workflow" text={(wfName ?? "") + " (Start Simple)"} change={lib && <Link to={"/workflows/" + lib.flowId}>Change</Link>} />}
      {step === 1 && <StepLibrary taken={(libs.data ?? []).map((x) => x.name)} kept={kept} onDone={async (v) => { setError(""); if (await ok(patchSettings({ firstRunLibrary: v, firstRunStep: 2 }), setError)) (setEditing(false), refresh()); }} />}
      {step === 2 && <StepWorkflow kept={kept!} onDone={async () => { await ok(patchSettings({ firstRunStep: 3 }), setError); refresh(); }} />}
      {step < 2 && <Later n={2} name="Create a workflow" />}
      {step === 3 ? <StepWorker libraryId={lib!.id} /> : <Later n={3} name="Your worker" />}
    </div>
  );
}

function Done({ n, what, text, change }: { n: number; what: string; text: string; change?: ReactNode }) {
  return <section className="card"><div className="bd row" style={{ gap: 8 }}><span className="st ok"><Icon id="i-check-c" /><span>Done: {n} · {what}</span></span><span>{text}</span><span className="sp" />{change}</div></section>;
}
function Later({ n, name }: { n: number; name: string }) {
  return <section className="card" aria-disabled="true"><div className="hd"><h2 className="muted">{n} · {name}</h2></div></section>;
}

function StepLibrary({ kept, onDone, taken }: { kept: Kept | null; onDone: (v: Kept) => void; taken: string[] }) {
  const f = useFolder(kept?.path ?? "");
  const [name, setName] = useState(kept?.name ?? ""), [typed, setTyped] = useState(!!kept);
  const shown = typed ? name : suggestName(f.checked, taken);
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
  const [name, setName] = useState("Shrink videos"), [out, setOut] = useState(kept.path + " (shrunk)"), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  const [made, setMade] = useState<string>(); // the workflow an earlier try made: a retry reuses it
  const save = async () => {
    setError("");
    if (!name.trim()) return setError("Give the workflow a name.");
    setBusy(true);
    try {
      const w = await ok(startSimpleWorkflow(name.trim(), out.trim(), made, kept.path), setError);
      if (!w) return;
      setMade(w.id);
      if (await ok(addLibrary(kept.name, kept.path, w.id), setError)) onDone();
    } finally { setBusy(false); }
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
          <span id="s2-out-hint" className="hint">A folder outside {kept.path}. You can change this and every other choice later.</span></div>
        {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
        <div className="row"><button className="btn primary" type="submit" disabled={busy}>{busy ? "Saving..." : "Save and use it"}</button></div>
      </form>
    </section>
  );
}

function StepWorker({ libraryId }: { libraryId: string }) {
  const ws = useData(listWorkers, []);
  const [error, setError] = useState(""), [busy, setBusy] = useState(false);
  const w = ws.data?.find((x) => x.id === "this") ?? ws.data?.[0];
  const finish = async (start: boolean) => {
    setError(""); setBusy(true);
    try {
      if (!(await ok(scanLibrary(libraryId), setError))) return; // both buttons scan (reading only), as v1's first run
      if (start && w && !(await ok(startWorker(w.id), setError))) return;
      if (await ok(patchSettings({ firstRunDone: true, firstRunStep: 4 }), setError)) navigate("/");
    } finally { setBusy(false); }
  };
  return (
    <section className="card" aria-labelledby="s3-h">
      <div className="hd"><h2 id="s3-h">3 · Your worker</h2></div>
      <div className="bd stack">
        <p>{w ? w.name : "This computer"} is already set up as a worker. Nothing to do.</p>
        {w && <dl className="kv">
          <dt>Computer</dt><dd>{w.host}</dd>
          <dt>Files at once</dt><dd>{w.filesAtOnce}</dd>
          <dt>Graphics card</dt><dd>{w.gpu.found ? (w.gpu.vendor ? w.gpu.vendor.toUpperCase() + " found" : "Found") + ", not used for converting yet" : "None found. That is fine: the processor converts."}</dd>
        </dl>}
        {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
        <div className="row"><button className="btn primary" type="button" disabled={busy} onClick={() => finish(true)}><Icon id="i-play" />Start the worker</button>
          <button className="btn" type="button" disabled={busy} onClick={() => finish(false)}>Not yet</button></div>
        <p className="muted sm">Start the worker begins converting this library. Not yet finishes setup; nothing converts until you press Start on Workers.</p>
      </div>
    </section>
  );
}
