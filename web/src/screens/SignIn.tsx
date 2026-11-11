// Sign in (a 401 from the API comes here; api.ts). POST /api/login {token} sets the same cookie the current interface
// uses, so signing in on either is enough for both.
import { PageHead } from "../PageHead";
import { useState } from "react";
import { call, failed } from "../api";
import { navigate, useSearch } from "../router";

export function SignIn() {
  const next = new URLSearchParams(useSearch()).get("next") ?? "/";
  const [token, setToken] = useState(""), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  const go = async () => {
    setError(""); setBusy(true);
    const r = await call("POST", "/api/login", { token: token.trim() }).finally(() => setBusy(false));
    if (failed(r)) return setError(r.error.message);
    navigate(next.startsWith("/") && !next.startsWith("/signin") ? next : "/");
  };
  return (
    <div className="stack" style={{ maxWidth: 520 }}>
      <PageHead title="Sign in" />
      <p className="muted">This server asks for its access token.</p>
      <form className="card" onSubmit={(e) => (e.preventDefault(), go())}><div className="bd stack">
        <div className="field"><label htmlFor="token">Access token</label>
          <input id="token" className="input mono" type="password" autoComplete="current-password" value={token} onChange={(e) => setToken(e.target.value)} aria-describedby="token-hint" />
          <span id="token-hint" className="hint">It is in the sign-in link the server printed when it started.</span></div>
        {error && <p className="st bad" role="alert"><span>{error}</span></p>}
        <div className="row"><button className="btn primary" type="submit" disabled={busy || !token.trim()}>Sign in</button></div>
      </div></form>
    </div>
  );
}
