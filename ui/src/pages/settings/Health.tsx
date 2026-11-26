import { useState } from "react";
import { api, type CheckStatus, type Doctor, type HealthCheck } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { LoadState, errText } from "./parts.tsx";

// Tells the shell to re-fetch the summary, so the red dot clears without a reload.
const refreshSummary = () => window.dispatchEvent(new Event("summary:refresh"));

const WORD: Record<CheckStatus, string> = { ok: "OK", warn: "Warning", bad: "Problem", absent: "Not found" };

function Check({ c, onDoc }: { c: HealthCheck; onDoc: (d: Doctor) => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const fix = () => {
    if (!c.fix) return;
    setBusy(true);
    setErr(null);
    api.runHealthFix(c.fix.path).then((d) => { onDoc(d); refreshSummary(); }, (e: unknown) => setErr(errText(e))).finally(() => setBusy(false));
  };
  return (
    <section className="set-group hc">
      <div className="set-row">
        <div className="set-lab">
          <b>{c.label}</b> <span className={c.status === "bad" ? "bad sm" : c.status === "ok" ? "ok sm" : "muted sm"}>{WORD[c.status]}</span>
          <div className="hint">{c.detail}</div>
        </div>
        <div className="set-ctl">
          {c.hint && <p className="muted sm">{c.hint}</p>}
          {c.fix && <button type="button" className="btn sm" onClick={fix} disabled={busy}>{c.fix.label}</button>}
          {err && <p className="bad sm" role="alert">{err}</p>}
        </div>
      </div>
    </section>
  );
}

export function Health() {
  const s = useApi(() => api.getDoctor(false), []);
  const [doc, setDoc] = useState<Doctor | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const current = doc ?? s.data;
  if (!current) return <><h2 className="set-title">Health check</h2><LoadState s={s} /></>;
  const again = () => {
    setBusy(true);
    setErr(null);
    api.getDoctor(true).then((d) => { setDoc(d); refreshSummary(); }, (e: unknown) => setErr(errText(e))).finally(() => setBusy(false));
  };
  return (
    <>
      <h2 className="set-title">Health check</h2>
      <div className="row">
        <span className="muted sm">Checked {new Date(current.ranAt).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" })}</span>
        <span className="sp" />
        <button type="button" className="btn ghost sm" onClick={again} disabled={busy}>Check again</button>
      </div>
      {err && <p className="bad" role="alert">{err}</p>}
      {current.checks.map((c) => (
        <Check key={c.id} c={c} onDoc={setDoc} />
      ))}
    </>
  );
}
