// Settings > Processing (SCREENS V9): "Look for new files" (#136: settings.scanEvery, seconds, with scanEverySource
// "flag" until someone changes it here). Applies at once. Not here yet: New files (Add to queue / Hold).
import { useState } from "react";
import { PageHead } from "../PageHead";
import { SignedOut, failed } from "../api";
import { duration } from "../format";
import { getSettings, patchSettings } from "../libraryApi";
import { SettingsTabs } from "../pages";
import { useData } from "../useData";

const CHOICES = [30, 60, 300, 900, 3600]; // design: Every 30 seconds (default) / minute / 5 minutes / 15 minutes / hour
export const every = (s: number) => "Every " + (s === 60 ? "minute" : s === 3600 ? "hour" : duration(s));

export function ProcessingSettings() {
  const st = useData(getSettings, []);
  const [said, setSaid] = useState(""), [bad, setBad] = useState(false), [busy, setBusy] = useState(false);
  const s = st.data, now = s?.scanEvery;
  // A value set some other way (the startup flag, or the API) is shown as one more choice; "(set at startup)" only
  // when it is the flag's. Choices outside the server's bounds (#146 scanEveryMin/Max) aren't listed (design).
  const fits = (v: number) => v >= (s?.scanEveryMin ?? 0) && v <= (s?.scanEveryMax ?? Infinity);
  const listed = CHOICES.filter(fits), options = [...listed, ...(now && !listed.includes(now) ? [now] : [])].sort((a, b) => a - b); // the live value always shows
  const pick = async (v: number) => {
    setSaid(""); setBad(false); setBusy(true);
    try {
      const r = await patchSettings({ scanEvery: v });
      if (failed(r)) { setBad(true); setSaid(r.error.message); return; }
      setSaid("Saved. Libraries are now checked " + every(v).toLowerCase() + "."); st.reload();
    } catch (e) { if (!(e instanceof SignedOut)) throw e; } finally { setBusy(false); }
  };
  return (
    <div className="stack">
      <PageHead icon="i-settings" title="Settings" />
      <SettingsTabs at="processing" />
      <div className="card">
        <div className="hd"><h2>Processing</h2></div>
        <div className="bd stack">
          {!s ? <p className="muted">{st.error?.message ?? "Loading..."}</p> : now === undefined ? <p className="muted">This server doesn't say how often it looks for new files.</p> : <>
            <div className="field"><label htmlFor="scan-every">Look for new files</label>
              <select id="scan-every" className="input" value={now} disabled={busy} onChange={(e) => pick(Number(e.target.value))} aria-describedby="scan-every-h">
                {options.map((v) => <option key={v} value={v}>{every(v) + (v === now && !listed.includes(v) && s.scanEverySource === "flag" ? " (set at startup)" : "")}</option>)}
              </select>
              <span className="hint" id="scan-every-h">A new file is added once it hasn't changed between two checks, so it can take up to twice this long to appear. Choose a longer time if checking a big library keeps the disk busy.</span></div>
            {said && <p className={bad ? "st bad" : "muted"} role={bad ? "alert" : "status"}>{said}</p>}
            {/* E (SCREENS V9): job logs are deleted after this; 0 = Forever. Shown only when the server reports logKeepDays (not yet on dev) */}
            {s.logKeepDays !== undefined && <div className="field"><label htmlFor="log-keep">Keep job logs for</label>
              <select id="log-keep" className="input" value={s.logKeepDays ?? 30} disabled={busy} onChange={async (e) => { const v = Number(e.target.value); setBusy(true); const r = await patchSettings({ logKeepDays: v }); setBusy(false); if (failed(r)) { setBad(true); setSaid(r.error.message); } else { setBad(false); setSaid("Saved. Job logs are kept " + (v ? "for " + v + " days." : "for ever.")); st.reload(); } }} aria-describedby="log-keep-h" style={{ width: "auto" }}>
                {[[7, "7 days"], [30, "30 days (default)"], [90, "90 days"], [0, "Forever"]].map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
              <span className="hint" id="log-keep-h">Logs of finished and failed jobs are deleted after this. Your files aren't affected.</span></div>}
          </>}
        </div>
      </div>
    </div>
  );
}
