import { useEffect, useState } from "react";
import { navigate } from "../router.tsx";
import { api, type Worker } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { useKeys } from "../ui/useKeys.ts";
import { errorText } from "./workerParts.tsx";

// Add worker (page 10): shows the command to run on the other machine, then waits for it to join.
export function AddWorker({ onClose }: { onClose: () => void }) {
  const pair = useApi(() => api.createPairing(), []);
  const [joined, setJoined] = useState<Worker | null>(null);
  const [waitErr, setWaitErr] = useState<string | null>(null);
  const pairId = pair.data?.id;

  useEffect(() => {
    if (!pairId) return;
    let alive = true;
    api.waitForJoin(pairId).then(
      (w) => alive && setJoined(w),
      (e: unknown) => alive && setWaitErr(errorText(e)),
    );
    return () => {
      alive = false;
    };
  }, [pairId]);

  useKeys((e) => {
    if (e.key === "Escape") onClose();
  });

  return (
    <>
      <div className="scrim" onClick={onClose} />
      <aside className="ov panel" role="dialog" aria-modal="true" aria-labelledby="add-worker-title">
        <div className="ovh">
          <div style={{ minWidth: 0 }}>
            <h2 id="add-worker-title">Add worker</h2>
            <div className="muted xs">Another computer or NAS that converts files for this server.</div>
          </div>
          <span className="sp"></span>
          <button type="button" className="btn ghost sm" onClick={onClose} aria-label="Close">Close</button>
        </div>

        <div className="ovb">
          {pair.error && !pair.data && <p className="bad">Can't create the command. <button type="button" className="btn ghost sm" onClick={pair.reload}>Try now</button></p>}
          {pair.loading && !pair.data && <span className="skel" style={{ height: 80 }} />}
          {pair.data && (
            <>
              <p>Run this on the other machine:</p>
              <pre className="cmd">{pair.data.command}</pre>
              {joined ? (
                <div className="note">
                  <span>
                    <b>{joined.name} joined.</b>
                  </span>
                </div>
              ) : (
                <p className="muted">Waiting for the other machine to join…</p>
              )}
              {waitErr && <p className="err" role="alert">{waitErr}</p>}
            </>
          )}
        </div>

        <div className="ovf">
          <span className="sp"></span>
          {joined && (
            <button type="button" className="btn primary sm" onClick={() => navigate(`/workers/${joined.id}`)}>
              Open its page
            </button>
          )}
        </div>
      </aside>
    </>
  );
}
