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
import { allWorkers, dismissUpdateNotice, getWorker, listWorkers, patchWorker, stoppedByUpdate, testCapabilities, workerAction } from "../workersApi";
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

// "Workers on" (user review 11-12 item 2, design 7.2): one switch for every worker, in the Dashboard's and the Workers
// page's header. On while any worker is on (converting or ready; a finishing worker is off), with how many: "Workers on ·
// 1 of 3", or "Workers off · 1 finishing its file". ON (also while one finishes) starts every
// worker that is off, with no question; OFF asks today's question once when a worker is converting (Stop after these
// files = drain, or Stop now), else stops at once. The question takes its own row under the header.
export function WorkersSwitch({ n, m, busy, finishing = 0, onSet }: { n: number; m: number; busy: boolean; finishing?: number; onSet: (a: "start" | "stop" | "drain") => void }) {
  const [asking, setAsking] = useState(false), [now, setNow] = useState(false), group = useId(), on = n > 0;
  const sw = useRef<HTMLInputElement>(null), box = useRef<HTMLDivElement>(null);
  // review 32a8138 P3: the question is a labelled alertdialog; focus goes into it and back to the switch; Escape is Cancel
  const close = () => { setAsking(false); setTimeout(() => sw.current?.focus(), 0); };
  useEffect(() => { if (asking) box.current?.querySelector<HTMLInputElement>("input:checked")?.focus(); }, [asking]);
  useEffect(() => { if (asking && !busy) close(); }, [busy]); // eslint-disable-line react-hooks/exhaustive-deps -- the files finished by themselves: nothing to ask
  return <>
    <label className="sw"><input ref={sw} type="checkbox" role="switch" className="switch" checked={on} disabled={!m} aria-describedby={group + "-n"}
      onChange={() => (on ? (busy ? setAsking(!asking) : onSet("stop")) : onSet("start"))} />
      <span><b>{on ? "Workers on" : "Workers off"}</b><span className="muted" id={group + "-n"}>{on ? " · " + n + " of " + m + (finishing > 0 ? " · " + finishing + " finishing" : "")
        : finishing > 0 ? " · " + finishing + (finishing === 1 ? " finishing its file" : " finishing their files") : " · 0 of " + m}</span></span></label>
    {asking && <div ref={box} className="swask" role="alertdialog" aria-label="Stop all workers?" aria-describedby={group + "-q"} onKeyDown={(e) => e.key === "Escape" && (e.preventDefault(), close())}>
      <fieldset className="stack choice" style={{ border: 0, padding: 0, margin: 0 }}>
        <legend className="lab" id={group + "-q"}>Stop all workers?</legend>
        <label className="radio"><input type="radio" name={group} checked={!now} onChange={() => setNow(false)} /><span><strong>Stop after these files</strong><span className="hint" style={{ display: "block" }}>Each worker finishes what it is converting, then stops.</span></span></label>
        <label className="radio"><input type="radio" name={group} checked={now} onChange={() => setNow(true)} /><span><strong>Stop now</strong><span className="hint" style={{ display: "block" }}>The files being converted go back to the queue. Your originals aren't touched.</span></span></label>
        <div className="row"><button className="btn sm primary" type="button" onClick={() => { close(); onSet(now ? "stop" : "drain"); }}>Stop</button>
          <button className="btn sm ghost" type="button" onClick={close}>Cancel</button></div>
      </fieldset></div>}
  </>;
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
  const list = (ws.data ?? []).map(live);
  const done = (m: string) => (setSaid(m), ws.reload());
  // the "Workers on" switch: one call for every worker (#171)
  // lead 16:57Z (design 7.2): a finishing worker shows OFF; the label says it's finishing; ON again starts every worker that isn't on
  const n = list.filter(going).length, fin = list.filter((w) => w.status === "draining").length;
  const busyAny = list.some((w) => (going(w) || w.status === "draining") && w.currentJobIds.length > 0); // a finishing file is asked about too
  const set = async (a: "start" | "stop" | "drain") => {
    if (await act(allWorkers(a), setSaid)) setSaid(a === "start" ? "Workers started." : a === "stop" ? "Workers stopped." : "Workers finish their files, then stop.");
    ws.reload();
  };
  return (
    <div className="stack">
      <PageHead icon="i-worker" title="Workers" help="The computers that convert your files. Nothing converts until a worker is started.">
        {list.length > 0 && <WorkersSwitch n={n} m={list.length} busy={busyAny} finishing={fin} onSet={set} />}
        {/* user item 2: the button is there before remote workers (V7b) can be paired; until then it says so */}
        <span className="addw"><button className="btn sm" type="button" disabled aria-describedby="addw-note"><Icon id="i-plus" />Add a worker</button><span id="addw-note" className="muted sm">Coming with remote workers</span></span>
      </PageHead>
      {upd.data === true && <div className="note"><Icon id="i-info" /><div className="sp">Converting is stopped after the update. Press Start to carry on.</div>
        <button className="btn sm ghost" type="button" onClick={async () => { await act(dismissUpdateNotice(), setSaid); upd.reload(); }}>Dismiss</button></div>}
      <p className="muted" role="status">{said}</p>
      {ws.error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{ws.error.message}</span></p>}
      {!ws.loaded ? <div className="empty">Loading...</div> : !list.length ? <Empty icon="i-worker" text="No workers yet." /> : (
        // design 7.2: flat rows (name and host, now, slots, encoders, last heard); on phones the name and its action
        // lead, then each figure on its own labelled line
        <table className="tbl wk"><thead><tr><th>Worker</th><th>Now</th><th className="num">Slots</th><th>Encoders</th><th className="num">Last heard</th><th><span className="sr">Start or stop</span></th></tr></thead>
          <tbody>{list.map((w) => {
            const [t, cls, icon] = words(w), gone = w.status === "offline" || w.status === "stalled";
            return (
              <tr key={w.id} className={gone ? "dim" : undefined}>
                <td className="nm"><Link className="ttl" to={"/workers/" + encodeURIComponent(w.id)}>{w.name}</Link>
                  <span className="sub">{[w.host, w.os === "linux" ? "Linux" : w.os === "darwin" ? "macOS" : "Windows"].join(" · ")}</span>
                  {w.tags.length > 0 && <span className="chips" aria-label="Tags">{w.tags.map((x) => <span className="mchip" key={x}>{x}</span>)}</span>}</td>
                <td className="now"><span className="nowl"><span className={"pill " + cls}><Icon id={icon} />{t}</span>
                  {w.currentJobIds.map((id) => <Link key={id} className="mono sm" to={"/jobs?tab=running&job=" + encodeURIComponent(id)}>{names[id] ?? "Job " + id}</Link>)}</span></td>
                <td className="num" data-l="Slots">{w.slots.cpu.used} of {w.slots.cpu.total}</td>
                <td data-l="Encoders">{encoders(w)}</td>
                <td className="num" data-l="Last heard">{ago(w.lastHeartbeatAt)}</td>
                <td className="act"><StartStop w={w} onDone={done} /></td>
              </tr>);
          })}</tbody></table>)}
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
