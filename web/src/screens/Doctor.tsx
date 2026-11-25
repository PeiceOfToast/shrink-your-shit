// Settings > Health (user item 10; contract "Doctor"): every check with OK / Warning / Fail (or a dimmed Not present for hardware that isn't there: counted nowhere, no Fix), its one sentence and, when
// something needs doing, the fix (a sentence, so no Copy button: design). Check again re-runs the fast checks; Test encoders again runs the
// test encodes too (up to ~15 s). It changes no settings; Test encoders again also refreshes which encoder conversions use. Open before the first run (App.tsx), linked from
// the first-run page and from the Dashboard when a check fails.
// Look (M2c, design settings-health-*.png): a banner for the summary (the .note look, green when all is well), then one card per group, one line per check.
import { useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { SignedOut, failed } from "../api";
import { getDoctor } from "../doctorApi";
import { encoderRows, healthSummary, libraryRows, type HealthTone } from "../doctorSummary";
import { clock } from "../format";
import type { Doctor, DoctorCheck } from "../types/doctor";
import type { Issue } from "../types/v1";

// the status word is read by screen readers only; the icon and the line's colour carry it on screen
const STATUS: Record<DoctorCheck["status"], { word: string; icon: string; tone: string }> = {
  ok: { word: "OK", icon: "i-check-c", tone: "ok" },
  warn: { word: "Warning", icon: "i-alert", tone: "warn" },
  bad: { word: "Fail", icon: "i-x-c", tone: "bad" },
  absent: { word: "Not present", icon: "i-info", tone: "wait" },
};
const GROUPS: [string, (id: string) => boolean][] = [
  ["Tools", (id) => ["ffmpeg", "ffprobe", "x265", "ssim"].includes(id)],
  ["Encoders", (id) => id.startsWith("encoder:")],
  ["Data folder", (id) => id === "data"],
  ["Libraries", (id) => id === "libraries" || id.startsWith("library:")],
];
const TONE_TEXT: Record<HealthTone, string> = { ok: "var(--success-text)", warn: "var(--warning-text)", bad: "var(--error-text)" };
const group = (id: string) => GROUPS.find(([, f]) => f(id))?.[0] ?? "Other";


function Check({ c }: { c: DoctorCheck }) {
  const s = STATUS[c.status] ?? STATUS.warn, absent = c.status === "absent", bad = c.status === "bad";
  return (
    <li className="stack" style={{ gap: 4 }}>
      <div className="row nw" style={{ alignItems: "flex-start", gap: 8 }}>
        <span className={"st " + s.tone} style={{ marginTop: 2 }}><Icon id={s.icon} /><span className="sr">{s.word}: </span></span>
        <span style={{ color: absent ? "var(--text-2)" : bad ? "var(--error-text)" : undefined, overflowWrap: "anywhere" }}>{c.detail}</span>
      </div>
      {c.hint && !absent && <p style={{ margin: "0 0 0 24px", color: bad ? "var(--error-text)" : "var(--text)" }}><strong>Fix:</strong> {c.hint}</p>}
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
  const sum = d ? healthSummary(d.checks) : null;
  const icon = sum?.tone === "bad" ? "i-x-c" : sum?.tone === "warn" ? "i-alert" : "i-check-c";

  return (
    <section id="health" aria-labelledby="hs-h" className="stack settings-sec">
      <p className="muted" style={{ margin: 0 }}>Checks that this computer can convert your files. It changes no settings. Fixed an encoder? Test encoders again, and the next conversion uses it.</p>
      {notHere ? <p className="muted">This server can't run the health checks yet.</p>
        : error && !d ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{error.message}</span></p>
          : !d || !sum ? <div className="empty" role="status">Checking…</div>
            : <>
              <section className={"note " + sum.tone} aria-labelledby="hs-h" style={{ alignItems: "flex-start" }}>
                <Icon id={icon} />
                <div className="sp stack" style={{ gap: 8 }}>
                  <h2 id="hs-h" style={{ margin: 0, fontSize: "1.125rem", color: TONE_TEXT[sum.tone] }}>{sum.text}</h2>
                  <p className="muted" style={{ margin: 0 }}>Checked at {clock(d.ranAt)}. {d.encodersTestedAt ? "Encoders tested at " + clock(d.encodersTestedAt) + "." : "Encoders not tested yet."} Version {d.version}.</p>
                  <div className="row" style={{ gap: 8 }}>
                    <button className="btn sm" type="button" aria-disabled={!!busy} onClick={() => { if (!busy) run(false); }}>{busy === "check" ? "Checking…" : "Check again"}</button>
                    <button className="btn sm ghost" type="button" aria-disabled={!!busy} onClick={() => { if (!busy) run(true); }}>{busy === "test" ? "Testing encoders…" : "Test encoders again"}</button>
                  </div>
                  <p className="muted" role="status" style={{ margin: 0 }}>{busy === "test" && "Testing encoders… This takes up to 15 seconds."}</p>
                  {error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error.message}</span></p>}
                </div>
              </section>
              {GROUPS.map(([g]) => g).concat("Other").map((g) => { const cs = d.checks.filter((c) => group(c.id) === g); return cs.length > 0 &&
                <section key={g} className="card" aria-labelledby={"hg-" + g}>
                  <div className="hd"><h2 id={"hg-" + g}>{g}</h2></div>
                  <ul className="health">
                    {(g === "Libraries" ? libraryRows(cs) : g === "Encoders" ? encoderRows(cs) : cs).map((c) => <Check key={c.id} c={c} />)}</ul>
                </section>; })}
            </>}
    </section>);
}
