// D: Network shares settings (SCREENS V9d, D-API-SHAPE.md). Mock only (the engine is after item E).
// The share list table, Add/Edit sheet (drawer), Remove dialog, Test button, and the connect-mode-specific UI.
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { MOCK, failed, type Answer } from "../api";
import { Empty, PageHead } from "../PageHead";
import { SettingsTabs } from "../pages";
import { useData } from "../useData";
import { addShare, checkShare, deleteShare, getShares, patchShare, reconnectShare, testShare } from "../sharesApi";
import type { Share, ShareTestResult, SharesResponse, ShareType } from "../types/shares";

async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (_) { return null; }
}

const TYPE: Record<ShareType, { label: string; hint: string }> = {
  smb: { label: "SMB", hint: "Windows shares and most NAS boxes (Synology, QNAP, TrueNAS, Unraid)." },
  nfs: { label: "NFS", hint: "Linux servers and NAS boxes set up for NFS." },
};

function ShareRow({ share, resp, onChanged, said }: { share: Share; resp: SharesResponse; onChanged: () => void; said: (m: string) => void }) {
  const [menu, setMenu] = useState(false), [removing, setRemoving] = useState(false), [editing, setEditing] = useState(false);
  const libs = share.libraries.map((l) => l.name).join(", ");
  const statusClass = share.status === "connected" ? "ok" : share.status === "not_connected" ? "bad" : share.status === "waiting_setup" ? "warn" : "";
  const statusIcon = share.status === "connected" ? "i-check-c" : share.status === "connecting" || share.status === "waiting_setup" ? "i-clock" : "i-alert";
  const statusText = share.status === "connected" ? "Connected" : share.status === "connecting" ? "Connecting..." : share.status === "waiting_setup" ? "Waiting for setup on the server" : "Not connected" + (share.since ? " since " + new Date(share.since).toLocaleDateString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) : "");
  const canReconnect = share.status !== "connected" && resp.mode === "app";
  const canCheck = share.status !== "connected" && (resp.mode === "manual" || resp.mode === "docker");
  const reconnect = async () => { const r = await act(canReconnect ? reconnectShare(share.id) : checkShare(share.id), said); if (r) { said("Done."); onChanged(); } };

  return (
    <tr>
      <td className="idc"><button className="ttl linkbtn" type="button" onClick={() => setEditing(true)}>{share.name}</button>
        <span className="sub mono">{share.address}</span>
        {share.libraries.length > 0 && <span className="sub phone">Used by {libs}</span>}
        <span className="sub phone">{TYPE[share.type].label}</span></td>
      <td data-p="2">{TYPE[share.type].label}</td>
      <td className="stc"><span className={"st " + statusClass}><Icon id={statusIcon} /><span>{statusText}</span></span>
        {share.reason && <span className="sub over">{share.reason.message}</span>}
        {share.status === "waiting_setup" && share.setup && <div className="codebox over" style={{ marginTop: 8 }}><code>{share.setup.text}</code><button className="btn sm over" type="button" onClick={() => navigator.clipboard?.writeText(share.setup!.text).catch(() => {})}><Icon id="i-copy" />Copy</button></div>}
        {(canReconnect || canCheck) && <div className="row over" style={{ marginTop: 8 }}>{canReconnect && <button className="btn sm over" type="button" onClick={reconnect}><Icon id="i-refresh" />Reconnect</button>}{canCheck && <button className="btn sm over" type="button" onClick={reconnect}><Icon id="i-refresh" />Check again</button>}</div>}
      </td>
      <td data-p="2">{libs ? <a href="#">{libs}</a> : <span className="muted">Not used</span>}</td>
      <td className="actc">
        <details className="menu" style={{ display: menu ? undefined : "inline-block" }}>
          <summary className="btn icon ghost sm" aria-label={"Actions for " + share.name} onClick={() => setMenu(!menu)} style={{ display: "inline-flex" }}><Icon id="i-more" /></summary>
          {menu && <div className="pop over" role="menu">
            {canReconnect && <button className="it" type="button" onClick={() => (setMenu(false), reconnect())}>Reconnect</button>}
            {canCheck && <button className="it" type="button" onClick={() => (setMenu(false), reconnect())}>Check again</button>}
            <button className="it" type="button" onClick={() => (setMenu(false), setEditing(true))}>Edit</button>
            <button className="it" type="button" onClick={() => (setMenu(false), setRemoving(true))}>Remove...</button>
          </div>}
        </details>
        {removing && <RemoveDialog share={share} onCancel={() => setRemoving(false)} onDone={() => { setRemoving(false); onChanged(); }} said={said} />}
        {editing && <ShareSheet share={share} resp={resp} onDone={() => { setEditing(false); onChanged(); }} onCancel={() => setEditing(false)} said={said} />}
      </td>
    </tr>
  );
}

function RemoveDialog({ share, onCancel, onDone, said }: { share: Share; onCancel: () => void; onDone: () => void; said: (m: string) => void }) {
  const [error, setError] = useState("");
  const remove = async () => {
    const r = await act(deleteShare(share.id), setError);
    if (r === undefined) { said("Removed."); onDone(); }
  };
  return (
    <div className="drawer-wrap" onMouseDown={(e) => e.target === e.currentTarget && onCancel()}>
      <aside className="drawer" tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="rm-s" onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), onCancel())}>
        <div className="hd"><h3 id="rm-s" style={{ margin: 0 }}>Remove {share.name}?</h3><button className="btn icon ghost sm" type="button" aria-label="Close" onClick={onCancel}><Icon id="i-x" /></button></div>
        <div className="bd">
          <p style={{ margin: 0 }}>Shrink Your Shit disconnects from it. Nothing on the share is changed or deleted.{share.setup ? " Also remove it from the server:" : ""}</p>
          {share.setup && <div className="codebox"><code>{share.setup.removeText}</code><button className="btn sm" type="button" onClick={() => navigator.clipboard?.writeText(share.setup!.removeText).catch(() => {})}><Icon id="i-copy" />Copy</button></div>}
          {share.libraries.length > 0 && <p className="st bad" style={{ margin: 0 }}><Icon id="i-alert" /><span>{share.libraries.map((l) => l.name).join(" and ")} use this share. Change their folders or remove them first.</span></p>}
          {error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}
          <div className="row" style={{ gap: 8 }}>
            {share.libraries.length === 0 && <button className="btn primary" type="button" onClick={remove}><Icon id="i-x" />Remove share</button>}
            <button className="btn ghost" type="button" onClick={onCancel}>Keep it</button>
          </div>
        </div>
      </aside>
    </div>
  );
}

function ShareSheet({ share, resp, onDone, onCancel, said }: { share?: Share; resp: SharesResponse; onDone: (share?: Share) => void; onCancel: () => void; said: (m: string) => void }) {
  const edit = !!share;
  const [type, setType] = useState<ShareType>(share?.type ?? "smb");
  const [address, setAddress] = useState(share?.address ?? "");
  const [name, setName] = useState(share?.name ?? "");
  const [guest, setGuest] = useState(share?.guest ?? false);
  const [username, setUsername] = useState(share?.username ?? "");
  const [password, setPassword] = useState("");
  const [showPw, setShowPw] = useState(false);
  const [testResult, setTestResult] = useState<ShareTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [savedShare, setSavedShare] = useState<Share | null>(null);
  const box = useRef<HTMLElement>(null);

  const testNow = async () => {
    setTesting(true); setTestResult(null); setError("");
    const r = await act(testShare({ type, address, guest: type === "nfs" ? true : guest, username: guest || type === "nfs" ? undefined : username, password, ...(edit && { id: share.id }) }), setError);
    setTesting(false);
    if (r) setTestResult(r);
  };
  const save = async () => {
    if (!address.trim()) return void setError("Enter the server and the shared folder.");
    setSaving(true); setError("");
    const r = edit
      ? await act(patchShare(share!.id, { type, address: address.trim(), name: name.trim() || address.trim().split(/[\\/]/).pop()!, guest, username: guest ? undefined : username, password: password || undefined }), setError)
      : await act(addShare({ type, address: address.trim(), name: name.trim() || address.trim().split(/[\\/]/).pop()!, guest: type === "nfs" ? true : guest, username: type === "nfs" ? undefined : (guest ? undefined : username), password: type === "nfs" ? undefined : (password || undefined) }), setError);
    setSaving(false);
    if (r) { if (r.setup && !edit) setSavedShare(r as Share); else { said(edit ? "Saved." : "Added."); onDone(r); } }
  };

  useEffect(() => { box.current?.focus(); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const ss = savedShare;
  return (
    <div className="drawer-wrap" onMouseDown={(e) => e.target === e.currentTarget && onCancel()}>
      <aside ref={box} className="drawer" tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby={edit ? "es" : "ns"} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), onCancel())}>
        <div className="hd"><h3 id={edit ? "es" : "ns"} style={{ margin: 0 }}>{edit ? share!.name : ss ? ss.name : "Add a share"}</h3>
          {edit && <p className="hint" style={{ margin: "4px 0 0" }}>{share!.address} · {TYPE[share!.type].label}</p>}
          <button className="btn icon ghost" type="button" aria-label="Close" onClick={onCancel}><Icon id="i-chev-l" /></button></div>
        <div className="bd">
          {ss?.setup ? <>
            <p className="st warn" style={{ margin: 0 }}><Icon id="i-clock" />Waiting for setup on the server</p>
            <h4 style={{ margin: 0 }}>Finish on the server</h4>
            <p style={{ margin: 0 }}>Run this once on this server, as root:</p>
            <div className="codebox"><code>{ss.setup.text}</code><button className="btn sm" type="button" onClick={() => navigator.clipboard?.writeText(ss.setup!.text).catch(() => {})}><Icon id="i-copy" />Copy</button></div>
            <p className="hint" style={{ margin: 0 }}>It connects the share now and after every restart.</p>
          </> : <>
            <fieldset style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}><legend style={{ fontWeight: "var(--fw-strong)", marginBottom: 6, padding: 0 }}>Type</legend>
              <label className={"radio" + (type === "smb" ? " sel" : "")}><input type="radio" name="st" checked={type === "smb"} onChange={() => setType("smb")} disabled={edit} />
                <span><strong>SMB</strong><span className="hint blk">{TYPE.smb.hint}</span></span></label>
              <label className={"radio" + (type === "nfs" ? " sel" : "")}><input type="radio" name="st" checked={type === "nfs"} onChange={() => setType("nfs")} disabled={!resp.nfsSupported || edit} />
                <span><strong>NFS</strong><span className="hint blk">{TYPE.nfs.hint}</span>{!resp.nfsSupported && <span className="hint blk">NFS isn't supported on Mac yet.</span>}</span></label>
            </fieldset>
            <div className="field"><label htmlFor="sa">Address</label>
              <input className="input mono" id="sa" value={address} onChange={(e) => setAddress(e.target.value)} placeholder={type === "smb" ? "\\\\nas\\media" : "nas:/volume1/media"} />
              <span className="hint">Also accepts {type === "smb" ? "smb://nas/media" : "nfs://nas/volume1/media"}. Saved as {type === "smb" ? "\\\\nas\\media" : "nas:/volume1/media"}</span></div>
            <div className="field"><label htmlFor="sn">Name</label>
              <input className="input" id="sn" value={name || address.split(/[\\/]/).pop() || ""} onChange={(e) => setName(e.target.value)} />
              <span className="hint">Shown in the folder browser and on Libraries.</span></div>
            {type === "smb" && <>
              <fieldset style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}><legend style={{ fontWeight: "var(--fw-strong)", marginBottom: 6, padding: 0 }}>Sign in</legend>
                <label className={"radio" + (!guest ? " sel" : "")}><input type="radio" name="si" checked={!guest} onChange={() => setGuest(false)} /><span><strong>With a username</strong></span></label>
                <label className={"radio" + (guest ? " sel" : "")}><input type="radio" name="si" checked={guest} onChange={() => setGuest(true)} /><span><strong>As a guest</strong></span></label>
              </fieldset>
              {!guest && <>
                <div className="field"><label htmlFor="su">Username</label>
                  <input className="input" id="su" value={username} onChange={(e) => setUsername(e.target.value)} /><span className="hint">For a domain account: DOMAIN\name</span></div>
                <div className="field"><label htmlFor="sp">Password</label>
                  <div className="pw"><input className="input" id="sp" type={showPw ? "text" : "password"} value={password} onChange={(e) => setPassword(e.target.value)} placeholder={edit ? "Leave empty to keep the saved password." : ""} />
                    <button className="btn sm" type="button" aria-pressed={showPw} onClick={() => setShowPw(!showPw)}>{showPw ? "Hide" : "Show"}</button></div>
                  <span className="hint">Stored encrypted in this app's data and never shown again. It keeps copies and backups of the data safe, not someone who can already sign in to this computer.</span></div>
              </>}
            </>}
            {type === "nfs" && <p className="hint" style={{ marginTop: 8 }}>NFS has no password. The server must allow this computer's address ({resp.serverAddress ?? "this server"}).</p>}
            {!resp.testedPlatform && <p className="hint" style={{ marginTop: 8 }}>Network shares are not tested on a Mac yet. If one doesn't connect, the share's status says why.</p>}
            {resp.mode === "manual" && <p className="hint" style={{ marginTop: 8 }}>To let the app connect shares itself, run the installer again and answer yes to network shares.</p>}
            <div className="row"><button className="btn sm" type="button" disabled={testing || saving} onClick={testNow}>{testing ? "Testing..." : "Test"}</button></div>
            {testResult && (testResult.ok
              ? <div className="note ok" role="status" aria-live="polite"><Icon id="i-check-c" /><span>{testResult.portOnly ? "The server answers on the NFS port. The folder itself is checked when the share connects." : "Connected. Folders at the top: " + testResult.folders!.slice(0, 3).join(", ") + (testResult.moreFolders ? ", and " + testResult.moreFolders + " more." : ".")}</span></div>
              : <div className="note bad" role="status" aria-live="polite"><Icon id="i-alert" /><span>{testResult.message + (testResult.shares?.length ? " The server offers: " + testResult.shares.join(", ") : "")}</span></div>)}
            {error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}
            <div className="row" style={{ gap: 8, marginTop: 8 }}><button className="btn primary" type="button" disabled={saving} onClick={save}>{saving ? "Saving..." : edit ? "Save" : "Save and connect"}</button>
              <button className="btn ghost" type="button" onClick={onCancel}>Cancel</button></div>
          </>}
        </div>
      </aside>
    </div>
  );
}

const is404 = (e: { code?: string }) => e.code === "http_404" || e.code === "not_found";

export function SharesSettings() {
  const resp = useData(getShares, []), [said, setSaid] = useState(""), [adding, setAdding] = useState(false);
  const data = resp.data;
  return (
    <div className="stack">
      <PageHead icon="i-shares" title="Settings" />
      <SettingsTabs at="shares" />
      <p className="muted" role="status">{said}</p>
      {resp.error && is404(resp.error) ? <p className="muted">This server doesn't have network shares yet.</p> : resp.error ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{resp.error.message}</span></p> : !data ? <div className="empty">Loading...</div> : <>
        <div className="row" style={{ gap: 6 }}>
          <p className="muted" style={{ margin: 0 }}>Use a folder on a NAS or another computer as a library. Shrink Your Shit connects to it and reconnects after a restart.</p>
          <span className="sp" />
          <button className="btn primary" type="button" onClick={() => setAdding(true)}><Icon id="i-plus" />Add a share</button>
        </div>
        {(data.items ?? []).length === 0
          ? <Empty icon="i-shares" text="No network shares yet. Libraries can still use any folder this computer can already see." />
          : <div className="list"><table className="tbl"><thead><tr>
            <th>Name</th><th data-p="2">Type</th><th className="stc">Status</th><th data-p="2">Used by</th><th><span className="sr">Actions</span></th>
          </tr></thead>
              <tbody>{(data.items ?? []).map((s) => <ShareRow key={s.id} share={s} resp={data} onChanged={resp.reload} said={setSaid} />)}</tbody>
            </table>
          </div>}
        {adding && <ShareSheet resp={data} onDone={() => { setAdding(false); resp.reload(); }} onCancel={() => setAdding(false)} said={setSaid} />}
      </>}
    </div>
  );
}