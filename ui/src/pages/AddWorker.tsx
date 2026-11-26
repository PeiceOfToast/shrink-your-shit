import { useEffect, useState } from "react";
import { navigate } from "../router.tsx";
import { api, type JoinRequest, type Worker } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { useKeys } from "../ui/useKeys.ts";
import { clock } from "../format/time.ts";
import { errorText } from "./workerParts.tsx";

// Add worker (page 10): shows the command to run on the other machine, then waits for its join request.
// A join request is answered with Allow or Deny; the worker appears only after Allow.
export function AddWorker({ onClose }: { onClose: () => void }) {
  const pair = useApi(() => api.createPairing(), []);
  const [request, setRequest] = useState<JoinRequest | null>(null);
  const [joined, setJoined] = useState<Worker | null>(null);
  const [denied, setDenied] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pairId = pair.data?.id;

  useEffect(() => {
    if (!pairId) return;
    let alive = true;
    api.waitForJoinRequest(pairId).then(
      (r) => alive && setRequest(r),
      (e: unknown) => alive && setErr(errorText(e)),
    );
    return () => {
      alive = false;
    };
  }, [pairId]);

  const decide = (allow: boolean) => {
    if (!request) return;
    setBusy(true);
    setErr(null);
    const done = allow ? api.allowJoinRequest(request.id).then(setJoined) : api.denyJoinRequest(request.id).then(() => setDenied(true));
    done.catch((e: unknown) => setErr(errorText(e))).finally(() => setBusy(false));
  };

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
              <p className="muted sm">The code works until {clock(pair.data.expiresAt)}.</p>
              {pair.data.plainHTTP && (
                <div className="note">
                  <span>This server is reached over plain HTTP, so the code travels unencrypted. Only join on a network you trust.</span>
                </div>
              )}

              {joined && (
                <div className="note">
                  <span>
                    <b>{joined.name} joined.</b>
                  </span>
                </div>
              )}
              {denied && <p className="muted">Denied.</p>}
              {!joined && !denied && request && (
                <div className="note">
                  <span>
                    <b>{request.name}</b> ({request.address}) asks to join.{" "}
                    <button type="button" className="btn primary sm" onClick={() => decide(true)} disabled={busy}>Allow</button>{" "}
                    <button type="button" className="btn ghost sm" onClick={() => decide(false)} disabled={busy}>Deny</button>
                  </span>
                </div>
              )}
              {!joined && !denied && !request && <p className="muted">Waiting for the other machine to ask to join…</p>}
              {err && <p className="err" role="alert">{err}</p>}
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
