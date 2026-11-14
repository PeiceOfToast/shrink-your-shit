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
import { allWorkers, allowJoin, denyJoin, listJoinRequests, newPairing, type Pairing, dismissUpdateNotice, getWorker, listWorkers, patchWorker, stoppedByUpdate, testCapabilities, workerAction } from "../workersApi";
import type { Capability, JoinRequest, Worker } from "../types/v2";

// review 61dc891 P2-1: only what the server reports (a remote worker may send no OS or host): no guess, no stray dot
const OS: Record<string, string> = { linux: "Linux", darwin: "macOS", windows: "Windows" };
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
// Encoder cards (SCREENS V7, 11-13): one per encoder type; these five always, the extras only when found. Lit when a
// test of that type passed (with its formats); dim with the reason otherwise. Converting is processor-only, so a lit
// graphics card says so.
const TYPES: [string, Capability["slot"], (c: Capability) => boolean][] = [
  ["Processor", "cpu", (c) => c.slot === "cpu"],
  ["NVIDIA", "gpu", (c) => c.key.endsWith("_nvenc")],
  ["Intel Quick Sync", "gpu", (c) => c.key.endsWith("_qsv")],
  ["AMD", "gpu", (c) => c.key.endsWith("_amf")],
  ["Apple", "gpu", (c) => c.key.endsWith("_videotoolbox")],
  ["Graphics card (VAAPI)", "gpu", (c) => c.key.endsWith("_vaapi")],
  ["Windows encoder", "gpu", (c) => c.key.endsWith("_mf")],
  ["Other encoder", "gpu", (c) => c.key.endsWith("_v4l2m2m")],
];
type Card = { name: string; gpu: boolean; found: boolean; lit: boolean; formats: string; why: string; detail?: string; at: string | null };
function cards(w: Worker): Card[] {
  const encs = w.capabilities.filter((c) => c.kind === "encoder"), untested = !w.capabilities.length;
  return TYPES.map(([name, slot, pick]): Card => {
    const cs = encs.filter(pick), ok = cs.filter((c) => c.test === "passed"), bad = cs.find((c) => c.test === "failed");
    return {
      name, gpu: slot === "gpu", found: cs.length > 0, lit: ok.length > 0, detail: bad?.detail,
      formats: ["H.264", "HEVC", "AV1", "VP9"].filter((f) => ok.some((c) => FORMAT(c.key) === f)).join(" · "),
      why: untested ? "Not tested yet" : ok.length ? "" : bad ? "Found, but its test failed" : "Not on this computer",
      at: cs.map((c) => c.testedAt).filter(Boolean).sort().at(-1) ?? null,
    };
  }).filter((c, i) => i < 5 || c.found);
}
// the directory's row (small) and the Capabilities tab (big, with the time each was tested and a failure's detail)
function EncoderCards({ w, big }: { w: Worker; big?: boolean }) {
  return <ul className={"ec-grid" + (big ? " big" : "")} aria-label={"Encoders on " + w.name}>{cards(w).map((c) => (
    <li key={c.name} className={"ec" + (c.lit ? " lit" : "") + (big ? " ec-lg" : "")}>
      <span className="ec-t">{c.lit && <Icon id="i-check" />}<b>{c.name}</b>{c.lit && <span className="ec-ok">Works</span>}</span>
      {c.lit ? <span>{c.formats}</span> : <span>{c.why}</span>}
      {c.lit && c.gpu && <span>Not used for converting yet</span>}
      {big && c.detail && <span className="mono">{c.detail}</span>}
      {big && c.at && <span className="ec-meta">Tested {new Date(c.at).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}</span>}
    </li>))}</ul>;
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

// Add a worker (user item 2; V7b): a one-time pairing code and the one-line command for the other computer, with Copy,
// the time it still works, the plain-http warning, and the new worker named when it connects (the list is watched while
// the panel is open). A second click closes the panel; an expired code offers a new one.
function AddWorker({ workers, reload }: { workers: Worker[]; reload: () => void }) {
  const [open, setOpen] = useState(false), [p, setP] = useState<Pairing | null>(null), [came, setCame] = useState<Worker | null>(null), [err, setErr] = useState(""), [now, setNow] = useState(Date.now()), [said, setSaid] = useState("");
  const known = useRef<string[]>([]), cmd = useRef<HTMLTextAreaElement>(null), group = useId();
  const make = async () => { setErr(""); setSaid(""); setCame(null); known.current = workers.map((w) => w.id); const r = await act(newPairing(), setErr); if (r) setP(r); };
  useEffect(() => { if (!open) return; const t = setInterval(() => (setNow(Date.now()), reload()), 3000); return () => clearInterval(t); }, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const joined = p ? workers.find((w) => !known.current.includes(w.id)) : undefined;
  // design 17:57Z: the panel stays open on the confirmation (closing on its own risks missing it)
  useEffect(() => { if (joined) { setCame(joined); setP(null); } }, [joined?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  // review 61dc891 P3: the minutes come from the server's expiry at render (the 3 s tick re-renders), never a stale clock
  const left = p ? Math.ceil((new Date(p.expiresAt).getTime() - Math.max(now, Date.now())) / 60000) : 0, gone = !!p && left <= 0;
  const local = !!p && /--server https?:\/\/(127\.|localhost|\[::1\])/.test(p.command); // a command only this computer can use
  useEffect(() => { if (said !== "Command copied.") return; const t = setTimeout(() => setSaid(""), 4000); return () => clearTimeout(t); }, [said]);
  // plain http has no clipboard API (not a secure context): select the text and copy it the old way
  // the clipboard API can be missing (plain http) or refused (permission): then the selected text is copied the old way
  const copy = async () => {
    const t = cmd.current; if (!t) return; t.select(); let ok = false;
    try { if (navigator.clipboard) { await navigator.clipboard.writeText(t.value); ok = true; } } catch { /* refused: fall back */ }
    if (!ok) { t.select(); ok = document.execCommand("copy"); }
    setSaid(ok ? "Command copied." : "Select the command and copy it.");
  };
  return <>
    <span className="addw"><button className="btn sm" type="button" aria-expanded={open} aria-controls={group} onClick={() => { if (open) return setOpen(false); setOpen(true); if (came || !p || gone) make(); }}><Icon id="i-plus" />Add a worker</button>
      {said && <span className="muted sm" role="status">{said}</span>}</span>
    {open && <div className="swask addpanel" id={group} role="region" aria-label="Add a worker">
      <p className="sm">Easiest: run the install command on the other computer. It finds this server and asks to join here.</p>
      {came ? <>
        <p role="status"><Icon id="i-check-c" /> Connected: <strong>{came.name}</strong>.</p>
        <div className="row"><Link className="btn sm primary" to={"/workers/" + encodeURIComponent(came.id)}>Open {came.name}</Link><button className="btn sm ghost" type="button" onClick={() => (setOpen(false), setCame(null))}>Done</button></div></>
      : err ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{err}</span></p> : !p ? <p className="muted">Making a pairing code...</p> : gone ? <>
        <p>This code has expired.</p><div className="row"><button className="btn sm primary" type="button" onClick={make}>Make a new code</button></div></> : <>
        <label className="lab" htmlFor={group + "-cmd"}>Run this on the other computer:</label>
        <textarea id={group + "-cmd"} ref={cmd} className="input mono" readOnly rows={2} value={p.command} onFocus={(e) => e.currentTarget.select()} />
        <div className="row"><button className="btn sm primary" type="button" onClick={copy}><Icon id="i-copy" />Copy</button>
          <span className="muted sm">Pairing code <strong className="mono">{p.code}</strong> · works for {left <= 1 ? "less than a minute" : left + " more minutes"}, once.</span></div>
        {local && <p className="muted sm"><Icon id="i-alert" /> This address only works on this computer. To pair another computer, open this page by its network address (for example http://192.168.1.10:8484) and make a new code.</p>}
        {p.plainHTTP && <p className="muted sm"><Icon id="i-info" /> This page uses plain http: fine on your own network. A worker outside it needs https://.</p>}
        <p className="muted sm" aria-live="polite">Waiting for the worker to connect...</p></>}
    </div>}
  </>;
}

// E4 (ONE-COMMAND-SETUP.md section 9): a worker that ran the install command asks to join. One card per request, newest
// first, above the workers; more than 3 fold into "N more requests". Decided and expired requests stay (the server keeps
// them about 10 minutes) until Done or Dismiss. New requests are announced once; the countdown is visual only.
function JoinCards({ workers, on, reload }: { workers: Worker[]; on: boolean; reload: () => void }) {
  const d = useData(listJoinRequests, [], 15000);
  const [hidden, setHidden] = useState<string[]>([]), [all, setAll] = useState(false), [now, setNow] = useState(Date.now());
  const [errs, setErrs] = useState<Record<string, string>>({}), [ids, setIds] = useState<Record<string, string>>({}), [heard, setHeard] = useState("");
  const told = useRef<string[]>([]), box = useRef<HTMLDivElement>(null), refocus = useRef(false);
  const load = useRef(d.reload); load.current = d.reload;
  useEffect(() => onLive((t) => { if (t === "worker.join_request" || t === "resync") load.current(); }), []);
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 15000); return () => clearInterval(t); }, []);
  const t = Math.max(now, Date.now());
  const items = (d.data ?? []).filter((j) => !hidden.includes(j.id))
    .map((j): JoinRequest => (j.state === "waiting" && Date.parse(j.expiresAt) <= t ? { ...j, state: "expired" } : j))
    .sort((a, b) => b.requestedAt.localeCompare(a.requestedAt));
  const fresh = items.filter((j) => j.state === "waiting" && !told.current.includes(j.id));
  useEffect(() => { if (!fresh.length) return; told.current.push(...fresh.map((j) => j.id)); setHeard(fresh.map((j) => "New worker " + j.name + " wants to join.").join(" ")); }, [fresh.map((j) => j.id).join()]); // eslint-disable-line react-hooks/exhaustive-deps
  // after Allow, Deny, Done or Dismiss: the next card's Allow, or else the Workers heading
  useEffect(() => {
    if (!refocus.current) return; refocus.current = false;
    const next = box.current?.querySelector<HTMLElement>(".allow");
    if (next) return next.focus();
    const h = document.querySelector<HTMLElement>(".pagehead h1"); if (h) { h.tabIndex = -1; h.focus(); }
  });
  const hide = (id: string) => { refocus.current = true; setHidden((h) => [...h, id]); };
  const decide = async (j: JoinRequest, allow: boolean) => {
    setErrs((e) => ({ ...e, [j.id]: "" }));
    let r; try { r = allow ? await allowJoin(j.id) : await denyJoin(j.id); } catch { return; } // signed out: going to sign-in
    if (failed(r)) { setErrs((e) => ({ ...e, [j.id]: r.error.message })); d.reload(); return; }
    if (allow) { setIds((m) => ({ ...m, [j.id]: (r as { workerId: string }).workerId })); setHeard(allowed(j.name)); reload(); }
    else setHeard("Denied " + j.name + ". It was told no.");
    refocus.current = true; d.reload();
  };
  const allowed = (n: string) => "Allowed: " + n + ". " + (on ? "It starts converting now." : "It waits, like the others, until you turn workers on.");
  const mins = (ms: number) => Math.round(ms / 60000);
  const shown = all ? items : items.slice(0, 3), more = items.length - shown.length;
  if (!items.length) return <p className="sr" aria-live="polite">{heard}</p>;
  return <div className="stack" ref={box}>
    <p className="sr" aria-live="polite">{heard}</p>
    <div className="joins">{shown.map((j) => {
      const err = errs[j.id] && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{errs[j.id]}</span></p>;
      if (j.state === "denied") return <article key={j.id} className="card joinc"><p>Denied {j.name}. It was told no.</p>
        <div className="row jbtn"><button className="btn" type="button" onClick={() => hide(j.id)}>Done</button></div></article>;
      if (j.state === "expired") return <article key={j.id} className="card joinc"><p>{j.name}&apos;s request expired. To ask again, run the install command on {j.name} again.</p>{err}
        <div className="row jbtn"><button className="btn" type="button" onClick={() => hide(j.id)}>Dismiss</button></div></article>;
      if (j.state === "allowed") {
        const wid = ids[j.id] ?? workers.find((w) => w.name.toLowerCase() === j.name.toLowerCase())?.id;
        return <article key={j.id} className="card joinc"><p><Icon id="i-check-c" /> {allowed(j.name)}</p>
          <div className="row jbtn">{wid && <Link className="btn primary" to={"/workers/" + encodeURIComponent(wid)}>Open {j.name}</Link>}<button className="btn" type="button" onClick={() => hide(j.id)}>Done</button></div></article>;
      }
      const asked = mins(t - Date.parse(j.requestedAt)), left = Math.ceil((Date.parse(j.expiresAt) - t) / 60000);
      const clash = workers.some((w) => w.name.toLowerCase() === j.name.toLowerCase());
      return <article key={j.id} className="card joinc" aria-labelledby={"jr-" + j.id}>
        <h3 id={"jr-" + j.id}>New worker wants to join</h3>
        <p className="sm facts">{[j.name, j.address, OS[j.os ?? ""], j.encoders.join(", ")].filter(Boolean).join(" · ")}</p>
        {clash && <p className="sm">A worker named {j.name} already exists. Allowing replaces it.</p>}
        <p className="sm muted">{asked < 1 ? "Asked just now" : "Asked " + plural(asked, "minute", "minutes") + " ago"} · {left <= 1 ? "expires in less than a minute" : "expires in " + left + " minutes"}</p>
        {err}
        <div className="row jbtn"><button className="btn primary allow" type="button" onClick={() => decide(j, true)}>Allow</button><button className="btn" type="button" onClick={() => decide(j, false)}>Deny</button></div>
      </article>;
    })}</div>
    {more > 0 && <div><button className="btn sm ghost" type="button" onClick={() => setAll(true)}>{plural(more, "more request", "more requests")}</button></div>}
  </div>;
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
        {/* user item 2: Add a worker pairs another computer (V7b) */}
        <AddWorker workers={ws.data ?? []} reload={ws.reload} />
      </PageHead>
      {upd.data === true && <div className="note"><Icon id="i-info" /><div className="sp">Converting is stopped after the update. Press Start to carry on.</div>
        <button className="btn sm ghost" type="button" onClick={async () => { await act(dismissUpdateNotice(), setSaid); upd.reload(); }}>Dismiss</button></div>}
      <p className="muted" role="status">{said}</p>
      {ws.error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{ws.error.message}</span></p>}
      <JoinCards workers={ws.data ?? []} on={n > 0} reload={ws.reload} />
      {!ws.loaded ? <div className="empty">Loading...</div> : !list.length ? <Empty icon="i-worker" text="No workers yet." /> : (
        <div className="wgrid">{list.map((w) => {
          const [t, cls, icon] = words(w), gone = w.status === "offline" || w.status === "stalled";
          return <article key={w.id} className={"card wcard" + (gone ? " dim" : "")}>
            <div className="hd"><h3><Link to={"/workers/" + encodeURIComponent(w.id)}>{w.name}</Link></h3><span className="sp" /><StartStop w={w} onDone={done} /></div>
            <div className="bd stack">
              <p className="sm muted">{[w.host, OS[w.os ?? ""], w.arch, "last heard " + ago(w.lastHeartbeatAt)].filter(Boolean).join(" · ")}</p>
              {w.tags.length > 0 && <div className="chips" aria-label="Tags">{w.tags.map((x) => <span className="mchip" key={x}>{x}</span>)}</div>}
              <p><span className={"pill " + cls}><Icon id={icon} />{t}</span></p>
              {w.currentJobIds.map((id) => <p key={id} className="sm">Converting <Link className="mono" to={"/jobs?tab=running&job=" + encodeURIComponent(id)}>{names[id] ?? "Job " + id}</Link></p>)}
              <div><h4 className="sm muted ec-h">Encoders</h4><EncoderCards w={w} /></div>
            </div>
          </article>;
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
  const decoders = w.capabilities.filter((c) => c.kind === "decoder");
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
        <div className="hd"><h2>Encoders</h2><span className="sp" />
          <button className="btn sm" type="button" disabled={busy} onClick={async () => { setBusy(true); const r = await act(testCapabilities(id), setSaid); setBusy(false); if (r) (setSaid("Tested again."), d.reload()); }}><Icon id="i-refresh" />{busy ? "Testing..." : "Test again"}</button></div>
        <p className="sm muted" style={{ padding: "8px var(--pad) 0" }}>Each one was tested with a short real conversion, not guessed from the graphics card's name.</p>
        <div style={{ padding: "8px var(--pad) 0" }}><EncoderCards w={w} big /></div>
        {decoders.length > 0 && <>
          <h3 className="ec-h" style={{ margin: "16px var(--pad) 6px" }}>Decoders</h3>
          <div className="list"><table className="tbl">
            <thead><tr><th>Format</th><th>How</th><th className="stc">Result</th><th data-p="3">When</th></tr></thead>
            <tbody>{decoders.map((c) => <tr key={c.key}><td className="idc"><span className="ttl">{FORMAT(c.key) || c.label}</span><span className="sub">{c.label}</span><span className="sub phone">{[c.label, c.test === "failed" && c.detail].filter(Boolean).join(" · ")}</span></td><td>{how(c)}</td>
              <td className="stc"><span className={"pill " + (c.test === "passed" ? "ok" : c.test === "failed" ? "" : "")} title={c.detail}>{c.test === "passed" ? "Works" : c.test === "failed" ? "Doesn't work here" : "Not tested"}</span></td>
              <td data-p="3" className="sm">{c.testedAt ? "Tested " + clock(c.testedAt) : ""}{c.test === "failed" && c.detail ? <span className="sub">{c.detail}</span> : null}</td></tr>)}</tbody>
          </table></div></>}
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
