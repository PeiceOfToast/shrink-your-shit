// Settings > General and Safety (SCREENS V9) on the real server's settings (v1's, at /api/v2/settings): the old pages'
// settings and words (settings.json), the new interface's look. General: Your languages, the access token (shown only
// when asked for, never kept by the page; a new one; Sign out, only when the server needs a token). Safety: how long
// replaced originals are kept, where, and what the trash holds now (its whole size lives here, not beside Space saved:
// design 05:34Z). Not here: log retention (no setting on the server).
import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { PageHead } from "../PageHead";
import { SignedOut, failed, type Answer } from "../api";
import { bytes, plural } from "../format";
import { getHealth, getSettings, getToken, getTrash, patchSettings, rotateToken, signOut } from "../libraryApi";
import { SettingsTabs } from "../pages";
import { navigate } from "../router";
import { useData } from "../useData";

async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}
const day = (t: string) => new Date(t).toLocaleDateString([], { day: "numeric", month: "short" });

export function GeneralSettings() {
  const st = useData(getSettings, []);
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="general" />
      {!st.data ? <div className="empty">{st.error?.message ?? "Loading..."}</div> : <>
        <Languages start={st.data.languages ?? []} saved={st.reload} />
        <Access auth={!!st.data.authRequired} />
      </>}
    </div>);
}

// "Your languages": one list for the whole install, shown by name, saved as ISO 639-2 codes (/assets/languages.json:
// [code, name, name in the language]); added by typing a name and picking it.
function Languages({ start, saved }: { start: string[]; saved: () => void }) {
  const [codes, setCodes] = useState(start), [list, setList] = useState<[string, string, string?][]>([]), [typed, setTyped] = useState(""), [said, setSaid] = useState(""), [bad, setBad] = useState(false);
  useEffect(() => { fetch("/assets/languages.json").then((r) => r.json()).then(setList, () => {}); }, []);
  const name = (c: string) => list.find((l) => l[0] === c)?.[1] ?? c;
  const add = () => {
    const t = typed.trim().toLowerCase(); if (!t) return;
    const hit = list.find((l) => l[1].toLowerCase() === t || (l[2] ?? "").toLowerCase() === t || l[0] === t);
    if (!hit) return (setBad(true), setSaid("No language called " + typed.trim() + ". Pick one from the list."));
    if (!codes.includes(hit[0])) setCodes([...codes, hit[0]]);
    setTyped(""); setBad(false); setSaid(hit[1] + " added. Press Save to keep it.");
  };
  const save = async () => { setBad(false); if (await act(patchSettings({ languages: codes }), (m) => (setBad(true), setSaid(m)))) (setSaid("Saved."), saved()); };
  const dirty = JSON.stringify(codes) !== JSON.stringify(start);
  return (
    <section className="card" aria-labelledby="lang-h"><div className="hd"><h2 id="lang-h">Your languages</h2></div>
      <div className="bd stack">
        {codes.length ? <ul className="chips" aria-label="Your languages" style={{ listStyle: "none", padding: 0, margin: 0, flexWrap: "wrap" }}>{codes.map((c) => (
          <li key={c} className="mchip">{name(c)} <button className="btn sm icon ghost" type="button" aria-label={"Remove " + name(c)} onClick={() => (setCodes(codes.filter((x) => x !== c)), setSaid(name(c) + " removed. Press Save to keep it."))}><Icon id="i-x" /></button></li>))}</ul>
          : <p className="muted">None chosen yet.</p>}
        <div className="field"><label htmlFor="lang-add">Add a language</label>
          <div className="row" style={{ gap: 8 }}>
            <input id="lang-add" className="input" list="lang-names" autoComplete="off" value={typed} aria-describedby="lang-h2"
              onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === "Enter" && (e.preventDefault(), add())} style={{ flex: 1, minWidth: 0 }} />
            <button className="btn" type="button" onClick={add}>Add</button></div>
          <datalist id="lang-names">{list.map((l) => <option key={l[0]} value={l[1]} label={l[2]} />)}</datalist>
          <span className="hint" id="lang-h2">Audio and subtitles in these languages are kept when a library uses "Keep only my languages". Tracks with no language set are always kept.</span></div>
        <p className={bad ? "st bad" : "muted"} role="status">{said}</p>
        <div className="row"><button className="btn primary" type="button" disabled={!dirty} onClick={save}>Save</button></div>
      </div>
    </section>);
}

// Access: the token only when asked for; a new one asks first; Sign out only when the server needs a token (IA).
function Access({ auth }: { auth: boolean }) {
  const [tok, setTok] = useState<string | null>(null), [ask, setAsk] = useState(false), [said, setSaid] = useState("");
  const show = async () => { if (tok) return setTok(null); const r = await act(getToken(), setSaid); if (r) setTok(r.token); };
  const make = async () => { setAsk(false); const r = await act(rotateToken(), setSaid); if (r) (setTok(r.token), setSaid("New token made. Use it to sign in on other devices.")); };
  const out = async () => { if (await act(signOut(), setSaid)) navigate("/signin"); };
  return (
    <section className="card" aria-labelledby="acc-h"><div className="hd"><h2 id="acc-h">Access</h2></div>
      <div className="bd stack">
        <div className="field"><label htmlFor="tok">Access token</label>
          <div className="row" style={{ gap: 8 }}>
            <input id="tok" className="input mono" readOnly value={tok ?? "••••••••••••"} aria-describedby="tok-h" style={{ flex: 1, minWidth: 0 }} />
            <button className="btn" type="button" aria-controls="tok" onClick={show}>{tok ? "Hide" : "Show"}</button>
            {tok && navigator.clipboard && <button className="btn" type="button" onClick={() => navigator.clipboard.writeText(tok).then(() => setSaid("Copied."), () => {})}>Copy</button>}
            <button className="btn" type="button" onClick={() => setAsk(true)}>Make a new one</button></div>
          <span className="hint" id="tok-h">{auth ? "" : "Not needed on this computer. "}Needed to open shrink-your-shit from another device.</span></div>
        {ask && <div className="note" role="alertdialog" aria-labelledby="tok-ask"><Icon id="i-alert" /><div className="sp"><strong id="tok-ask">Make a new token?</strong> {auth ? "The old one stops working, so other devices must use the new one." : "This server doesn't ask for one now. Making one turns sign-in on for every browser and script, and they will need the token from then on."}</div>
          <button className="btn sm primary" type="button" onClick={make}>Make it</button><button className="btn sm ghost" type="button" onClick={() => setAsk(false)}>Cancel</button></div>}
        <p className="muted" role="status">{said}</p>
        {auth && <div className="row"><button className="btn" type="button" onClick={out}>Sign out of this browser</button></div>}
      </div>
    </section>);
}

export function SafetySettings() {
  const st = useData(getSettings, []), tr = useData(getTrash, [], 60000);
  const [days, setDays] = useState<string | null>(null), [said, setSaid] = useState(""), [bad, setBad] = useState(false);
  const now = days ?? String(st.data?.keepOriginalsDays ?? ""), n = Number(now), okDays = now.trim() !== "" && Number.isInteger(n) && n >= 0 && n <= 365;
  const save = async () => {
    setBad(false);
    if (!okDays) return (setBad(true), setSaid("Enter a whole number of days, from 0 to 365."));
    if (await act(patchSettings({ keepOriginalsDays: n }), (m) => (setBad(true), setSaid(m)))) (setSaid("Saved."), setDays(null), st.reload());
  };
  const t = tr.data;
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="safety" />
      {!st.data ? <div className="empty">{st.error?.message ?? "Loading..."}</div> : (
        <section className="card" aria-labelledby="keep-h"><div className="hd"><h2 id="keep-h">Keeping originals</h2></div>
          <div className="bd stack">
            <div className="field"><label htmlFor="keep-days">Keep originals for (days)</label>
              <input id="keep-days" className="input" type="number" inputMode="numeric" min={0} max={365} step={1} value={now} onChange={(e) => setDays(e.target.value)} aria-describedby="keep-h2" style={{ maxWidth: 140 }} />
              <span className="hint" id="keep-h2">0 means the original is deleted right after the new file has been checked.</span></div>
            <p className={bad ? "st bad" : "muted"} role="status">{said}</p>
            <div className="row"><button className="btn primary" type="button" disabled={days == null || Number(days) === st.data.keepOriginalsDays} onClick={save}>Save</button></div>
            {st.data.trashPath && <p><strong>Where originals are kept:</strong> {st.data.trashPath}</p>}
            <p aria-live="polite">{!t ? (tr.error?.message ?? "") : t.files ? <><strong>Kept originals:</strong> {bytes(t.bytes)} · {plural(t.files, "file", "files")}.{t.nextDeleteAt && " The next ones go on " + day(t.nextDeleteAt) + "."}</> : "No originals are being kept right now."}</p>
          </div>
        </section>)}
    </div>);
}

// Integrations (SCREENS V9): the server has none yet (Sonarr, Radarr, Plex, Jellyfin, notifications, the update check:
// 'not available'), so this tab only says so, with design's promise. Nothing here is a control that does nothing.
export function IntegrationsSettings() {
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="integrations" />
      <section className="card"><div className="bd stack">
        <p><strong>Shrink Your Shit sends nothing anywhere unless you turn something on here.</strong></p>
        <p className="muted">Connections to Sonarr, Radarr, Plex and Jellyfin, notifications and the update check come in a later version. Until then, nothing is sent.</p>
      </div></section>
    </div>);
}

// Backup (SCREENS V9): the server answers backups with 501 not_available for now, so this tab only says so (design's
// words); no button that can't work.
export function BackupSettings() {
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="backup" />
      <section className="card"><div className="bd stack">
        <p><strong>This backs up settings, not your videos.</strong></p>
        <p className="muted">Exporting your settings and workflows, and importing them with a preview of what changes, come in a later version.</p>
      </div></section>
    </div>);
}

// About (SCREENS V9: version, health report, licences): the old Settings > System's health checks and log lines.
const BADGE: Record<string, [string, string]> = { ok: ["ok", "OK"], warn: ["warn", "Heads up"], bad: ["bad", "Fix this"] };
export function AboutSettings() {
  const h = useData(getHealth, []);
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="about" />
      <section className="card" aria-labelledby="hc-h"><div className="hd"><h2 id="hc-h">Health checks</h2><span className="sp" /><button className="btn sm" type="button" onClick={h.reload}>Check again</button></div>
        <div className="bd stack">
          {!h.data ? <p className="muted" role="status">{h.error?.message ?? "Checking..."}</p> : <>
            <ul className="stack" style={{ listStyle: "none", padding: 0, margin: 0 }} aria-live="polite" aria-label="Health checks">
              {h.data.checks.map((c) => { const [cls, word] = BADGE[c.status] ?? BADGE.warn; return <li key={c.id} className="row nw" style={{ alignItems: "baseline", gap: 8 }}><span className={"pill " + cls}>{word}</span><span>{c.label}{c.detail ? ". " + c.detail : ""}</span></li>; })}</ul>
            <p className="muted">Shrink Your Shit {h.data.version}</p></>}
        </div></section>
      <section className="card" aria-labelledby="log-h"><div className="hd"><h2 id="log-h">Recent log lines</h2></div>
        <div className="bd"><pre className="mono sm" tabIndex={0} role="region" aria-labelledby="log-h" style={{ overflow: "auto", maxHeight: "16rem", margin: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{h.data ? (h.data.logsTail.length ? h.data.logsTail.join("\n") : "Nothing has been logged yet.") : ""}</pre></div></section>
      <section className="card" aria-labelledby="lic-h"><div className="hd"><h2 id="lic-h">Licences</h2></div>
        <div className="bd stack">
          <p>Shrink Your Shit is free software under the GNU Affero General Public License, version 3.</p>
          <p><a href="/next/licenses.md" target="_blank" rel="noopener">The licences of the parts this interface is built with</a> (React, React Flow and others).</p>
        </div></section>
    </div>);
}
