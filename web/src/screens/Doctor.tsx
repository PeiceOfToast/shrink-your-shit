// Settings > Health (user item 10; contract "Doctor"): every check with OK / Warning / Fail, its one sentence and, when
// something needs doing, the fix (a sentence, so no Copy button: design). Check again re-runs the fast checks; Test encoders again runs the
// test encodes too (up to ~15 s). It reports only; it changes nothing. Open before the first run (App.tsx), linked from
// the first-run page and from the Dashboard when a check fails.
import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { PageHead } from "../PageHead";
import { SignedOut, failed } from "../api";
import { getDoctor } from "../doctorApi";
import { clock, plural } from "../format";
import { SettingsTabs } from "../pages";
import type { Doctor, DoctorCheck } from "../types/doctor";
import type { Issue } from "../types/v1";

const PILL: Record<DoctorCheck["status"], [string, string]> = { ok: ["ok", "OK"], warn: ["warn", "Warning"], bad: ["bad", "Fail"] };
const GROUPS: [string, (id: string) => boolean][] = [
  ["Tools", (id) => ["ffmpeg", "ffprobe", "x265", "ssim"].includes(id)],
  ["Encoders", (id) => id.startsWith("encoder:")],
  ["Data folder", (id) => id === "data"],
  ["Libraries", (id) => id === "libraries" || id.startsWith("library:")],
];
const group = (id: string) => GROUPS.find(([, f]) => f(id))?.[0] ?? "Other";

function Check({ c }: { c: DoctorCheck }) {
  const [cls, word] = PILL[c.status] ?? PILL.warn;
  return (
    <li className="stack" style={{ gap: 4 }}>
      <div className="row nw" style={{ alignItems: "baseline", gap: 8 }}><span className={"pill " + cls}>{word}</span><span>{c.detail}</span></div>
      {c.hint && <p className="muted" style={{ margin: "0 0 0 4px" }}><strong>Fix:</strong> {c.hint}</p>}
    </li>);
}

export function HealthSettings() {
  const [d, setD] = useState<Doctor | null>(null), [error, setError] = useState<Issue | null>(null), [busy, setBusy] = useState<"" | "check" | "test">("check");
  const n = useRef(0);
  const run = useCallback(async (retest: boolean) => {
    const my = ++n.current; setBusy(retest ? "test" : "check");
    try {
      const r = await getDoctor(retest);
      if (my !== n.current) return;
      if (failed(r)) setError(r.error); else { setD(r); setError(null); }
      setBusy("");
    } catch (e) { if (!(e instanceof SignedOut)) throw e; }
  }, []);
  useEffect(() => { run(false); }, [run]);
  const notHere = error && ["http_404", "not_found"].includes(error.code);
  const fails = d?.checks.filter((c) => c.status === "bad").length ?? 0, warns = d?.checks.filter((c) => c.status === "warn").length ?? 0;
  const summary = !d ? "" : fails || warns ? [fails && plural(fails, "check failed", "checks failed"), warns && plural(warns, "warning", "warnings")].filter(Boolean).join(", ") + "." : "Everything checks out.";

  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="health" />
      <p className="muted" style={{ margin: 0 }}>Checks that this computer can convert your files. It only looks; it changes nothing.</p>
      {notHere ? <p className="muted">This server can't run the health checks yet.</p>
        : error && !d ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error.message}</span></p>
          : !d ? <div className="empty" role="status">Checking…</div>
            : <>
              <section className="card" aria-labelledby="hs-h">
                <div className="hd"><h2 id="hs-h">{summary}</h2></div>
                <div className="bd stack">
                  <p style={{ margin: 0 }}>Conversions use {d.encoder.hardware ? "" : "the "}{d.encoder.label}.</p>
                  <p className="muted" style={{ margin: 0 }}>Checked at {clock(d.ranAt)}. {d.encodersTestedAt ? "Encoders tested at " + clock(d.encodersTestedAt) + "." : "Encoders not tested yet."} Version {d.version}.</p>
                  <div className="row" style={{ gap: 8 }}>
                    <button className="btn sm" type="button" aria-disabled={!!busy} onClick={() => { if (!busy) run(false); }}>{busy === "check" ? "Checking…" : "Check again"}</button>
                    <button className="btn sm" type="button" aria-disabled={!!busy} onClick={() => { if (!busy) run(true); }}>{busy === "test" ? "Testing encoders…" : "Test encoders again"}</button>
                  </div>
                  <p className="muted" role="status" style={{ margin: 0 }}>{busy === "test" && "Testing encoders… This takes up to 15 seconds."}</p>
                  {error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error.message}</span></p>}
                </div>
              </section>
              {GROUPS.map(([g]) => g).concat("Other").map((g) => { const cs = d.checks.filter((c) => group(c.id) === g); return cs.length > 0 &&
                <section key={g} className="card" aria-labelledby={"hg-" + g}>
                  <div className="hd"><h2 id={"hg-" + g}>{g}</h2></div>
                  <div className="bd"><ul className="stack" style={{ listStyle: "none", padding: 0, margin: 0 }}>
                    {cs.map((c) => <Check key={c.id} c={c} />)}</ul></div>
                </section>; })}
            </>}
    </div>);
}
