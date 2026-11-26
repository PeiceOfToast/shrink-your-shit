// The sign-in choice as one fieldset, used by setup (Enroll step 0) and Settings > General. Words: design's direction file.
import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";
import { MIN_PASSWORD, passwordProblem, type AuthMode } from "./signinRules";

// hideLegend: Settings has the card's heading already; the legend stays the fieldset's accessible name, read but not shown
export function ModeChoice({ mode, onMode, children, hideLegend, addressesHidden }: { mode: AuthMode; onMode: (m: AuthMode) => void; children?: ReactNode; hideLegend?: boolean; addressesHidden?: boolean }) {
  // in Docker the home option's hint says why the home choice can't be trusted, in the same span the radio describes
  const port = location.port;
  const dockerNote = "This server runs in Docker, so it can't tell your home devices from the internet. " +
    (port ? `Pick this only if port ${port} can't be reached from outside your home.` : "Pick this only if this server can't be reached from outside your home.");
  const opt = (v: AuthMode, title: string, hint: string, why?: string) => (
    <label className={"card radio enroll-card" + (mode === v ? " sel" : "")}>
      <input type="radio" name="signin-mode" value={v} checked={mode === v} onChange={() => onMode(v)} aria-describedby={"signin-" + v + "-d"} />
      <span><strong>{title}</strong>
        <span className="hint" id={"signin-" + v + "-d"}>{hint}{why && <><br />{why}</>}</span>
      </span>
    </label>);
  return (
    <fieldset className="stack" style={{ gap: 8, border: 0, padding: 0, margin: 0 }}>
      <legend className={"lab" + (hideLegend ? " sr-only" : "")}>Sign-in</legend>
      {opt("home", "No sign-in on my home network", "Anyone on your home network can open Shrink Your Shit without signing in. From anywhere else it still asks for the access token.", addressesHidden ? dockerNote : undefined)}
      {opt("password", "Require sign-in", "Everyone signs in with a password, on your home network too.", "Pick this if this computer can be reached from the internet, for example through a forwarded port or a reverse proxy.")}
      {mode === "password" && children}
    </fieldset>);
}

// The two password fields. Errors show once something is typed; the hint says what the access token is for.
export function PasswordFields({ pw, again, onPw, onAgain }: { pw: string; again: string; onPw: (v: string) => void; onAgain: (v: string) => void }) {
  const [shown, setShown] = useState(false);
  const problem = pw !== "" || again !== "" ? passwordProblem(pw, again) : null;
  return (
    <div className="stack" style={{ gap: 8, marginTop: 8 }}>
      <div className="field"><label htmlFor="signin-pw">Password</label>
        <div className="pw"><input id="signin-pw" className="input" type={shown ? "text" : "password"} autoComplete="new-password" value={pw} aria-invalid={problem ? "true" : undefined} onChange={(e) => onPw(e.target.value)} />
          <button className="btn sm icon ghost" type="button" aria-controls="signin-pw" aria-pressed={shown} aria-label="Show the password" title="Show the password" onClick={() => setShown(!shown)}><Icon id="i-eye" /></button></div>
      </div>
      <div className="field"><label htmlFor="signin-again">Type it again</label>
        <input id="signin-again" className="input" type={shown ? "text" : "password"} autoComplete="new-password" value={again} aria-invalid={problem ? "true" : undefined} onChange={(e) => onAgain(e.target.value)} />
      </div>
      {problem && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{problem}</span></p>}
      <p className="hint">At least {MIN_PASSWORD} characters. Scripts and other apps keep using the access token, not this password.</p>
    </div>);
}
