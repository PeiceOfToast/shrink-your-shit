import { useState } from "react";
import { api, type Summary } from "../api/index.ts";

// The all-workers switch in the top bar. Off: no new jobs start and the files running finish first.
// On resumes. No confirm either way. Each worker keeps its own on/off (see the Workers page).
export function WorkersSwitch({ summary, reload }: { summary?: Summary; reload: () => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  if (!summary) return null;
  const { paused, finishing } = summary.workers;
  const label = !paused ? "Workers on" : finishing > 0 ? `Workers paused · ${finishing} finishing` : "Workers paused";
  const toggle = () => {
    setBusy(true);
    setErr(null);
    (paused ? api.startAllWorkers() : api.drainAllWorkers())
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : "Couldn't do that."))
      .finally(() => {
        setBusy(false);
        reload();
      });
  };
  return (
    <span className="gsw-wrap">
      <button type="button" className={`sw gsw ${paused ? "paused" : "on"}`} aria-pressed={!paused} disabled={busy} onClick={toggle}>
        <span className={`k ${paused ? "" : "on"}`}></span>
        <b>
          <span className="long">{label}</span>
          <span className="short">{paused ? "Paused" : "On"}</span>
        </b>
      </button>
      {err && <span className="err" role="alert">{err}</span>}
    </span>
  );
}
