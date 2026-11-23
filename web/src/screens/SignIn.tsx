// Sign in (a 401 from the API comes here; api.ts). POST /api/login {token} sets the same cookie the current interface
// uses, so signing in on either is enough for both.
import { Icon } from "../Icon";
import { PageHead } from "../PageHead";
import { useEffect, useState } from "react";
import { call, failed } from "../api";
import { navigate, useSearch } from "../router";

export const fromLink = { error: "" }; // a sign-in link with a wrong token (main.tsx): its message, shown once

export function SignIn() {
  const next = new URLSearchParams(useSearch()).get("next") ?? "/";
  const [token, setToken] = useState(""), [error, setError] = useState(fromLink.error), [busy, setBusy] = useState(false), [shown, setShown] = useState(false);
  useEffect(() => { fromLink.error = ""; }, []); // shown once
  const go = async () => {
    setError(""); setBusy(true);
    const r = await call("POST", "/api/login", { token: token.trim() }).finally(() => setBusy(false));
    if (failed(r)) return setError(r.error.message);
    // only a path on this site: browsers read "//host", "/\\host" and "/<tab>/host" as another site
    navigate(/^\/(?![\/\\])/.test(next) && !/[\x00-\x1f\\]/.test(next) && !next.startsWith("/signin") ? next : "/");
  };
  return (
    <div className="signin">
      <PageHead title="Sign in" sub="Paste the access token for this Shrink Your Shit server." />
      <form className="stack" onSubmit={(e) => (e.preventDefault(), go())}>
        <div className="field"><label htmlFor="token">Access token</label>
          {/* parity 11-19 #3: Show/Hide in the field, and where to find the token (README "Signing in") */}
          <div className="pw"><input id="token" className="input mono" type={shown ? "text" : "password"} autoComplete="current-password" spellCheck={false} value={token} aria-invalid={error ? "true" : undefined} aria-describedby="token-hint" onChange={(e) => (setToken(e.target.value), setError(""))} />
            <button className="btn sm icon ghost" type="button" aria-controls="token" aria-pressed={shown} aria-label="Show the token" title="Show the token" onClick={() => setShown(!shown)}><Icon id="i-eye" /></button></div>
          {error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
          <span id="token-hint" className="hint">It is in the sign-in link the server printed when it started.</span>
        </div>
        <div className="row"><button className="btn primary wide" type="submit" disabled={busy || !token.trim()}>Sign in</button></div>
        <details className="where" open><summary>Where do I find it?</summary>
          <p className="hint">On the computer that runs shrink-your-shit:</p>
          <dl className="hint">
              <dt>Docker</dt><dd><code>docker exec &lt;container name&gt; shrink-your-shit token</code></dd>
              <dt>Linux</dt><dd><code>sudo shrink-your-shit token</code></dd>
              <dt>macOS</dt><dd><code>shrink-your-shit token</code></dd>
              <dt>Windows</dt><dd>Open shrink-your-shit from the Start menu, then Settings, Access token, Show.</dd>
            </dl></details>
      </form>
    </div>
  );
}
