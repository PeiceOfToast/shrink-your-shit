import { useState, type ReactNode } from "react";
import { api } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { navigate } from "../router.tsx";
import { Icon } from "../ui/Icon.tsx";
import { LibrarySource, NO_SOURCE, baseName, toProblem, type Problem, type Source } from "./LibrarySource.tsx";
import "../styles/start.css";

// First run (page 13, drafts B). One card on the background, the app mark above it, no top bar or status line.
// Three steps on one page: the password, the first library, the starting workflow. Then the dashboard.

export function Standalone({ children }: { children: ReactNode }) {
  return (
    <div className="start-page">
      <div className="start-mark">
        <span className="logo">
          <Icon name="film" />
        </span>
        syp
      </div>
      <div className="start-card">{children}</div>
    </div>
  );
}

export function Starting() {
  return (
    <Standalone>
      <p className="start-note" role="status">Starting up…</p>
    </Standalone>
  );
}

type Starter = Awaited<ReturnType<typeof api.getStarters>>[number];

export function FirstRun() {
  const starters = useApi(() => api.getStarters(), []);
  const [password, setPassword] = useState("");
  const [src, setSrc] = useState<Source>(NO_SOURCE);
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [starter, setStarter] = useState<string | null>(null);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);

  // Changing the folder fills the name from it, unless the person has typed one.
  const change = (patch: Partial<Source>) => {
    setSrc((s) => ({ ...s, ...patch }));
    if (patch.path !== undefined) {
      if (!nameTouched) setName(baseName(patch.path));
      setProblem(null);
    }
  };

  const guided: Starter[] = (starters.data ?? []).filter((s) => s.guided);
  const hasSource = src.where === "local" ? src.path.trim() !== "" : src.shareId !== null;
  const canFinish = password.length > 0 && starter !== null && hasSource;
  const canSkip = password.length > 0 && starter !== null;

  // One call, all or nothing. A refused folder keeps everything typed (the form stays as it is).
  const save = async (withLibrary: boolean) => {
    setBusy(true);
    setProblem(null);
    try {
      const library = !withLibrary
        ? null
        : src.where === "local"
          ? { name: name.trim(), path: src.path.trim() }
          : { name: name.trim(), shareId: src.shareId! };
      await api.postSetup({
        account: { password },
        library,
        starter: starter ? { id: starter, answers } : null,
      });
      navigate("/");
    } catch (e) {
      setProblem(toProblem(e));
    } finally {
      setBusy(false);
    }
  };

  const folderProblem = problem?.field === "library.path" ? problem.message : null;
  const passwordProblem = problem?.field === "account.password" ? problem.message : null;

  return (
    <Standalone>
      <h1 className="start-title">Set up syp</h1>

      <section className="start-step" aria-labelledby="st-account">
        <h2 id="st-account"><span className="start-num">1</span> Account</h2>
        <div className="field">
          <label className="lbl" htmlFor="st-pw">Password</label>
          <input
            id="st-pw"
            className="inp"
            type="password"
            autoComplete="new-password"
            value={password}
            aria-invalid={passwordProblem ? true : undefined}
            aria-describedby={passwordProblem ? "st-pw-err" : "st-pw-hint"}
            onChange={(e) => {
              setPassword(e.target.value);
              setProblem(null);
            }}
          />
          {passwordProblem ? (
            <p id="st-pw-err" className="err" role="alert">{passwordProblem}</p>
          ) : (
            <p id="st-pw-hint" className="hint">8 characters or more.</p>
          )}
        </div>
      </section>

      <section className="start-step" aria-labelledby="st-lib">
        <h2 id="st-lib"><span className="start-num">2</span> First library</h2>
        <LibrarySource value={src} onChange={change} onProblem={setProblem} onBusy={setBusy} />
        {folderProblem && <p id="st-path-err" className="err" role="alert">{folderProblem}</p>}
        <div className="field">
          <label className="lbl" htmlFor="st-name">Name</label>
          <input
            id="st-name"
            className="inp"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setNameTouched(true);
            }}
          />
        </div>
      </section>

      <section className="start-step" aria-labelledby="st-starter">
        <h2 id="st-starter"><span className="start-num">3</span> Starting workflow</h2>
        {starters.loading && !starters.data && <span className="skel" style={{ height: 96 }} />}
        {starters.error && !starters.data && (
          <p className="bad">
            Can't load the starting workflows.{" "}
            <button type="button" className="btn ghost sm" onClick={starters.reload}>Try now</button>
          </p>
        )}
        {guided.length > 0 && (
          <div className="start-starters">
            {guided.map((s) => (
              <button
                key={s.id}
                type="button"
                className="start-starter"
                aria-pressed={starter === s.id}
                onClick={() => {
                  setStarter(s.id);
                  api
                    .getStarter(s.id)
                    .then((d) => setAnswers(d.answers))
                    .catch(() => setAnswers({}));
                }}
              >
                <b>{s.name}</b>
                <span className="muted sm">{s.line}</span>
              </button>
            ))}
          </div>
        )}
      </section>

      {problem && !folderProblem && !passwordProblem && <p className="err" role="alert">{problem.message}</p>}

      <div className="start-actions">
        <button type="button" className="btn ghost" onClick={() => save(false)} disabled={!canSkip || busy}>
          Skip the library for now
        </button>
        <button type="button" className="btn primary" onClick={() => save(true)} disabled={!canFinish || busy}>
          Finish
        </button>
      </div>
    </Standalone>
  );
}
