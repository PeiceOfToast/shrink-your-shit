// Sign in (a 401 from the API comes here; api.ts). POST /api/login {token} sets the same cookie the current interface
// uses, so signing in on either is enough for both. With sign-in by password (GET /api/auth: mode password and a
// password set) the password form comes first; the token form stays one link away.
import { Icon } from "../Icon";
import { PageHead } from "../PageHead";
import { useEffect, useRef, useState } from "react";
import { failed } from "../api";
import { navigate, useSearch } from "../router";
import { getAuth, signIn, type SignInError } from "../signinApi";
import { TOO_MANY, WAIT_A_MINUTE, tryAgainIn, type AuthState } from "../signinRules";

export const fromLink = { error: "" }; // a sign-in link with a wrong token (main.tsx): its message, shown once

// where the page goes after a sign-in: only a path on this site (browsers read "//host", "/\\host" and "/<tab>/host" as another site)
const afterSignIn = (next: string) => (/^\/(?![\/\\])/.test(next) && !/[\x00-\x1f\\]/.test(next) && !next.startsWith("/signin") ? next : "/");

export function SignIn() {
  const next = new URLSearchParams(useSearch()).get("next") ?? "/";
  const [auth, setAuth] = useState<AuthState | null>(null);
  const [useToken, setUseToken] = useState(false);
  const [token, setToken] = useState(""), [pw, setPw] = useState(""), [error, setError] = useState(fromLink.error), [busy, setBusy] = useState(false), [shown, setShown] = useState(false);
  // too_many_tries: secs is the server's wait, counting down to 0; null when the server gave none (cleared by typing)
  const [limit, setLimit] = useState<{ secs: number | null } | null>(null);
  useEffect(() => { fromLink.error = ""; }, []); // shown once
  useEffect(() => { getAuth().then((r) => { if (!failed(r)) setAuth(r); }); }, []);
  useEffect(() => {
    if (!limit || limit.secs === null) return;
    if (limit.secs <= 0) return setLimit(null);
    const t = setTimeout(() => setLimit({ secs: limit.secs! - 1 }), 1000);
    return () => clearTimeout(t);
  }, [limit]);
  const byPassword = auth?.mode === "password" && auth.passwordSet && !useToken;
  // switching forms clears the wrong-password line and hides the value; the too-many count carries over. Focus moves to the new field.
  const moved = useRef(false);
  const switchTo = (token: boolean) => { moved.current = true; setUseToken(token); setError(""); setShown(false); };
  useEffect(() => {
    if (!moved.current) return;
    moved.current = false;
    document.getElementById(byPassword ? "password" : "token")?.focus();
  }, [byPassword]);

  const go = async () => {
    setError(""); setBusy(true);
    const body = byPassword ? { password: pw } : { token: token.trim() };
    const r = await signIn(body).finally(() => setBusy(false));
    if (failed(r)) {
      const e = r.error as SignInError;
      if (e.code === "too_many_tries") {
        // the button is disabled now, so focus goes back to the field
        setError(""); setLimit({ secs: e.retryAfter ?? null });
        return document.getElementById(field)?.focus();
      }
      // focus stays in the field after any error (the alert and aria-describedby announce it), not on the button
      setError(e.message);
      return document.getElementById(field)?.focus();
    }
    navigate(afterSignIn(next));
  };
  const field = byPassword ? "password" : "token";
  const value = byPassword ? pw : token;
  const setValue = (v: string) => (byPassword ? setPw(v) : setToken(v), setError(""), limit?.secs === null && setLimit(null));
  return (
    <div className="signin">
      <PageHead title="Sign in" sub={byPassword ? "Type the password for this Shrink Your Shit server." : "Paste the access token for this Shrink Your Shit server."} />
      <form className="stack" onSubmit={(e) => (e.preventDefault(), go())}>
        <div className="field"><label htmlFor={field}>{byPassword ? "Password" : "Access token"}</label>
          {/* parity 11-19 #3: Show/Hide in the field, and where to find the token (README "Signing in") */}
          <div className="pw"><input id={field} className={"input" + (byPassword ? "" : " mono")} type={shown ? "text" : "password"} autoComplete="current-password" spellCheck={false} value={value} aria-invalid={error || limit ? "true" : undefined} aria-describedby={error || limit ? field + "-error" : undefined} onChange={(e) => setValue(e.target.value)} />
            <button className="btn sm icon ghost" type="button" aria-controls={field} aria-pressed={shown} aria-label={byPassword ? "Show the password" : "Show the token"} title={byPassword ? "Show the password" : "Show the token"} onClick={() => setShown(!shown)}><Icon id="i-eye" /></button></div>
          {limit ? (
            // the alert span mounts once and never changes; the count sits outside it, so it isn't read out again
            <p id={field + "-error"} className="st bad"><Icon id="i-alert" /><span><span role="alert">{TOO_MANY}{limit.secs === null && " " + WAIT_A_MINUTE}</span>{limit.secs !== null && <span> {tryAgainIn(limit.secs)}</span>}</span></p>
          ) : error && <p id={field + "-error"} className="st bad" role="alert"><Icon id="i-alert" /><span>{error}</span></p>}
        </div>
        <div className="row"><button className="btn primary" type="submit" disabled={busy || limit !== null || !value.trim()}>Sign in</button></div>
        {byPassword && <div className="row"><button className="linkbtn" type="button" onClick={() => switchTo(true)}>Use the access token instead</button></div>}
        {!byPassword && auth?.mode === "password" && auth.passwordSet && <div className="row"><button className="linkbtn" type="button" onClick={() => switchTo(false)}>Use the password instead</button></div>}
        {byPassword && (
          <details className="where"><summary>Forgot the password?</summary>
            <p className="hint">Sign in with the access token instead, then set a new password in Settings &gt; General.</p>
          </details>
        )}
        {!byPassword && (
          <details className="where" open><summary>Where do I find it?</summary>
            <p className="hint">It is in the sign-in link the server printed when it started (the part after ?token=). Or see it again on the computer that runs the app:</p>
            <dl className="hint">
              <dt>Linux</dt><dd><code>sudo shrink-your-shit token</code></dd>
              <dt>Mac</dt><dd><code>shrink-your-shit token</code> in Terminal</dd>
              <dt>Windows</dt><dd>Open shrink-your-shit from the Start menu, then Settings, Access token, Show.</dd>
              <dt>Docker</dt><dd><code>docker exec &lt;container name&gt; shrink-your-shit token</code></dd>
            </dl>
          </details>
        )}
      </form>
    </div>
  );
}
