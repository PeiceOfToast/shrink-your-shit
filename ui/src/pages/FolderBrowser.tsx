import { useState } from "react";
import { api } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";

// Browses folders on this server (GET /api/v2/folders). Picking a folder means "use this one".
export function FolderBrowser({ start, onPick }: { start?: string; onPick: (path: string) => void }) {
  const [cur, setCur] = useState(start || "/media");
  const dir = useApi(() => api.getFolders(cur), [cur]);

  return (
    <div className="field">
      <div className="row">
        <span className="mono sm" style={{ flex: 1, minWidth: 0, overflowWrap: "anywhere" }}>{dir.data?.path ?? cur}</span>
        {dir.data?.parent && (
          <button type="button" className="btn ghost sm" onClick={() => setCur(dir.data!.parent!)}>
            Up
          </button>
        )}
        <button type="button" className="btn primary sm" onClick={() => onPick(dir.data?.path ?? cur)}>
          Use this folder
        </button>
      </div>
      <div className="tree" role="listbox" aria-label="Folders on this server">
        {dir.error && <p className="err" style={{ padding: 10 }}>{dir.error}</p>}
        {dir.data?.folders.map((f) => (
          <button key={f.path} type="button" onClick={() => setCur(f.path)}>
            {f.name}
          </button>
        ))}
        {dir.data && dir.data.folders.length === 0 && <p className="muted" style={{ padding: 10 }}>No folders inside.</p>}
      </div>
    </div>
  );
}
