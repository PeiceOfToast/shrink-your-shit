import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "../api/index.ts";
import { navigate } from "../router.tsx";
import { Standalone } from "./Start.tsx";

// Sign in (page 13): the password only. A sign-in link carries ?token=; the page takes it out of the address bar at once,
// posts it, and goes on to the dashboard on success. A bad token shows the form with its message.
export function SignIn() {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [linkToken, setLinkToken] = useState<string | null>(null);
  const sentLink = useRef(false);

  // Read the link once. StrictMode runs effects twice, so the guard keeps the token from being posted twice.
  useEffect(() => {
    if (sentLink.current) return;
    sentLink.current = true;
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token");
    if (!token) return;
    params.delete("token");
    const rest = params.toString();
    window.history.replaceState(null, "", window.location.pathname + (rest ? `?${rest}` : ""));
    setLinkToken(token);
    setBusy(true);
    api
      .signInWithToken(token)
      .then(() => navigate("/"))
      .catch((e: unknown) => {
        setLinkToken(null);
        setBusy(false);
        setProblem(e instanceof ApiError ? e.message : "Couldn't sign in. Try again.");
      });
  }, []);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setProblem(null);
    api
      .signIn(password)
      .then(() => navigate("/"))
      .catch((err: unknown) => {
        setBusy(false);
        setProblem(err instanceof ApiError ? err.message : "Couldn't sign in. Try again.");
      });
  };

  return (
    <Standalone>
      <h1 className="start-title">Sign in</h1>
      {linkToken ? (
        <p className="start-note" role="status">Signing in…</p>
      ) : (
        <form className="start-form" onSubmit={submit}>
          <div className="field">
            <label className="lbl" htmlFor="si-pw">Password</label>
            <input
              id="si-pw"
              className="inp"
              type="password"
              autoComplete="current-password"
              autoFocus
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                setProblem(null);
              }}
            />
          </div>
          {problem && <p className="err" role="alert">{problem}</p>}
          <div className="start-actions">
            <button type="submit" className="btn primary" disabled={password === "" || busy}>
              Sign in
            </button>
          </div>
        </form>
      )}
    </Standalone>
  );
}
