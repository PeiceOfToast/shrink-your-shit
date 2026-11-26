import { useState } from "react";
import { api } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { errText } from "./parts.tsx";

// One sign-in and one access token. No people list, no token list.
export function Access() {
  const tok = useApi(() => api.getToken().then((r) => r.token).catch((e: unknown) => {
    if ((e as { code?: string }).code === "no_token") return null;
    throw e;
  }), []);
  const [pw, setPw] = useState("");
  const [pwSaved, setPwSaved] = useState(false);
  const [pwErr, setPwErr] = useState<string | null>(null);
  const [shown, setShown] = useState(false);
  const [copied, setCopied] = useState(false);
  const [tokErr, setTokErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const token = tok.data ?? null;

  const changePw = () => {
    setPwErr(null);
    setPwSaved(false);
    api.changePassword(pw).then(
      () => {
        setPw("");
        setPwSaved(true);
      },
      (e: unknown) => setPwErr(errText(e)),
    );
  };
  const rotate = () => {
    setBusy(true);
    setTokErr(null);
    api.rotateToken().then(
      () => {
        tok.reload();
        setShown(true);
        setCopied(false);
      },
      (e: unknown) => setTokErr(errText(e)),
    ).finally(() => setBusy(false));
  };
  const copy = () => {
    if (!token) return;
    if (!navigator.clipboard) return setTokErr("Can't copy here. Select the token and copy it.");
    navigator.clipboard.writeText(token).then(() => setCopied(true), () => setTokErr("Can't copy here. Select the token and copy it."));
  };

  return (
    <>
      <h2 className="set-title">Access</h2>
      <section className="set-group">
        <div className="set-grouphead"><b>Password</b></div>
        <div className="set-row">
          <div className="set-lab"><label htmlFor="a-pw">New password</label><div className="hint">At least 8 characters.</div></div>
          <div className="set-ctl">
            <input id="a-pw" className="inp" type="password" autoComplete="new-password" value={pw} onChange={(e) => { setPw(e.target.value); setPwSaved(false); }} />
            {pwErr && <p className="bad set-msg" role="alert">{pwErr}</p>}
            {pwSaved && <p className="muted sm set-saved" role="status">Password changed.</p>}
          </div>
        </div>
        <div className="row"><button type="button" className="btn primary sm" onClick={changePw} disabled={pw === ""}>Change password</button></div>
      </section>

      <section className="set-group">
        <div className="set-grouphead"><b>Access token</b></div>
        {tok.loading && tok.data === undefined && <span className="skel" style={{ height: 40 }} aria-busy="true" />}
        {tok.error && !tok.loading && <p className="bad">Can't load the token. <button type="button" className="btn ghost sm" onClick={tok.reload}>Try now</button></p>}
        {!tok.loading && !tok.error && token === null && (
          <div className="row">
            <span className="muted">No token yet.</span>
            <button type="button" className="btn primary sm" onClick={rotate} disabled={busy}>Make a token</button>
          </div>
        )}
        {token && (
          <>
            <div className="row">
              <code className="mono sm" style={{ overflowWrap: "anywhere", flex: 1, minWidth: 0 }}>{shown ? token : "•".repeat(24)}</code>
              <button type="button" className="btn ghost sm" onClick={() => setShown(!shown)} aria-pressed={shown}>{shown ? "Hide" : "Show"}</button>
              <button type="button" className="btn ghost sm" onClick={copy}>{copied ? "Copied" : "Copy"}</button>
            </div>
            <div className="row">
              <span className="muted sm">Rotating makes the old token stop working at once.</span>
              <span className="sp" />
              <button type="button" className="btn ghost sm" onClick={rotate} disabled={busy}>Rotate token</button>
            </div>
          </>
        )}
        {tokErr && <p className="bad" role="alert">{tokErr}</p>}
      </section>
    </>
  );
}
