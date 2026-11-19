// Backups (contract "Backup"). The server routes aren't on dev yet, so ?mock= keeps an in-memory list.
// Mock files: a name with "bad" isn't a backup, "new" is from a newer app, "busy" finds conversions running on restore.
import { call, MOCK, type Answer } from "./api";
import type { Backup, BackupApplied, BackupList, BackupPreview } from "./types/backup";

const ago = (h: number) => new Date(Date.now() - h * 3600000).toISOString();
const fileName = (t: string) => "shrink-your-shit-backup-" + t.slice(0, 16).replace("T", "-").replace(":", "") + ".json.gz";
const mk = (id: string, h: number, reason: Backup["reason"], appVersion: string, sizeBytes: number): Backup => ({ id, name: fileName(ago(h)), createdAt: ago(h), reason, appVersion, sizeBytes });
let mock: Backup[] = new URLSearchParams(location.search).get("mock") === "fresh" ? [] : [
  mk("4", 3, "manual", "0.3.1", 412_000), mk("3", 26, "before_update", "0.3.0", 398_500),
  mk("2", 50, "before_migration", "0.3.0", 391_200), mk("1", 170, "before_import", "0.2.4", 120_800)];
const previews = new Map<string, { busy: boolean }>();
const wait = <T,>(v: T, ms = 300) => new Promise<T>((r) => setTimeout(() => r(v), ms));
const bad = (code: string, message: string) => ({ error: { code, message } }) as Answer<never>;

export async function listBackups(): Promise<Answer<BackupList>> {
  if (MOCK) return wait({ backups: mock.map((b) => ({ ...b })), keep: 10 });
  return call("GET", "/api/v2/backups");
}

export async function createBackup(): Promise<Answer<Backup>> {
  if (MOCK) { const b = mk(String(Date.now()), 0, "manual", "0.3.1", 415_000); mock.unshift(b); return wait({ ...b }, 800); }
  return call("POST", "/api/v2/backups");
}

export async function deleteBackup(id: string): Promise<Answer<null>> {
  if (MOCK) { mock = mock.filter((b) => b.id !== id); return wait(null); }
  return call("DELETE", "/api/v2/backups/" + encodeURIComponent(id));
}

// The browser downloads it with the sign-in cookie; the mock hands over a small sample file.
export const downloadUrl = (b: Backup) => MOCK
  ? "data:application/json," + encodeURIComponent(JSON.stringify({ sample: true, name: b.name }))
  : "/api/v2/backups/" + encodeURIComponent(b.id) + "/download";

const contents = { settings: true, workflows: 3, libraries: 2, shares: 1, connections: 4, jobs: 1204 };
export async function previewBackup(from: File | { backupId: string }): Promise<Answer<BackupPreview>> {
  if (MOCK) {
    const id = "p" + Date.now(), file = from instanceof File ? from : null;
    if (file && /bad/i.test(file.name)) return wait(bad("not_a_backup", "This file is not a shrink-your-shit backup."));
    if (file && /new/i.test(file.name)) return wait(bad("backup_too_new", "This backup is from version 0.4.0, newer than this app (0.3.1). Update the app, then import it."));
    previews.set(id, { busy: !!file && /busy/i.test(file.name) });
    const stored = file ? null : mock.find((b) => b.id === (from as { backupId: string }).backupId);
    return wait({ previewId: id, formatVersion: 1, appVersion: stored?.appVersion ?? "0.3.0", createdAt: stored?.createdAt ?? ago(30), contents,
      warnings: file ? ["Share passwords and API keys can't be read on this computer; type them again after the import.",
        "2 libraries point at folders this computer doesn't have: /mnt/old."] : [] }, 600);
  }
  if (from instanceof File) { const f = new FormData(); f.append("file", from); return call("POST", "/api/v2/backups/preview", f); }
  return call("POST", "/api/v2/backups/preview", from);
}

export async function applyBackup(previewId: string): Promise<Answer<BackupApplied>> {
  if (MOCK) {
    const p = previews.get(previewId);
    if (!p) return wait(bad("preview_expired", "This preview has expired. Choose the file again."));
    if (p.busy) return wait(bad("jobs_running", "Conversions are running. Pause the queue and wait for them to finish, then import. Nothing was changed."));
    const safety = mk(String(Date.now()), 0, "before_import", "0.3.1", 415_000); mock.unshift(safety); previews.delete(previewId);
    return wait({ restored: contents, safetyBackup: safety }, 900);
  }
  return call("POST", "/api/v2/backups/apply", { previewId });
}
