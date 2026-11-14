// First page, step 0 of first run (ONE-COMMAND-SETUP.md section 11): is this computer the main server or a worker for
// another one? The installer asks nothing (user 11-14); the web page decides. Joining asks the other server, which
// shows an Allow card on its Workers page (section 9). Real /api/v2/setup, or the mock with ?mock=1.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { SignedOut, failed, type Answer } from "../api";
import { Icon } from "../Icon";
import { onLive } from "../live";
import { getSetup, listServers, setRole } from "../setupApi";
import type { FoundServer, RoleBody, Setup } from "../types/v2";
import { useData } from "../useData";
import { listWorkers } from "../workersApi";

// The setup, kept fresh: setup.changed when it arrives, every 3 s otherwise (workshop 00:41Z: polling is the fallback).
export function useSetup() {
  const d = useData(getSetup, [], 3000);
  const r = useRef(d.reload); r.current = d.reload;
  useEffect(() => onLive((type) => { if (type === "setup.changed" || type === "resync") r.current(); }), []);
  return d;
}

async function send(b: RoleBody): Promise<Answer<Setup>> {
  try { return await setRole(b); } catch (e) { if (e instanceof SignedOut) return { error: { code: "signed_out", message: "" } }; throw e; }
}
// "192.168.1.10" or "nas.local" is the default port on plain http; a typed http(s):// address is used as it is.
const toUrl = (a: string) => (/^https?:\/\//i.test(a) ? a : "http://" + (/:\d+$/.test(a) ? a : a + ":8484"));
const hostOf = (u: string) => u.replace(/^https?:\/\//i, "").replace(/\/.*$/, "").replace(/:8484$/, ""); // as typed, without the default port
const minutes = (iso: string | null) => { // the 15 s tick re-renders it
  const m = iso ? Math.max(1, Math.ceil((Date.parse(iso) - Date.now()) / 60000)) : 0;
  return m ? "Expires in " + m + (m === 1 ? " minute" : " minutes") : "";
};

type Problem = { kind: "unreachable" | "not_a_server"; url: string; reason: string };

// noMain: a server that is becoming a worker (Settings > This computer) gets no Main server card.
export function Enroll({ setup, reload, noMain }: { setup: Setup; reload: () => Promise<void>; noMain?: boolean }) {
  const [found, setFound] = useState<FoundServer[] | null>(null);
  const [pick, setPick] = useState(noMain ? "other" : "main"); // "main", "other" or a found server's url
  const [addr, setAddr] = useState(""), [code, setCode] = useState("");
  const [err, setErr] = useState(""), [busy, setBusy] = useState(false), [problem, setProblem] = useState<Problem | null>(null);
  const [, setNow] = useState(0);
  const me = useData(listWorkers, []), mine = me.data?.find((w) => w.kind === "local")?.appVersion;
  const head = useRef<HTMLHeadingElement>(null), focusPick = useRef(false), chose = useRef(false); // chose: the person picked a card

  const look = async () => {
    setFound(null);
    const r = await listServers(), list = failed(r) ? [] : r, ok = list.filter((s) => s.matchesVersion);
    setFound(list);
    if (chose.current) return; // a search that ends later never changes what the person picked (lead 08:04Z: Continue became Ask to join)
    setPick(list.length === 1 && ok.length === 1 ? ok[0].url : noMain ? "other" : "main"); // the server's card when exactly one is found
  };
  useEffect(() => { look(); }, []);
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 15000); return () => clearInterval(t); }, []);

  const j = setup.join, panel = problem?.kind ?? j?.state ?? null;
  // a new panel moves focus to its heading; Back returns it to the chosen card
  useEffect(() => { if (panel) head.current?.focus(); }, [panel]);
  useEffect(() => {
    if (panel || !focusPick.current) return;
    focusPick.current = false;
    document.querySelector<HTMLInputElement>(".enroll input[type=radio]:checked")?.focus();
  });

  const ask = async (url: string, pairCode?: string) => {
    setErr(""); setBusy(true);
    const r = await send(pairCode === undefined ? { role: "worker", serverUrl: url } : { role: "worker", serverUrl: url, pairCode });
    setBusy(false);
    if (!failed(r)) return (setProblem(null), reload());
    const c = r.error.code, name = j?.serverName ?? hostOf(url);
    if (c === "server_unreachable" || c === "not_a_server") setProblem({ kind: c === "server_unreachable" ? "unreachable" : "not_a_server", url, reason: r.error.message });
    else if (c === "pair_code_invalid") setErr("That code isn't right. Check it on " + name + "'s Workers page.");
    else if (c === "pair_code_expired") setErr("That code has expired. On " + name + ", press Add a worker for a new one.");
    else if (c !== "signed_out") setErr(r.error.message);
  };
  const back = async () => {
    setErr(""); setCode(""); focusPick.current = true;
    if (problem) return setProblem(null);
    setBusy(true); const r = await send({ role: "unset" }); setBusy(false);
    if (failed(r)) return r.error.code !== "signed_out" && setErr(r.error.message);
    await reload();
  };
  const go = async () => {
    setErr("");
    if (pick === "main") {
      setBusy(true); const r = await send({ role: "server" }); setBusy(false);
      if (failed(r)) return r.error.code !== "signed_out" && setErr(r.error.message);
      return reload();
    }
    if (pick === "other" && !addr.trim()) return setErr("Type the other server's address.");
    ask(pick === "other" ? toUrl(addr.trim()) : pick);
  };

  if (panel) {
    const url = problem?.url ?? j!.serverUrl, name = problem ? hostOf(url) : j!.serverName;
    const Back = <button className="btn" type="button" disabled={busy} onClick={back}>Back</button>;
    const [title, more, buttons] = ((): [string, ReactNode, ReactNode] => {
      switch (panel) {
        case "waiting": return ["Waiting for " + name + " to allow this worker.", <><p>On {name}, open Workers and press Allow.</p><p className="muted">{minutes(j!.expiresAt)}</p></>,
          <><a className="btn primary" href={url.replace(/\/$/, "") + "/workers"} target="_blank" rel="noreferrer">Open {name}'s Workers page<span className="sr"> (opens in a new tab)</span></a><button className="btn" type="button" disabled={busy} onClick={back}>Cancel request</button></>];
        case "allowed": return ["Joined " + name + ". This computer now converts videos for " + name + ".", <p>See what it&apos;s doing on {name}&apos;s Workers page.</p>, // design 04:36Z
          <a className="btn primary" href={url.replace(/\/$/, "") + "/workers"}>Open {name}&apos;s Workers page</a>];
        case "denied": return [name + " said no. Nothing was changed.", null, Back];
        case "expired": return ["No answer in 10 minutes.", null, <><button className="btn primary" type="button" disabled={busy} onClick={() => ask(url)}>Ask again</button>{Back}</>];
        case "unreachable": return ["Couldn't reach " + hostOf(url) + (problem?.reason ? ": " + problem.reason : "") + ".", <p>Check that it is on and on the same network.</p>,
          <><button className="btn primary" type="button" disabled={busy} onClick={() => ask(url)}>Try again</button>{Back}</>];
        case "not_a_server": return ["Nothing at " + hostOf(url) + " answered as a Shrink Your Shit server.", <p>Check the address.</p>, Back];
        case "not_local": return [name + " only takes join requests from its own network.", <>
          <p>Use a pairing code instead: on {name} open Workers and press Add a worker.</p>
          <label className="field"><span className="lab">Pairing code</span><input className="input" inputMode="numeric" autoComplete="off" value={code} onChange={(e) => setCode(e.target.value)} /></label></>,
          <><button className="btn primary" type="submit" disabled={busy || !code.trim()}>Join with code</button>{Back}</>];
        case "not_saved": return [name + " allowed this worker, but this computer couldn't finish joining.", <p>Ask again to finish.</p>, // design 03:28Z
          <><button className="btn primary" type="button" disabled={busy} onClick={() => ask(url)}>Ask again</button>{Back}</>];
        // a state this page doesn't know yet (a newer server) must not blank the page
        default: return ["Joining " + name + " didn't finish (" + String(panel) + "). Go back and try again.", null, Back]; // design 03:28Z: raw state kept for support
      }
    })();
    return (
      <form className="enroll stack" onSubmit={(e) => { e.preventDefault(); if (panel === "not_local") ask(url, code.trim()); }}>
        <section className="card"><div className="bd stack" aria-live="polite">
          <h2 ref={head} tabIndex={-1}>{title}</h2>{more}
          {err && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{err}</span></p>}
          <div className="row enroll-btns">{buttons}</div>
        </div></section>
      </form>
    );
  }

  const card = (value: string, title: string, text: ReactNode, off?: string) => (
    <label key={value} className={"card radio enroll-card" + (pick === value ? " sel" : "") + (off ? " off" : "")}>
      <input type="radio" name="enroll" value={value} checked={pick === value} disabled={!!off} onClick={() => (chose.current = true)} onChange={() => (setPick(value), setErr(""))} />
      <span><strong>{title}</strong><span className="hint">{text}</span>{off && <span className="hint">{off}</span>}</span>
    </label>
  );
  return (
    <form className="enroll stack" onSubmit={(e) => (e.preventDefault(), go())}>
      <h1 id="enroll-h">How will this computer be used?</h1>
      <div className="stack" role="radiogroup" aria-labelledby="enroll-h">
        {!noMain && card("main", "Main server", "Keeps your libraries and this web page. Choose this for your first computer.")}
        {(found ?? []).map((s) => card(s.url, "Join \"" + s.name + "\" at " + s.address, "Becomes a worker: " + s.name + " sends it videos to convert. " + s.name + " · version " + s.version,
          s.matchesVersion ? undefined : s.name + " runs version " + s.version + (mine ? " and this is " + mine : "") + ". Update one of them so they match."))}
        {card("other", "Join another server", "Type its address")}
      </div>
      {pick === "other" && <label className="field"><span className="lab">Address</span>
        <input className="input" autoFocus placeholder="192.168.1.10 or nas.local" value={addr} onChange={(e) => setAddr(e.target.value)} />
        <span className="hint">Find it in the address bar when you open Shrink Your Shit on that computer.</span></label>}
      <p className="muted" aria-live="polite">{found === null ? "Looking for Shrink Your Shit servers on your network..."
        : !found.length && <>No other servers found on your network. <button className="linkbtn" type="button" onClick={look}>Look again</button></>}</p>
      {err && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{err}</span></p>}
      <div className="row enroll-btns"><button className="btn primary" type="submit" disabled={busy}>{pick === "main" ? "Continue" : "Ask to join"}</button></div>
    </form>
  );
}

// A worker's own page, at every address (section 11, fallback 00:43Z: no status endpoint yet).
export function WorkerHome({ setup }: { setup: Setup }) {
  const name = setup.join?.serverName, url = setup.join?.serverUrl;
  const Change = <a className="btn" href="/settings/general">Change role</a>;
  if (!name || !url) return ( // the server isn't known (after a restart, until the API keeps it): design 04:53Z
    <div className="enroll stack">
      <h1>This computer is a worker</h1>
      <p>It converts videos for a server on your network. Open that server&apos;s Workers page to see what it&apos;s doing.</p>
      <div className="row enroll-btns">{Change}</div>
    </div>
  );
  return ( // design 04:52Z: the same words as the joined page
    <div className="enroll stack">
      <h1>This computer is a worker for {name}</h1>
      <p>This computer now converts videos for {name}.</p>
      <p>See what it&apos;s doing on {name}&apos;s Workers page.</p>
      <div className="row enroll-btns">
        <a className="btn primary" href={url.replace(/\/$/, "") + "/workers"}>Open {name}&apos;s Workers page</a>{Change}
      </div>
    </div>
  );
}
