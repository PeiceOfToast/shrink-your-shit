// Settings > Connections (SCREENS V9e, mockups/settings-connections.html): tell Sonarr, Radarr, Plex and Jellyfin when a
// file changes. One of each app in this version. Contract "Sonarr / Radarr" and "Plex / Jellyfin" (workshop 11-19).
import { useState } from "react";
import { Icon } from "../Icon";
import { failed, type Answer } from "../api";
import { LoadFailed, PageHead } from "../PageHead";
import { SettingsTabs } from "../pages";
import { SettingRow, ValueChip } from "../SettingRow";
import { useData } from "../useData";
import { clock } from "../format";
import { addIntegration, listIntegrations, patchIntegration, removeIntegration, testIntegration } from "../connectionsApi";
import type { Integration, IntegrationKind, IntegrationTestResult, PathMapping } from "../types/connections";

// per app: the key's name and where to find it, what a change makes it do, and folder examples (theirs, ours)
const APP: Record<IntegrationKind, { name: string; port: number; key: string; where: string; does: string; dirs: [string, string] }> = {
  sonarr: { name: "Sonarr", port: 8989, key: "API key", where: "In Sonarr: Settings > General > Security.", does: "Rescans each converted file, so it doesn't download it again.", dirs: ["/tv", "/mnt/media/tv"] },
  radarr: { name: "Radarr", port: 7878, key: "API key", where: "In Radarr: Settings > General > Security.", does: "Rescans each converted file, so it doesn't download it again.", dirs: ["/movies", "/mnt/media/movies"] },
  plex: { name: "Plex", port: 32400, key: "Token", where: "Plex's help page \"Finding an authentication token\" shows where it is.", does: "Scans the folder of each converted file, so Plex plays the new one.", dirs: ["/data/movies", "/mnt/media/movies"] },
  jellyfin: { name: "Jellyfin", port: 8096, key: "API key", where: "In Jellyfin: Dashboard > Advanced > API Keys > +.", does: "Tells Jellyfin each converted file changed, so it plays the new one.", dirs: ["/media/movies", "/mnt/media/movies"] },
};
const KINDS = Object.keys(APP) as IntegrationKind[];
const dot = (s: string) => s.replace(/[.\s]+$/, "");

// The test's one sentence (V9e): built from its folders when it connected; the server's own words when it didn't.
export function testSentence(kind: IntegrationKind, r: IntegrationTestResult): string {
  const app = APP[kind].name;
  if (!r.ok) return dot(r.message) + ".";
  const head = r.version ? `Connected to ${app} ${r.version}.` : `Connected to ${app}.`;
  if (!r.folders.length) return head;
  const more = (n: number) => (n > 1 ? ` and ${n - 1} more` : "");
  const gone = r.folders.filter((f) => f.ourPath === null), out = r.folders.filter((f) => f.ourPath !== null && !f.inLibrary);
  if (gone.length) return `${head} Its folder ${gone[0].theirPath}${more(gone.length)} ${gone.length > 1 ? "don't" : "doesn't"} exist here. If ${app} runs in Docker, add it under Folders.`;
  if (out.length) return `${head} Its folder ${out[0].theirPath}${more(out.length)} ${out.length > 1 ? "aren't" : "isn't"} in any library here. Add it under Folders, unless you don't convert those files.`;
  return `${head} ${r.folders.length === 1 ? "Its folder is" : `Its ${r.folders.length} folders are`} in your libraries.`;
}

function Status({ it }: { it: Integration | undefined }) {
  if (!it) return <span className="st muted">Not set up</span>; // design V9e: not an error, so no icon
  if (!it.enabled) return <span className="st"><Icon id="i-pause" />Turned off</span>;
  const r = it.lastResult;
  if (!r) return <span className="st"><Icon id="i-clock" />Saved · not tested yet</span>;
  if (r.ok) return <span className="st ok"><Icon id="i-check" />Connected · last told {clock(r.at)}</span>;
  return <span className="st warn"><Icon id="i-alert" /><span className="fail">Failed at {clock(r.at)}: <span className="why">{dot(r.message)}.</span></span></span>;
}

// connected but a folder is missing or outside the libraries: a warning, not a tick
const gap = (r: IntegrationTestResult) => r.ok && r.folders.some((f) => f.ourPath === null || !f.inLibrary);
type Tone = "ok" | "warn" | "bad";
function Result({ tone, text }: { tone: Tone; text: string }) {
  return <p className={"st " + tone} style={{ margin: 0 }}><Icon id={tone === "ok" ? "i-check" : "i-alert"} /><span>{text}</span></p>;
}

function Form({ kind, it, onDone, onCancel, onTested }: { kind: IntegrationKind; it?: Integration; onDone: (m: { tone: Tone; text: string }) => void; onCancel: () => void; onTested: () => void }) {
  const app = APP[kind].name, id = "c-" + kind;
  const [url, setUrl] = useState(it?.url ?? ""), [key, setKey] = useState(""), [changeKey, setChangeKey] = useState(!it?.hasKey);
  const [maps, setMaps] = useState<PathMapping[]>(it?.pathMappings.length ? it.pathMappings : []);
  const [busy, setBusy] = useState(false), [result, setResult] = useState<{ tone: Tone; text: string } | null>(null), [error, setError] = useState("");
  const rows = maps.filter((m) => m.theirPath.trim() || m.ourPath.trim()).map((m) => ({ theirPath: m.theirPath.trim(), ourPath: m.ourPath.trim() }));
  // pass 5 #4: why Test is disabled, from the same conditions as its disabled prop below
  const testWhy = !url.trim() && !it && !key ? "Fill in the address and API key to test." : !url.trim() ? "Fill in the address to test." : !it && !key ? "Fill in the API key to test." : "";
  const setMap = (i: number, p: Partial<PathMapping>) => setMaps(maps.map((m, j) => (j === i ? { ...m, ...p } : m)));
  const run = async <T,>(p: Promise<Answer<T>>): Promise<T | null> => {
    try { const r = await p; if (failed(r)) { setError(r.error.message); return null; } return r; } catch (_) { setError("Couldn't reach Shrink Your Shit. Try again."); return null; }
  };
  const test = async () => {
    setBusy(true); setError(""); setResult(null);
    // nothing changed: a plain Test of the saved one, which also updates the card's status (contract: only that sets lastResult)
    const same = it && url.trim() === it.url && !key && JSON.stringify(rows) === JSON.stringify(it.pathMappings);
    const r = await run(!it ? testIntegration({ kind, url: url.trim(), apiKey: key, pathMappings: rows })
      : same ? testIntegration({ id: it.id }) : testIntegration({ id: it.id, url: url.trim(), ...(key && { apiKey: key }), pathMappings: rows }));
    if (r && same) onTested();
    if (r) setResult({ tone: !r.ok ? "bad" : gap(r) ? "warn" : "ok", text: testSentence(kind, r) });
    setBusy(false);
  };
  const save = async () => {
    setBusy(true); setError(""); setResult(null);
    const saved = await run(it ? patchIntegration(it.id, { url: url.trim(), pathMappings: rows, ...(key && { apiKey: key }) })
      : addIntegration({ kind, name: app, url: url.trim(), apiKey: key, pathMappings: rows }));
    if (!saved) { setBusy(false); return; }
    const r = await run(testIntegration({ id: saved.id })); // Save runs the same test; this one sets lastResult
    setBusy(false);
    onDone(r?.ok ? { tone: gap(r) ? "warn" : "ok", text: "Saved. " + testSentence(kind, r) } : { tone: "warn", text: `Saved, but ${app} didn't answer, so files won't be refreshed until it does.` });
  };
  return (
    <form className="stack cform" onSubmit={(e) => { e.preventDefault(); save(); }} onInput={() => setResult(null)}>
      <div className="field">
        <label htmlFor={id + "-url"}>Address</label>
        <input className="input mono" id={id + "-url"} type="url" inputMode="url" autoComplete="off" spellCheck={false} required value={url}
          onChange={(e) => setUrl(e.target.value)} placeholder={`http://192.168.1.20:${APP[kind].port}`} />
      </div>
      <div className="field">
        {changeKey ? <label htmlFor={id + "-key"}>{APP[kind].key}</label> : <span className="lab">{APP[kind].key}</span>}
        {changeKey
          ? <input className="input mono" id={id + "-key"} type="password" autoComplete="off" required={!it} value={key} onChange={(e) => setKey(e.target.value)}
            placeholder={it ? "Leave empty to keep the saved key." : ""} />
          : <p className="row" style={{ margin: 0, gap: 8 }} id={id + "-key"}><span className="st ok"><Icon id="i-check" />Saved</span>
            <button className="btn sm ghost" type="button" onClick={() => setChangeKey(true)} aria-label={"Change the saved " + APP[kind].key.toLowerCase()}>Change</button></p>}
        <span className="hint">{APP[kind].where}</span>
      </div>
      <details className="cfold" open={maps.length > 0 || undefined}>
        <summary><strong>Folders</strong> <span className="muted">Only needed if {app} sees your files under a different path, for example in Docker.</span></summary>
        <div className="stack" style={{ marginTop: 8 }}>
          {maps.map((m, i) => (
            <div className="cmap" key={i}>
              <label className="field"><span className="hint">{app} sees</span>
                <input className="input mono" value={m.theirPath} onChange={(e) => setMap(i, { theirPath: e.target.value })} placeholder={APP[kind].dirs[0]} /></label>
              <Icon id="i-arrow-r" />
              <label className="field"><span className="hint">Shrink Your Shit sees</span>
                <input className="input mono" value={m.ourPath} onChange={(e) => setMap(i, { ourPath: e.target.value })} placeholder={APP[kind].dirs[1]} /></label>
              <button className="btn sm icon ghost" type="button" aria-label={"Remove folder " + (i + 1)} onClick={() => { setMaps(maps.filter((_, j) => j !== i)); setResult(null); }}><Icon id="i-x" /></button>
            </div>))}
          {!maps.length && <p className="hint" style={{ margin: 0 }}>With no folders here, both see the same paths.</p>}
          <p style={{ margin: 0 }}><button className="btn sm" type="button" onClick={() => setMaps([...maps, { theirPath: "", ourPath: "" }])}><Icon id="i-plus" />Add a folder</button></p>
        </div>
      </details>
      <div aria-live="polite">{result && <Result tone={result.tone} text={result.text} />}{error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}</div>
      <div className="row" style={{ gap: 8 }}>
        <button className="btn" type="button" disabled={busy || !url.trim() || (!it && !key)} onClick={test}>Test</button>
        <button className="btn primary" type="submit" disabled={busy}>{busy ? "Working…" : "Save"}</button>
        <button className="btn ghost" type="button" onClick={onCancel} disabled={busy}>Cancel</button>
      </div>
      {testWhy && !busy && <p className="muted sm" style={{ margin: 0 }}>{testWhy}</p>}
    </form>);
}

// One app as a row of the Media apps card: name and hint on the left (the URL as a chip once set), status and actions on
// the right. Its confirm and form open underneath, inside the card.
function AppRow({ kind, it, first, reload, said }: { kind: IntegrationKind; it?: Integration; first: boolean; reload: () => void; said: (m: string) => void }) {
  const app = APP[kind].name, [open, setOpen] = useState(false), [confirm, setConfirm] = useState(false), [msg, setMsg] = useState(""), [saved, setSaved] = useState<{ tone: Tone; text: string } | null>(null);
  const remove = async () => {
    const r = await removeIntegration(it!.id);
    if (failed(r)) { setMsg(r.error.message); return; }
    setConfirm(false); said(app + " removed. Files won't be refreshed there any more."); reload();
  };
  return (
    <div style={first ? undefined : { borderTop: "1px solid var(--border)" }}>
      <SettingRow title={app} desc={<>{APP[kind].does}{it && <> <ValueChip><span style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{it.url}</span></ValueChip></>}</>}>
        <Status it={it} />
        {!open && !confirm && (it
          ? <>
            <button className="btn sm" type="button" onClick={() => { setOpen(true); setSaved(null); }} aria-label={"Edit " + app}>Edit</button>
            <button className="btn sm ghost" type="button" onClick={() => { setConfirm(true); setSaved(null); }} aria-label={"Remove " + app}>Remove</button></>
          : <button className="btn primary sm" type="button" onClick={() => { setOpen(true); setSaved(null); }} aria-label={"Connect " + app}>Connect</button>)}
      </SettingRow>
      {/* the live region stays mounted so a screen reader announces the text when it arrives */}
      <div role="status" style={saved || confirm || open ? { padding: "0 var(--pad) 12px" } : undefined}>
        {saved && <Result tone={saved.tone} text={saved.text} />}
      </div>
      {confirm && <div className="row" role="group" aria-label={"Remove " + app} style={{ gap: 8, padding: "0 var(--pad) 12px" }}>
        <span>Remove {app}? It won't be told about converted files.</span>
        <button className="btn sm danger" type="button" onClick={remove}>Remove</button>
        <button className="btn sm ghost" type="button" onClick={() => setConfirm(false)}>Cancel</button>
        {msg && <span className="st bad"><Icon id="i-alert" />{msg}</span>}
      </div>}
      {open && <div style={{ padding: "0 var(--pad) 12px" }}><Form kind={kind} it={it} onTested={reload} onCancel={() => setOpen(false)} onDone={(m) => { setOpen(false); setSaved(m); said(""); reload(); }} /></div>}
    </div>);
}

export function ConnectionsSettings() {
  const d = useData(listIntegrations, []), [said, setSaid] = useState("");
  const notHere = d.error && (d.error.code === "http_404" || d.error.code === "not_found");
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="connections" />
      <p className="muted" style={{ margin: 0 }}>Tell your media apps when a file changes, so they show the new file and Sonarr and Radarr don't download it again.</p>
      <p className="muted" role="status" style={{ margin: 0 }}>{said}</p>
      {notHere ? <p className="muted">This server can't connect to media apps yet.</p>
        : d.error && !d.data ? <LoadFailed error={d.error} reload={d.reload} />
          : !d.data ? <div className="empty">Loading…</div>
            : <>
              <section className="card" aria-labelledby="h-media">
                <div className="hd"><h2 id="h-media">Media apps</h2></div>
                <div className="bd" style={{ padding: 0 }}>
                  {KINDS.map((k, i) => <AppRow key={k} kind={k} first={i === 0} it={d.data!.find((x) => x.kind === k)} reload={d.reload} said={setSaid} />)}
                </div>
              </section>
              {/* contract "Plex / Jellyfin": said once on the screen */}
              <p className="hint" style={{ margin: 0 }}>Changing a file's container (.mp4 to .mkv) can reset its watched state in Jellyfin, and sometimes in Plex.</p></>}
    </div>);
}
