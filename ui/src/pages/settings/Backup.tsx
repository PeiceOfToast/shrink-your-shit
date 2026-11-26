import { useRef, useState } from "react";
import { api, type BackupItem, type BackupPreview } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { LoadState, Row, SaveBar, errText, useDirty, useDraft } from "./parts.tsx";

const REASON: Record<BackupItem["reason"], string> = {
  manual: "Made by hand",
  before_update: "Before an update",
  before_migration: "Before a migration",
  before_import: "Before an import",
};
const size = (n: number) => (n >= 1e9 ? `${(n / 1e9).toFixed(1)} GB` : n >= 1e6 ? `${Math.round(n / 1e6)} MB` : `${Math.max(1, Math.round(n / 1e3))} KB`);
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const dayMonth = (iso: string) => new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "short" });

export function Backup() {
  const s = useApi(() => api.getBackups(), []);
  const keep = useDraft(s.data?.keep);
  useDirty("backup", keep.dirty);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [text, setText] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [preview, setPreview] = useState<BackupPreview | null>(null);
  const [restored, setRestored] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const saveKeep = () => {
    if (keep.draft === undefined) return;
    setSaving(true);
    setSaved(false);
    setText(null);
    api.patchSettings({ backupKeep: keep.draft }).then(
      () => {
        setSaved(true);
        s.reload();
      },
      (e: unknown) => setText(errText(e)),
    ).finally(() => setSaving(false));
  };
  const run = (p: Promise<unknown>, after?: () => void) => {
    setBusy(true);
    setErr(null);
    p.then(() => after?.(), (e: unknown) => setErr(errText(e))).finally(() => setBusy(false));
  };
  const download = (id: string, name: string) => {
    api.downloadBackup(id).then(
      (blob) => {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = name;
        a.click();
        URL.revokeObjectURL(url);
      },
      (e: unknown) => setErr(errText(e)),
    );
  };
  const pickBackup = (id: string) => run(api.previewBackup({ backupId: id }).then(setPreview));
  const pickFile = (f: File | undefined) => {
    if (f) run(api.previewBackup({ file: f }).then(setPreview));
  };
  const apply = () => {
    if (!preview) return;
    run(api.applyBackup(preview.previewId).then(() => {
      setPreview(null);
      setRestored(true);
      s.reload();
    }));
  };

  if (!s.data) return <><h2 className="set-title">Backup</h2><LoadState s={s} /></>;
  const { backups } = s.data;
  return (
    <>
      <h2 className="set-title">Backup</h2>
      <Row id="b-keep" label="Backups kept" hint="The oldest backups are removed past this number." field="backupKeep" problem={null}>
        <input id="b-keep" type="number" className="inp narrow" min={1} max={50} value={keep.draft} onChange={(e) => keep.setDraft(e.target.valueAsNumber)} />
      </Row>
      <SaveBar dirty={keep.dirty} busy={saving} saved={saved} text={text} onSave={saveKeep} onUndo={keep.undo} />

      <section className="set-group">
        <div className="set-grouphead">
          <b>Backups</b>
          <span className="sp" />
          <button type="button" className="btn sm" onClick={() => run(api.createBackup(), s.reload)} disabled={busy}>Back up now</button>
        </div>
        {backups.length === 0 && <p className="muted sm">No backups yet.</p>}
        {backups.map((b) => (
          <div className="row" key={b.id}>
            <div style={{ minWidth: 0, flex: 1 }}>
              <div>{b.name}</div>
              <div className="muted sm">{dayMonth(b.createdAt)} · {REASON[b.reason]} · {b.appVersion} · {size(b.sizeBytes)}</div>
            </div>
            <button type="button" className="btn ghost sm" onClick={() => download(b.id, b.name)}>Download</button>
            <button type="button" className="btn ghost sm" onClick={() => pickBackup(b.id)} disabled={busy}>Restore</button>
            <button type="button" className="btn ghost sm" onClick={() => run(api.deleteBackup(b.id), s.reload)} disabled={busy}>Delete</button>
          </div>
        ))}
        <div className="row">
          <button type="button" className="btn ghost sm" onClick={() => fileRef.current?.click()} disabled={busy}>Restore from a file…</button>
          <input ref={fileRef} type="file" accept=".zip,.json,application/zip" hidden onChange={(e) => pickFile(e.target.files?.[0])} aria-label="Backup file" />
        </div>
      </section>

      {err && <p className="bad" role="alert">{err}</p>}
      {restored && <p className="muted sm" role="status">Restored. Settings and workflows are back to that backup.</p>}

      {preview && (
        <div className="dlg" role="dialog" aria-modal="true" aria-labelledby="restore-q">
          <h2 id="restore-q">Replace all settings and workflows with this backup from {dayMonth(preview.createdAt)}?</h2>
          <p className="muted sm">
            {plural(preview.contents.workflows, "workflow", "workflows")}, {plural(preview.contents.libraries, "library", "libraries")}, {plural(preview.contents.shares, "share", "shares")}, {plural(preview.contents.connections, "connection", "connections")}. Jobs and files aren't touched.
          </p>
          {preview.warnings.map((w) => <p className="bad sm" key={w}>{w}</p>)}
          <div className="row dlg-actions">
            <button type="button" className="btn ghost sm" onClick={() => setPreview(null)} disabled={busy}>Cancel</button>
            <button type="button" className="btn primary sm" onClick={apply} disabled={busy}>Restore</button>
          </div>
        </div>
      )}
    </>
  );
}
