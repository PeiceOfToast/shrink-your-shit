// Settings > Backup (user item 5; contract "Backup"): back up now, the list of backups (yours and the automatic ones made before
// updates, database changes and restores) with Download, Restore and Delete, and restore from a file. A restore always shows
// what the backup holds and its warnings first; nothing changes until Restore. After it the page reloads.
import { useEffect, useRef, useState } from "react";
import { FileName } from "../FileName";
import { Icon } from "../Icon";
import { LoadFailed, PageHead } from "../PageHead";
import { failed } from "../api";
import { applyBackup, createBackup, deleteBackup, downloadUrl, listBackups, previewBackup } from "../backupApi";
import { bytes, clock, count, plural } from "../format";
import { SettingsTabs } from "../pages";
import { SettingRow } from "../SettingRow";
import { useData } from "../useData";
import type { Backup, BackupContents, BackupPreview } from "../types/backup";

const REASON: Record<Backup["reason"], string> = {
  manual: "Made by you", before_update: "Before an update", before_migration: "Before a database change", before_import: "Before a restore" };
const when = (t: string) => new Date(t).toLocaleDateString([], { day: "numeric", month: "short", year: "numeric" }) + ", " + clock(t);
const DONE = "syp-restored"; // the sentence to show after the reload that follows a restore

function holds(c: BackupContents): string {
  const parts = [c.settings && "settings", plural(c.workflows, "workflow", "workflows"), plural(c.libraries, "library", "libraries"),
    plural(c.shares, "network share", "network shares"), plural(c.connections, "connection", "connections"), plural(c.jobs, "job", "jobs") + " of history"].filter(Boolean) as string[];
  return parts.slice(0, -1).join(", ") + " and " + parts[parts.length - 1];
}

type Pending = { preview: BackupPreview; from: string };

function Preview({ p, onCancel }: { p: Pending; onCancel: () => void }) {
  const [busy, setBusy] = useState(false), [error, setError] = useState(""), head = useRef<HTMLHeadingElement>(null);
  useEffect(() => head.current?.focus(), []);
  const apply = async () => {
    if (busy) return;
    setBusy(true); setError("");
    const r = await applyBackup(p.preview.previewId).catch(() => null);
    setBusy(false);
    if (!r) return;
    if (failed(r)) { setError(r.error.message); return; }
    try { sessionStorage.setItem(DONE, `Restored from ${p.from}. How things were before is saved as ${r.safetyBackup.name}.`); } catch { /* the reload still happens */ }
    location.reload();
  };
  const { preview: v } = p;
  return (
    <section className="card" aria-labelledby="rs-h">
      <div className="hd"><h2 id="rs-h" ref={head} tabIndex={-1}>Restore {p.from}?</h2></div>
      <div className="bd stack">
        <p style={{ margin: 0 }}>Made {when(v.createdAt)} by version {v.appVersion}. It holds {holds(v.contents)}.</p>
        {v.warnings.map((w) => <p key={w} className="st warn" style={{ margin: 0 }}><Icon id="i-alert" /><span>{w}</span></p>)}
        <p className="muted" style={{ margin: 0 }}>Restoring replaces everything on this server with what's in the backup. Nothing is merged. A backup of how things are now is made first, so you can go back.</p>
        <div className="row" style={{ gap: 8 }}>
          <button className="btn danger" type="button" onClick={apply} aria-disabled={busy}>{busy ? "Restoring…" : "Restore"}</button>
          <button className="btn ghost" type="button" onClick={() => { if (!busy) onCancel(); }} aria-disabled={busy}>Cancel</button>
        </div>
        <div role="alert">{error && <p className="st bad" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}</div>
      </div>
    </section>);
}

function Row({ b, onRestore, reload, said }: { b: Backup; onRestore: (b: Backup) => void; reload: () => void; said: (m: string) => void }) {
  const [confirm, setConfirm] = useState(false), [msg, setMsg] = useState(""), [busy, setBusy] = useState(false);
  const del = async () => {
    if (busy) return;
    setBusy(true);
    let r;
    try { r = await deleteBackup(b.id); } finally { setBusy(false); }
    if (failed(r)) { setMsg(r.error.message); return; } // a deleted backup answers with an empty body (null): that is success
    said("Backup deleted."); reload();
  };
  return (
    <li className="stack" style={{ gap: 6, paddingBottom: "var(--gap)", borderBottom: "1px solid var(--border)" }}>
      <div className="row" style={{ gap: 8 }}>
        <strong>{when(b.createdAt)}</strong>
        <span className="muted">{REASON[b.reason]} · version {b.appVersion} · {bytes(b.sizeBytes)}</span>
      </div>
      <span className="mono muted"><FileName n={b.name} /></span>
      {confirm
        ? <div className="row" role="group" aria-label={"Delete backup " + b.name} style={{ gap: 8 }}>
          <span>Delete this backup? It can't be brought back.</span>
          <button className="btn sm danger" type="button" onClick={del} aria-disabled={busy}>{busy ? "Deleting…" : "Delete"}</button>
          <button className="btn sm ghost" type="button" onClick={() => { if (!busy) setConfirm(false); }} aria-disabled={busy}>Cancel</button>
          {msg && <span className="st bad"><Icon id="i-alert" />{msg}</span>}
        </div>
        : <div className="row" style={{ gap: 6 }}>
          <a className="btn sm" href={downloadUrl(b)} download={b.name} aria-label={"Download backup from " + when(b.createdAt)}>Download</a>
          <button className="btn sm" type="button" onClick={() => onRestore(b)} aria-label={"Restore backup from " + when(b.createdAt)}>Restore…</button>
          <button className="btn sm ghost" type="button" onClick={() => setConfirm(true)} aria-label={"Delete backup from " + when(b.createdAt)}>Delete</button>
        </div>}
    </li>);
}

export function BackupSettings() {
  const d = useData(listBackups, []), [said, setSaid] = useState(""), [busy, setBusy] = useState(""), [error, setError] = useState("");
  const [pending, setPending] = useState<Pending | null>(null), [fileError, setFileError] = useState(""), file = useRef<HTMLInputElement>(null);
  useEffect(() => { try { const m = sessionStorage.getItem(DONE); if (m) { sessionStorage.removeItem(DONE); setSaid(m); } } catch { /* nothing to show */ } }, []);
  const notHere = d.error && ["http_404", "http_501", "not_found"].includes(d.error.code);

  // the busy buttons stay focusable (aria-disabled, not disabled), so focus stays put while they work
  const backUp = async () => {
    if (busy) return;
    setBusy("make"); setError(""); setSaid("");
    const r = await createBackup().catch(() => null);
    setBusy("");
    if (!r) return;
    if (failed(r)) { setError(r.error.message); return; }
    setSaid("Backup made. Download it to keep a copy off this computer."); d.reload();
  };
  const preview = async (from: File | Backup) => {
    setBusy("preview"); setError(""); setFileError(""); setSaid(""); setPending(null);
    const r = await previewBackup(from instanceof File ? from : { backupId: from.id }).catch(() => null);
    setBusy("");
    if (file.current) file.current.value = "";
    if (!r) return;
    if (failed(r)) { (from instanceof File ? setFileError : setError)(r.error.message); return; }
    setPending({ preview: r, from: from instanceof File ? from.name : "the backup from " + when(from.createdAt) });
  };

  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="backup" />
      <p className="muted" style={{ margin: 0 }}>A backup holds your settings, workflows, libraries, network shares, connections and job history. Not your videos.</p>
      <p className="muted" role="status" style={{ margin: 0 }}>{said}{busy === "preview" && "Reading the backup…"}</p>
      <div role="alert">{error && <p className="st bad" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}</div>
      {pending && <Preview key={pending.preview.previewId} p={pending} onCancel={() => setPending(null)} />}
      {notHere ? <p className="muted">This server can't make backups yet.</p>
        : d.error && !d.data ? <LoadFailed error={d.error} reload={d.reload} />
          : !d.data ? <div className="empty">Loading…</div>
            : <>
              <section className="card" aria-labelledby="bk-h">
                <div className="hd"><h2 id="bk-h">Back up</h2><span className="sp" />
                  <button className="btn primary sm" type="button" onClick={backUp} aria-disabled={!!busy}>{busy === "make" ? "Backing up…" : "Back up now"}</button></div>
                <div className="bd stack">
                  <div className="note"><Icon id="i-shield" /><span>Share passwords and API keys are locked with this computer's key, which isn't in the backup. Restored on another computer, they need typing again.</span></div>
                  {d.data.backups.length === 0 ? <p className="muted" style={{ margin: 0 }}>No backups yet.</p>
                    : <ul className="stack" style={{ listStyle: "none", padding: 0, margin: 0 }} aria-label="Backups">
                      {d.data.backups.map((b) => <Row key={b.id} b={b} onRestore={preview} reload={d.reload} said={setSaid} />)}
                    </ul>}
                </div>
                <div className="bd stack" style={{ padding: 0, gap: 0 }}>
                  <SettingRow title="Automatic backups" desc={`A backup is made on its own before every update and database change. The last ${count(d.data.keep)} of those are kept; yours stay until you delete them.`} />
                </div>
              </section>
              <section className="card" aria-labelledby="rf-h">
                <div className="hd"><h2 id="rf-h">Restore</h2></div>
                <div className="bd stack" style={{ padding: 0, gap: 0 }}>
                  <SettingRow title="Restore from a file" desc="Choose a backup file from this or another computer. You'll see what it holds before anything changes.">
                    <button className="btn" type="button" onClick={() => { if (!busy) file.current?.click(); }} aria-disabled={!!busy}>Choose a backup file…</button>
                    <input ref={file} type="file" accept=".gz,.json,application/gzip,application/json" hidden
                      onChange={(e) => { const f = e.target.files?.[0]; if (f) preview(f); }} />
                  </SettingRow>
                  <div role="alert" style={{ padding: "0 var(--pad) 12px" }}>{fileError && <p className="st bad" style={{ margin: 0 }}><Icon id="i-alert" /><span>{fileError}</span></p>}</div>
                </div>
              </section>
            </>}
    </div>);
}
