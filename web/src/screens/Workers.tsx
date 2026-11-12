// V7 Workers (SCREENS V7; DESIGN/v2 mockups/workers.html, worker.html, worker-capabilities.html, worker-limits.html) on
// the real /api/v2/workers (#132) with worker.heartbeat live, or the mock with ?mock=1. Shown only when the server has
// it: live CPU, memory and GPU load (null today), graphics cards (none listed). Hidden until the engine has them
// (design): files at once on the graphics card (converting is processor-only), Paths and path tests, Schedule, Logs.
import { useEffect, useId, useRef, useState } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed, type Answer } from "../api";
import { bytes, clock, plural } from "../format";
import { listJobs } from "../jobsApi";
import { onLive } from "../live";
import { Empty, PageHead } from "../PageHead";
import { Link, navigate, useSearch } from "../router";
import { useData } from "../useData";
import { dismissUpdateNotice, getWorker, listWorkers, patchWorker, stoppedByUpdate, testCapabilities, workerAction } from "../workersApi";
import type { Capability, Worker } from "../types/v2";

const going = (w: Worker) => w.status === "busy" || w.status === "online";
// Status in words (SCREENS V7; design: no 'slots').
function words(w: Worker): [string, string, string] {
  const busy = w.slots.cpu.used + " of " + w.slots.cpu.total + " busy";
  switch (w.status) {
    case "busy": case "online": return ["Running · " + busy, "run", "i-play"];
    case "draining": return ["Draining · " + plural(w.currentJobIds.length, "file", "files") + " left", "warn", "i-pause"];
    case "paused": return ["Stopped", "", "i-stop"];
    case "offline": return ["Offline since " + clock(w.statusSince), "bad", "i-wifi-off"];
    default: return ["Not responding since " + clock(w.statusSince), "bad", "i-alert"];
  }
}
const ago = (t: string | null) => {
  if (!t) return "never";
  const s = Math.max(0, Math.round((Date.now() - new Date(t).getTime()) / 1000));
  return s < 60 ? s + " s ago" : s < 3600 ? Math.round(s / 60) + " min ago" : clock(t);
};
const FORMAT = (k: string) => (/265|hevc/i.test(k) ? "HEVC" : /264/i.test(k) ? "H.264" : /av1/i.test(k) ? "AV1" : /vp9/i.test(k) ? "VP9" : "");
const how = (c: Capability) => (c.slot === "cpu" ? "Processor" : c.label.split(",")[0]);
// "HEVC, H.264, AV1 and VP9 on the processor · NVENC found, not used for converting yet"
function encoders(w: Worker): string {
  const ok = w.capabilities.filter((c) => c.kind === "encoder" && c.test === "passed");
  const cpu = [...new Set(ok.filter((c) => c.slot === "cpu").map((c) => FORMAT(c.key)).filter(Boolean))];
  const gpu = [...new Set(ok.filter((c) => c.slot === "gpu").map(how))];
  if (!w.capabilities.length) return "Not tested yet";
  if (!cpu.length && !gpu.length) return "No encoder works yet";
  return [cpu.length && (cpu.length > 1 ? cpu.slice(0, -1).join(", ") + " and " + cpu.at(-1) : cpu[0]) + " on the processor", gpu.length && gpu.join(", ") + " found, not used for converting yet"].filter(Boolean).join(" · ");
}
async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}
// worker.heartbeat moves status and slots live; a job changing state reloads (current jobs).
function useLiveWorkers(reload: () => void) {
  const [beat, setBeat] = useState<Record<string, Partial<Worker>>>({});
  const r = useRef(reload); r.current = reload;
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined;
    return onLive((type, data) => {
      if (type === "worker.heartbeat") { const h = data as { workerId: string; status: Worker["status"]; slots: Worker["slots"]; live: Worker["live"]; at: string }; setBeat((m) => ({ ...m, [h.workerId]: { status: h.status, slots: h.slots, live: h.live, lastHeartbeatAt: h.at } })); }
      if (type === "job.state" || type === "resync") { clearTimeout(t); t = setTimeout(() => (r.current(), setBeat({})), 400); }
    });
  }, []);
  return (w: Worker): Worker => ({ ...w, ...beat[w.id] });
}

// Stop on a busy worker asks first (SCREENS V7): finish these files (drain, the default) or stop now.
function StopAsk({ w, onDone, onCancel }: { w: Worker; onDone: (m: string) => void; onCancel: () => void }) {
  const [now, setNow] = useState(false);
  const go = async () => { if (await act(workerAction(w.id, now ? "stop" : "drain"), onDone)) onDone(now ? w.name + " stopped." : w.name + " finishes its files, then stops."); };
  return (
    <fieldset className="stack choice" style={{ border: 0, padding: 0, margin: 0 }}>
      <legend className="lab">Stop {w.name}?</legend>
      <label className="radio"><input type="radio" name={"stop-" + w.id} checked={!now} onChange={() => setNow(false)} /><span><strong>Stop after these files</strong><span className="hint" style={{ display: "block" }}>The worker finishes what it is converting, then stops.</span></span></label>
      <label className="radio"><input type="radio" name={"stop-" + w.id} checked={now} onChange={() => setNow(true)} /><span><strong>Stop now</strong><span className="hint" style={{ display: "block" }}>The files being converted go back to the queue. Your originals aren't touched.</span></span></label>
      <div className="row"><button className="btn sm primary" type="button" onClick={go}>Stop</button><button className="btn sm ghost" type="button" onClick={onCancel}>Cancel</button></div>
    </fieldset>
  );
}

// Stop all (header of Workers and the Dashboard): when any worker is converting, the same question as Stop on one
// worker (review ab98ef3), in a popover; otherwise it stops at once. onStop(now): now false = finish these files.
// inline (inside the phone "..." menu): the question opens in place, never a popover inside a popover.
export function StopAll({ busy, onStop, inline }: { busy: boolean; onStop: (now: boolean) => void; inline?: boolean }) {
  const [now, setNow] = useState(false), [asking, setAsking] = useState(false), ref = useRef<HTMLDetailsElement>(null), group = useId(); // the Dashboard draws its actions twice (desk, phone menu): one radio group each
  if (!busy) return <button className="btn sm" type="button" onClick={() => onStop(true)}><Icon id="i-stop" />Stop all</button>;
  const close = () => (ref.current?.removeAttribute("open"), setAsking(false));
  const ask = (
        <fieldset className="stack choice" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lab">Stop all workers?</legend>
          <label className="radio"><input type="radio" name={group} checked={!now} onChange={() => setNow(false)} /><span><strong>Stop after these files</strong><span className="hint" style={{ display: "block" }}>Each worker finishes what it is converting, then stops.</span></span></label>
          <label className="radio"><input type="radio" name={group} checked={now} onChange={() => setNow(true)} /><span><strong>Stop now</strong><span className="hint" style={{ display: "block" }}>The files being converted go back to the queue. Your originals aren't touched.</span></span></label>
          <div className="row"><button className="btn sm primary" type="button" onClick={() => { close(); onStop(now); }}>Stop</button>
            <button className="btn sm ghost" type="button" onClick={close}>Cancel</button></div>
        </fieldset>);
  if (inline) return asking ? <div role="group" aria-label="Stop all workers?" style={{ padding: 8, maxWidth: "calc(100vw - 48px)" }}>{ask}</div>
    : <button className="btn sm" type="button" onClick={() => setAsking(true)}><Icon id="i-stop" />Stop all</button>;
  return (
    <details className="menu" ref={ref}><summary className="btn sm"><Icon id="i-stop" />Stop all</summary>
      <div className="pop" role="dialog" aria-label="Stop all workers?" style={{ width: 320, maxWidth: "calc(100vw - 16px)", padding: 12 }}>{ask}</div>
    </details>);
}

function StartStop({ w, onDone }: { w: Worker; onDone: (m: string) => void }) {
  const [asking, setAsking] = useState(false);
  if (w.status === "offline" || w.status === "stalled") return <span className="muted sm">Waiting for it to connect</span>; // design: no Start on an offline worker
  if (asking) return <StopAsk w={w} onDone={(m) => (setAsking(false), onDone(m))} onCancel={() => setAsking(false)} />;
  if (w.status === "paused") return <button className="btn sm" type="button" onClick={async () => { if (await act(workerAction(w.id, "start"), onDone)) onDone(w.name + " started."); }}><Icon id="i-play" />Start</button>;
  return <button className="btn sm" type="button" onClick={async () => { if (w.currentJobIds.length) return setAsking(true); if (await act(workerAction(w.id, "stop"), onDone)) onDone(w.name + " stopped."); }}><Icon id="i-stop" />Stop</button>;
}

function Live({ w }: { w: Worker }) { // only the figures the server sends (null today on this engine)
  const l = w.live, out: string[] = [];
  if (l.cpuPercent != null) out.push("CPU " + Math.round(l.cpuPercent) + "%");
  if (l.memUsedBytes != null && l.memTotalBytes) out.push("Memory " + bytes(l.memUsedBytes) + " of " + bytes(l.memTotalBytes));
  for (const g of w.gpus) out.push("GPU · " + g.model + (g.vramBytes ? " · " + bytes(g.vramBytes) : ""));
  return out.length ? <p className="sm">{out.join(" · ")}</p> : null;
}

export function Workers() {
  const ws = useData(listWorkers, [], 15000), jobs = useData(() => listJobs("running", ""), [], 15000), upd = useData(stoppedByUpdate, []);
  const live = useLiveWorkers(() => (ws.reload(), jobs.reload()));
  const [said, setSaid] = useState("");
  const names = Object.fromEntries((jobs.data && !failed(jobs.data) ? jobs.data.items : []).map((j) => [j.id, j.name]));
  const list = (ws.data ?? []).map(live), anyGoing = list.some(going);
  const done = (m: string) => (setSaid(m), ws.reload());
  // every worker is tried; the line names the ones that failed instead of saying they all changed (review ab98ef3 P3)
  const all = async (a: "start" | "stop" | "drain-busy") => {
    const bad: string[] = [];
    for (const w of list) {
      const v = a === "start" ? (w.status === "paused" ? "start" : null) : going(w) ? (a === "drain-busy" && w.currentJobIds.length ? "drain" : "stop") : null;
      if (v) await act(workerAction(w.id, v), (m) => bad.push(w.name + ": " + m));
    }
    setSaid(bad.length ? "Not changed: " + bad.join(" ") : a === "start" ? "Workers started." : a === "stop" ? "Workers stopped." : "Workers finish their files, then stop.");
    ws.reload();
  };
  return (
    <div className="stack">
      <PageHead icon="i-worker" title="Workers" help="The computers that convert your files. Nothing converts until a worker is started.">
        {list.length > 0 && (anyGoing ? <StopAll busy={list.some((w) => going(w) && w.currentJobIds.length > 0)} onStop={(now) => all(now ? "stop" : "drain-busy")} />
          : <button className="btn sm" type="button" onClick={() => all("start")}><Icon id="i-play" />Start all</button>)}
      </PageHead>
      {upd.data === true && <div className="note"><Icon id="i-info" /><div className="sp">Converting is stopped after the update. Press Start to carry on.</div>
        <button className="btn sm ghost" type="button" onClick={async () => { await act(dismissUpdateNotice(), setSaid); upd.reload(); }}>Dismiss</button></div>}
      <p className="muted" role="status">{said}</p>
      {ws.error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{ws.error.message}</span></p>}
      {!ws.loaded ? <div className="empty">Loading...</div> : !list.length ? <Empty icon="i-worker" text="No workers yet." /> : (
        <div className="wgrid">{list.map((w) => {
          const [t, cls, icon] = words(w), gone = w.status === "offline" || w.status === "stalled";
          return (
            <section key={w.id} className={"card wcard" + (gone ? " dim" : "")} aria-labelledby={"w-" + w.id}>
              <div className="hd"><h2 id={"w-" + w.id}><Link to={"/workers/" + encodeURIComponent(w.id)}>{w.name}</Link></h2><span className="sp" /><span className={"pill " + cls}><Icon id={icon} />{t}</span></div>
              <div className="bd stack">
                <p className="sm muted">{[w.host, w.os === "linux" ? "Linux" : w.os === "darwin" ? "macOS" : "Windows", w.arch, w.appVersion !== "dev" ? "v" + w.appVersion : "dev build", "Last heartbeat " + ago(w.lastHeartbeatAt)].join(" · ")}</p>
                {gone ? <p className="sm muted">No live figures while it is offline.</p> : <Live w={w} />}
                <dl className="kv"><dt>Encoders</dt><dd>{encoders(w)}</dd>
                  <dt>Now</dt><dd>{w.currentJobIds.length ? w.currentJobIds.map((id) => <Link key={id} className="clamp2" to={"/jobs?tab=running&job=" + encodeURIComponent(id)}>{names[id] ?? "Job " + id}</Link>) : "Nothing"}</dd>
                  {w.tags.length > 0 && <><dt>Tags</dt><dd><span className="chips">{w.tags.map((x) => <span className="mchip" key={x}>{x}</span>)}</span></dd></>}</dl>
                <div className="row"><StartStop w={w} onDone={done} /><span className="sp" /><Link className="btn sm ghost" to={"/workers/" + encodeURIComponent(w.id)}>Open</Link></div>
              </div>
            </section>);
        })}</div>)}
    </div>
  );
}

const TABS: [string, string][] = [["overview", "Overview"], ["capabilities", "Capabilities"], ["limits", "Limits"], ["settings", "Settings"]];
export function WorkerPage({ id }: { id: string }) {
  const tab = new URLSearchParams(useSearch()).get("tab") ?? "overview";
  const d = useData(() => getWorker(id), [id], 15000), jobs = useData(() => listJobs("running", ""), [], 15000);
  const live = useLiveWorkers(() => (d.reload(), jobs.reload()));
  const [said, setSaid] = useState(""), [busy, setBusy] = useState(false);
  if (!d.loaded) return <div className="empty">Loading...</div>;
  if (!d.data) return <div className="stack"><PageHead icon="i-worker" crumbs={[["Workers", "/workers"]]} title="Worker not found" /><p className="st bad"><Icon id="i-alert" /><span>{d.error?.message}</span></p></div>;
  const w = live(d.data), [t, cls, icon] = words(w);
  const rows = jobs.data && !failed(jobs.data) ? jobs.data.items.filter((j) => w.currentJobIds.includes(j.id)) : [];
  const caps = w.capabilities.filter((c) => c.kind === "encoder" || c.kind === "decoder");
  const others = w.capabilities.filter((c) => c.kind !== "encoder" && c.kind !== "decoder");
  return (
    <div className="stack">
      <PageHead icon="i-worker" crumbs={[["Workers", "/workers"]]} title={w.name} after={<span className={"pill " + cls}><Icon id={icon} />{t}</span>}>
        <StartStop w={w} onDone={(m) => (setSaid(m), d.reload())} />
      </PageHead>
      <p className="sm muted" style={{ marginTop: -8 }}>{[w.host, w.os, w.arch, w.appVersion, w.kind === "local" ? "built in" : ""].filter(Boolean).join(" · ")}</p>
      <div className="tabs" role="tablist">{TABS.map(([k, l]) => <button key={k} role="tab" type="button" aria-selected={tab === k} onClick={() => navigate("/workers/" + encodeURIComponent(id) + (k === "overview" ? "" : "?tab=" + k))}>{l}</button>)}</div>
      <p className="muted" role="status">{said}</p>
      {tab === "overview" && <>
        <Live w={w} />
        <section className="card panel"><div className="hd"><h2>Current jobs</h2><span className="sp" /><Link className="btn sm ghost" to="/jobs?tab=running">View all</Link></div>
          {!rows.length ? <div className="empty">{w.status === "paused" ? "Stopped: nothing is converting." : "Nothing is converting right now."}</div> : (
            <ul className="plist">{rows.map((j) => <li key={j.id}><div className="sp"><Link className="ttl clamp2" to={"/jobs?tab=running&job=" + encodeURIComponent(j.id)}>{j.name}</Link><span className="sub">{j.library} · {j.progress?.stage ?? ""}</span></div>
              {j.progress && <span className="sm">{Math.round(j.progress.percent ?? j.progress.workflowPercent)}%</span>}</li>)}</ul>)}
        </section>
        <p className="sm muted">Graphics card: {w.gpus.length ? "not used for converting yet" : "none found"}.</p>
      </>}
      {tab === "capabilities" && <section className="card panel">
        <div className="hd"><h2>Encoders and decoders found</h2><span className="sp" />
          <button className="btn sm" type="button" disabled={busy} onClick={async () => { setBusy(true); const r = await act(testCapabilities(id), setSaid); setBusy(false); if (r) (setSaid("Tested again."), d.reload()); }}><Icon id="i-refresh" />{busy ? "Testing..." : "Test again"}</button></div>
        <p className="sm muted" style={{ padding: "8px var(--pad) 0" }}>Each one was tested with a short real conversion, not guessed from the graphics card's name.</p>
        {!caps.length ? <div className="empty">Not tested yet.</div> : (
          <div className="list"><table className="tbl">
            <thead><tr><th>Format</th><th>How</th><th className="stc">Result</th><th data-p="3">When</th></tr></thead>
            <tbody>{caps.map((c) => <tr key={c.key}><td className="idc"><span className="ttl">{(c.kind === "decoder" ? "Decode " : "") + (FORMAT(c.key) || c.label)}</span><span className="sub">{c.label}</span><span className="sub phone">{[c.label, c.test === "failed" && c.detail].filter(Boolean).join(" · ")}</span></td><td>{how(c)}</td>
              <td className="stc"><span className={"pill " + (c.test === "passed" ? "ok" : c.test === "failed" ? "" : "")} title={c.detail}>{c.test === "passed" ? (c.slot === "gpu" ? "Works; not used yet" : "Works") : c.test === "failed" ? "Doesn't work here" : "Not tested"}</span></td>
              <td data-p="3" className="sm">{c.testedAt ? "Tested " + clock(c.testedAt) : ""}{c.test === "failed" && c.detail ? <span className="sub">{c.detail}</span> : null}</td></tr>)}</tbody>
          </table></div>)}
        {others.length > 0 && <p className="sm muted" style={{ padding: "8px var(--pad)" }}>Also: {others.map((c) => c.label + (c.test === "passed" ? "" : " (doesn't work)")).join(", ")}.</p>}
        <p className="sm" style={{ padding: "0 var(--pad) var(--pad)" }}>FFmpeg {w.ffmpeg ? w.ffmpeg.version + " · " + w.ffmpeg.path : "not found"}</p>
      </section>}
      {tab === "limits" && <Limits w={w} onSaved={(m) => (setSaid(m), d.reload())} />}
      {tab === "settings" && <Settings w={w} onSaved={(m) => (setSaid(m), d.reload())} />}
    </div>
  );
}

function Limits({ w, onSaved }: { w: Worker; onSaved: (m: string) => void }) {
  const [cpu, setCpu] = useState(w.slots.cpu.total), [err, setErr] = useState("");
  return (
    <section className="card panel"><div className="hd"><h2>Files at once</h2></div>
      <form className="bd stack" style={{ maxWidth: 520 }} onSubmit={async (e) => { e.preventDefault(); setErr(""); if (await act(patchWorker(w.id, { slots: { cpu } }), setErr)) onSaved("Saved: " + plural(cpu, "file", "files") + " at once."); }}>
        <div className="field"><label htmlFor="lim-cpu">Using the processor</label>
          <select id="lim-cpu" className="input" value={cpu} onChange={(e) => setCpu(Number(e.target.value))} style={{ width: "auto" }}>{[1, 2, 3, 4, 5, 6, 7, 8].map((n) => <option key={n} value={n}>{n}</option>)}</select>
          <span className="hint">How many files this worker converts at the same time. More uses more of the processor.</span></div>
        {/* design: the graphics card's files at once stays hidden while converting is processor-only */}
        {err && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{err}</span></p>}
        <div className="row"><button className="btn primary" type="submit" disabled={cpu === w.slots.cpu.total}>Save</button></div>
      </form>
    </section>
  );
}

function Settings({ w, onSaved }: { w: Worker; onSaved: (m: string) => void }) {
  const [name, setName] = useState(w.name), [tags, setTags] = useState(w.tags.join(", ")), [err, setErr] = useState("");
  const list = tags.split(",").map((x) => x.trim()).filter(Boolean), changed = name.trim() !== w.name || list.join() !== w.tags.join();
  return (
    <section className="card panel"><div className="hd"><h2>Settings</h2></div>
      <form className="bd stack" style={{ maxWidth: 520 }} onSubmit={async (e) => { e.preventDefault(); setErr(""); if (!name.trim()) return setErr("Give the worker a name."); if (await act(patchWorker(w.id, { name: name.trim(), tags: list }), setErr)) onSaved("Saved."); }}>
        <div className="field"><label htmlFor="ws-name">Name</label><input id="ws-name" className="input" value={name} onChange={(e) => setName(e.target.value)} /></div>
        <div className="field"><label htmlFor="ws-tags">Tags</label><input id="ws-tags" className="input" value={tags} onChange={(e) => setTags(e.target.value)} aria-describedby="ws-tags-h" /><span className="hint" id="ws-tags-h">Separate tags with commas.</span></div>
        {err && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{err}</span></p>}
        <div className="row"><button className="btn primary" type="submit" disabled={!changed}>Save</button></div>
      </form>
    </section>
  );
}
