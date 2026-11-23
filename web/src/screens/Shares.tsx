// D: Network shares settings (SCREENS V9d, D-API-SHAPE.md). Mock only (the engine is after item E).
// The share list table, Add/Edit sheet (drawer), Remove dialog, Test button, and the connect-mode-specific UI.
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Icon } from "../Icon";
import { failed, type Answer } from "../api";
import { Empty, LoadFailed, PageHead } from "../PageHead";
import { SettingsTabs } from "../pages";
import { SettingRow, ValueChip } from "../SettingRow";
import { useData } from "../useData";
import { addShare, checkShare, deleteShare, getShares, patchShare, reconnectShare, testShare } from "../sharesApi";
import { shareIntro } from "../shareIntro";
import type { Share, ShareTestResult, SharesResponse, ShareType } from "../types/shares";

async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (_) { return null; }
}

const TYPE: Record<ShareType, { label: string; hint: string }> = {
  smb: { label: "SMB", hint: "Windows shares and most NAS boxes (Synology, QNAP, TrueNAS, Unraid)." },
  nfs: { label: "NFS", hint: "Linux servers and NAS boxes set up for NFS." },
};

// One share as a row of the Shares card: name, path and type on the left, status and the ⋯ menu on the right. Why it isn't
// connected, and the commands that finish its setup, sit full width under the row, so the commands can be copied whole.
function ShareRow({ share, resp, first, onChanged, said }: { share: Share; resp: SharesResponse; first: boolean; onChanged: () => void; said: (m: string) => void }) {
  const [removing, setRemoving] = useState(false), [editing, setEditing] = useState(false);
  const libs = share.libraries.map((l) => l.name).join(", ");
  const statusClass = share.status === "connected" ? "ok" : share.status === "not_connected" ? "bad" : share.status === "waiting_setup" ? "warn" : "";
  const statusIcon = share.status === "connected" ? "i-check-c" : share.status === "connecting" || share.status === "waiting_setup" ? "i-clock" : "i-alert";
  const statusWord = share.status === "connected" ? "Connected" : share.status === "connecting" ? "Connecting…" : share.status === "waiting_setup" ? "Waiting for setup on the server" : "Not connected";
  const canReconnect = share.status !== "connected" && resp.mode === "app";
  const canCheck = share.status !== "connected" && (resp.mode === "manual" || resp.mode === "docker");
  const reconnect = async () => { const r = await act(canReconnect ? reconnectShare(share.id) : checkShare(share.id), said); if (r) { said("Done."); onChanged(); } };
  const closeMenu = (e: React.MouseEvent) => { (e.currentTarget.closest("details") as HTMLDetailsElement | null)?.removeAttribute("open"); };
  const waiting = share.status === "waiting_setup" && !!share.setup;

  return (
    <div style={first ? undefined : { borderTop: "1px solid var(--border)" }}>
      <SettingRow
        title={<button className="ttlbtn" type="button" onClick={() => setEditing(true)}>{share.name}</button>}
        desc={<>
          <span style={{ display: "inline-flex", flexWrap: "wrap", gap: 6 }}><ValueChip><span className="mono" style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{share.address}</span></ValueChip><ValueChip>{TYPE[share.type].label}</ValueChip></span>
          <span style={{ display: "block" }}>{libs ? "Used by " + libs : "Not used"}</span></>}>
        <span className={"st " + statusClass}><Icon id={statusIcon} /><span>{statusWord}{share.since ? " since " + new Date(share.since).toLocaleDateString([], { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" }) : ""}</span></span>
        <details className="menu">
          <summary className="btn icon ghost sm" aria-label={"Actions for " + share.name}><Icon id="i-more" /></summary>
          <div className="pop" role="menu">
            {canReconnect && <button className="it" type="button" onClick={(e) => { closeMenu(e); reconnect(); }}>Reconnect</button>}
            {canCheck && <button className="it" type="button" onClick={(e) => { closeMenu(e); reconnect(); }}>Check again</button>}
            <button className="it" type="button" onClick={(e) => { closeMenu(e); setEditing(true); }}>Edit</button>
            <button className="it" type="button" onClick={(e) => { closeMenu(e); setRemoving(true); }}>Remove…</button>
          </div>
        </details>
      </SettingRow>
      {(share.reason || waiting || canReconnect || canCheck) && <div className="stack" style={{ padding: "0 var(--pad) 12px" }}>
        {share.reason && <p className="muted" style={{ margin: 0 }}>{share.reason.message}</p>}
        {waiting && resp.modeReason === "unprivileged_container" && <p className="hint" style={{ margin: 0 }}>Shrink Your Shit runs in an unprivileged container here, so it can't connect network shares itself. Mount each share on the host with the commands below, or run it on a full computer or VM, or in a privileged Proxmox container with <code>mount=nfs;cifs</code>.</p>}
        {waiting && <div className="codebox"><code>{share.setup!.text}</code></div>}
        {(waiting || canReconnect || canCheck) && <div className="row" style={{ gap: 8 }}>
          {waiting && <button className="btn sm" type="button" onClick={() => navigator.clipboard?.writeText(share.setup!.text).catch(() => {})}><Icon id="i-copy" />Copy</button>}
          {canReconnect && <button className="btn sm" type="button" onClick={() => reconnect()}><Icon id="i-refresh" />Reconnect</button>}
          {canCheck && <button className="btn sm" type="button" onClick={() => reconnect()}><Icon id="i-refresh" />Check again</button>}
        </div>}
      </div>}
      {removing && createPortal(<RemoveDialog share={share} onCancel={() => setRemoving(false)} onDone={() => { setRemoving(false); onChanged(); }} said={said} />, document.body)}
      {editing && createPortal(<ShareSheet share={share} resp={resp} onDone={() => { setEditing(false); onChanged(); }} onCancel={() => setEditing(false)} said={said} />, document.body)}
    </div>
  );
}

// Focus moves into the drawer; when it closes, focus goes back to what opened it (a menu item's ··· button), as V9d Keyboard says.
function useDrawerFocus(box: { current: HTMLElement | null }) {
  useEffect(() => {
    const a = document.activeElement as HTMLElement | null;
    const from = a?.closest("details")?.querySelector<HTMLElement>("summary") ?? a;
    box.current?.focus();
    return () => { if (from?.isConnected) from.focus(); };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
}

function RemoveDialog({ share, onCancel, onDone, said }: { share: Share; onCancel: () => void; onDone: () => void; said: (m: string) => void }) {
  const [error, setError] = useState("");
  const box = useRef<HTMLElement>(null);
  useDrawerFocus(box);
  const remove = async () => {
    // The server answers 204 with no body, so success is any answer that isn't an error (act can't tell null apart).
    let r; try { r = await deleteShare(share.id); } catch { return; }
    if (failed(r)) setError(r.error.message); else { said("Removed."); onDone(); }
  };
  return (
    <div className="drawer-wrap" onMouseDown={(e) => e.target === e.currentTarget && onCancel()}>
      <aside ref={box} className="drawer" tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="rm-s" onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), onCancel())}>
        <div className="hd"><button className="btn icon ghost sm" type="button" aria-label="Close" onClick={onCancel}><Icon id="i-chev-l" /></button><div className="sp"><h3 id="rm-s" style={{ margin: 0 }}>Remove {share.name}?</h3></div></div>
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
  // Wording per mode (design SCREENS.md V9d): Docker needs no root on the server; an unprivileged LXC mounts on the Proxmox host.
  const [intro, after] = shareIntro(resp.mode, resp.modeReason);
  const block = (setup: Share["setup"]) => <><p className="st warn" style={{ margin: 0 }}><Icon id="i-clock" />Waiting for setup on the server</p><h4 style={{ margin: 0 }}>Finish on the server</h4>
    <p style={{ margin: 0 }}>{intro}</p>
    <div className="codebox"><code>{setup!.text}</code><button className="btn sm" type="button" onClick={() => navigator.clipboard?.writeText(setup!.text).catch(() => {})}><Icon id="i-copy" />Copy</button></div>
    <p className="hint" style={{ margin: 0 }}>{after}</p></>;
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

  useDrawerFocus(box);
  const ss = savedShare;
  const close = () => { if (ss) { said("Added."); onDone(); } else onCancel(); };
  return (
    <div className="drawer-wrap" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <aside ref={box} className="drawer" tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby={edit ? "es" : "ns"} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), close())}>
        <div className="hd"><button className="btn icon ghost" type="button" aria-label="Close" onClick={close}><Icon id="i-chev-l" /></button><div className="sp"><h3 id={edit ? "es" : "ns"} style={{ margin: 0 }}>{edit ? share!.name : ss ? ss.name : "Add a share"}</h3>
          {edit && <p className="hint" style={{ margin: 0 }}>{share!.address} · {TYPE[share!.type].label}</p>}</div></div>
        <div className="bd">
          {ss?.setup ? block(ss.setup) : <>{edit && share?.setup && share.status === "waiting_setup" && block(share.setup)}
            <fieldset style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}><legend style={{ fontWeight: "var(--fw-strong)", marginBottom: 6, padding: 0 }}>Type</legend>
              <label className={"radio" + (type === "smb" ? " sel" : "")}><input type="radio" name="st" checked={type === "smb"} onChange={() => setType("smb")} disabled={edit} />
                <span><strong>SMB</strong><span className="hint" style={{ display: "block" }}>{TYPE.smb.hint}</span></span></label>
              <label className={"radio" + (type === "nfs" ? " sel" : "")}><input type="radio" name="st" checked={type === "nfs"} onChange={() => setType("nfs")} disabled={!resp.nfsSupported || edit} />
                <span><strong>NFS</strong><span className="hint" style={{ display: "block" }}>{TYPE.nfs.hint}</span>{!resp.nfsSupported && <span className="hint" style={{ display: "block" }}>NFS isn't supported on Mac yet.</span>}</span></label>
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
            {resp.modeReason === "unprivileged_container" ? <p className="hint" style={{ marginTop: 8 }}>Shrink Your Shit runs in an unprivileged container here, so it can't connect network shares itself. Save the share, and this page then shows the commands to mount it on the host.</p> : resp.mode === "manual" && <p className="hint" style={{ marginTop: 8 }}>To let the app connect shares itself, run the installer again and answer yes to network shares.</p>}
            <div className="row"><button className="btn sm" type="button" disabled={testing || saving} onClick={testNow}>{testing ? "Testing…" : "Test"}</button></div>
            {testResult && (testResult.ok
              ? <div className="note ok" role="status" aria-live="polite"><Icon id="i-check-c" /><span>{testResult.portOnly ? "The server answers on the NFS port. The folder itself is checked when the share connects." : (testResult.folders?.length ? "Connected. Folders at the top: " + testResult.folders.slice(0, 3).join(", ") + (testResult.moreFolders ? ", and " + testResult.moreFolders + " more." : ".") : "Connected. The share has no folders yet.")}</span></div>
              : <div className="note bad" role="status" aria-live="polite"><Icon id="i-alert" /><span>{testResult.message + (testResult.shares?.length ? " The server offers: " + testResult.shares.join(", ") : "")}</span></div>)}
            {error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}
          </>}
          <div className="row" style={{ gap: 8, marginTop: 8 }}>{!ss && <button className="btn primary" type="button" disabled={saving} onClick={save}>{saving ? "Saving…" : edit ? "Save" : "Save and connect"}</button>}
            <button className="btn ghost" type="button" onClick={close}>{ss ? "Close" : "Cancel"}</button></div>
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
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="shares" />
      <p className="muted" role="status">{said}</p>
      {resp.error && is404(resp.error) ? <p className="muted">This server doesn't have network shares yet.</p> : resp.error && !data ? <LoadFailed error={resp.error} reload={resp.reload} /> : resp.error ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{resp.error.message}</span></p> : !data ? <div className="empty">Loading…</div> : <>
        <section className="card" aria-labelledby="h-shares">
          <div className="hd"><h2 id="h-shares">Shares</h2><span className="sp" />
            <button className="btn primary sm" type="button" onClick={() => setAdding(true)}><Icon id="i-plus" />Add a share</button></div>
          <div className="bd" style={{ padding: 0 }}>
        <p className="muted" style={{ margin: 0, padding: "12px var(--pad)", borderBottom: (data.items ?? []).length ? "1px solid var(--border)" : undefined }}>Use a folder on a NAS or another computer as a library. Shrink Your Shit connects to it and reconnects after a restart.</p>
        {(data.items ?? []).length === 0
          ? <div style={{ padding: "0 var(--pad) 12px" }}><Empty icon="i-shares" text="No network shares yet. Libraries can still use any folder this computer can already see." /></div>
          : (data.items ?? []).map((s, i) => <ShareRow key={s.id} share={s} resp={data} first={i === 0} onChanged={resp.reload} said={setSaid} />)}
          </div>
        </section>
        {adding && <ShareSheet resp={data} onDone={() => { setAdding(false); resp.reload(); }} onCancel={() => setAdding(false)} said={setSaid} />}
      </>}
    </div>
  );
}