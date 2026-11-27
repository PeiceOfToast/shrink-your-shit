import { useState } from "react";
import { api, type TrashItem } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { LoadState, errText } from "./parts.tsx";

const dayMonth = (iso: string) => new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "short" });

// Settings > Trash (page 11): the originals kept after a conversion, newest first. Restore asks first, then puts the
// original back and removes the converted file. A refusal is one sentence in that file's row.
export function Trash() {
  const list = useApi(() => api.getTrash(), []);
  const [asking, setAsking] = useState<TrashItem | null>(null);
  const [busy, setBusy] = useState(false);
  const [refused, setRefused] = useState<Record<string, { code: string; message: string }>>({});
  const [done, setDone] = useState<string | null>(null);
  if (!list.data) return <LoadState s={list} />;
  const items = list.data;

  const restore = async (item: TrashItem) => {
    setBusy(true);
    setDone(null);
    try {
      await api.restoreTrash(item.id);
      setAsking(null);
      setRefused((r) => {
        const next = { ...r };
        delete next[item.id];
        return next;
      });
      setDone(`Put back the original of ${item.file}.`);
      list.reload();
    } catch (e) {
      setAsking(null);
      const code = typeof (e as { code?: unknown })?.code === "string" ? (e as { code: string }).code : "";
      setRefused((r) => ({ ...r, [item.id]: { code, message: errText(e) } }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h2 className="set-title">Trash</h2>
      <p className="hint">The originals kept after a conversion, newest first. How long they're kept is set in Processing.</p>
      {done && <p className="ok sm" role="status">{done}</p>}
      {items.length === 0 && <p className="muted">Nothing in the trash.</p>}
      <ul className="trash-list" aria-label="Trash">
        {items.map((item) => (
          <li key={item.id} className="trash-row">
            <div className="trash-main">
              <b>{item.file}</b>
              <span className="muted sm">
                {item.library} · {dayMonth(item.trashedAt)} · {item.daysLeft} {item.daysLeft === 1 ? "day" : "days"} left
              </span>
              {refused[item.id] && <p className="bad sm" role="alert">{refused[item.id].message}</p>}
            </div>
            {!(refused[item.id] && !api.restoreCanRetry(refused[item.id].code)) && (
              <button type="button" className="btn sm" disabled={busy} onClick={() => setAsking(item)}>
                Restore
              </button>
            )}
          </li>
        ))}
      </ul>
      {asking && (
        <div className="dlg" role="dialog" aria-modal="true" aria-labelledby="trash-q">
          <h2 id="trash-q">Put back the original of {asking.file}? The converted file is removed.</h2>
          <div className="row dlg-actions">
            <button type="button" className="btn primary" disabled={busy} onClick={() => restore(asking)}>
              Restore
            </button>
            <button type="button" className="btn" disabled={busy} onClick={() => setAsking(null)}>
              Cancel
            </button>
          </div>
        </div>
      )}
    </>
  );
}
