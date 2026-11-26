import { useState } from "react";
import { clock } from "../format/time.ts";
import { api, type Worker, type WorkerState } from "../api/index.ts";
import { useKeys } from "../ui/useKeys.ts";

export const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));

const STATE: Record<WorkerState, { cls: string; label: (w: Worker) => string }> = {
  converting: { cls: "run", label: () => "Converting" },
  idle: { cls: "", label: () => "Idle" },
  paused: { cls: "warn", label: () => "Paused" },
  offline: { cls: "bad", label: (w) => (w.since ? `Offline since ${clock(w.since)}` : "Offline") },
};

export function StatePill({ w }: { w: Worker }) {
  const s = STATE[w.state];
  return <span className={`pill ${s.cls}`}>{s.label(w)}</span>;
}

// Pause asks whether the files it is on finish first (page 10, Actions).
export function PauseDialog({ w, onDone, onClose }: { w: Worker; onDone: () => void; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useKeys((e) => {
    if (e.key === "Escape" && !busy) onClose();
  });
  const pause = (finish: boolean) => {
    setBusy(true);
    setErr(null);
    api.pauseWorker(w.id, finish).then(onDone, (e: unknown) => {
      setErr(errorText(e));
      setBusy(false);
    });
  };
  return (
    <>
      <div className="scrim" onClick={busy ? undefined : onClose} />
      <div className="dlg" role="dialog" aria-modal="true" aria-labelledby="pause-title">
        <h2 id="pause-title">Finish the files it's on first?</h2>
        {err && <p className="err" role="alert">{err}</p>}
        <div className="row dlg-actions">
          <button type="button" className="btn sm" onClick={onClose} disabled={busy}>Cancel</button>
          <button type="button" className="btn sm" onClick={() => pause(false)} disabled={busy}>Stop now</button>
          <button type="button" className="btn primary sm" onClick={() => pause(true)} disabled={busy}>Finish them</button>
        </div>
      </div>
    </>
  );
}

// Remove is offered only when the worker is offline (page 10, Actions).
export function RemoveDialog({ w, onDone, onClose }: { w: Worker; onDone: () => void; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useKeys((e) => {
    if (e.key === "Escape" && !busy) onClose();
  });
  const remove = () => {
    setBusy(true);
    setErr(null);
    api.removeWorker(w.id).then(onDone, (e: unknown) => {
      setErr(errorText(e));
      setBusy(false);
    });
  };
  return (
    <>
      <div className="scrim" onClick={busy ? undefined : onClose} />
      <div className="dlg" role="dialog" aria-modal="true" aria-labelledby="remove-title">
        <h2 id="remove-title">Remove {w.name}? It can join again later.</h2>
        {err && <p className="err" role="alert">{err}</p>}
        <div className="row dlg-actions">
          <button type="button" className="btn sm" onClick={onClose} disabled={busy}>Cancel</button>
          <button type="button" className="btn bad sm" onClick={remove} disabled={busy}>Remove</button>
        </div>
      </div>
    </>
  );
}
