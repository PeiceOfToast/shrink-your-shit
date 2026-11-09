// V2 Dashboard (spec 4; DESIGN/v2 SCREENS V2, mockups/dashboard.html and dashboard-empty.html), on the typed mock.
// "Will be freed" comes from Trash (GET /api/v2/trash, mocked until it is on dev). Hidden until the contract has their
// data: Throughput (job_events), the GPU count on the Workers card, per-worker encoders. Not here yet: Edit dashboard, View (the job drawer, V6).
import { Icon } from "../Icon";
import { bytes, clock, clockS, count, elapsed, plural } from "../format";
import { retry, scanAll, startAll, stopAll, summary, updatedAt, useMock } from "../mock";
import { Link } from "../router";
import type { ActivityEvent, Summary } from "../types/v2";

type W = Summary["workers"][number];
// The short step label (design + review 02:18Z): "Converting · step 3 of 5".
const STEP_WORD: Record<string, string> = { probing: "Reading the file", converting: "Converting", checking: "Checking", saving: "Saving" };
const day = (t: string) => new Date(t).toLocaleDateString("en-GB", { day: "numeric", month: "short" });
const STATUS: Record<W["status"], [string, string, string]> = { // words, .st class, icon
  busy: ["Running", "run", "i-play"], online: ["Ready", "ok", "i-check-c"], draining: ["Stopping", "warn", "i-pause"],
  paused: ["Stopped", "wait", "i-stop"], offline: ["Offline", "bad", "i-wifi-off"], stalled: ["Not responding", "bad", "i-alert"],
};
const EVENT_ICON: Record<ActivityEvent["type"], string> = {
  "job.completed": "i-check-c", "job.skipped": "i-skip", "job.failed": "i-x-c", "worker.joined": "i-worker", "worker.left": "i-wifi-off",
  "setting.changed": "i-settings", "workflow.published": "i-workflow", "library.scanned": "i-refresh",
};

function St({ cls, icon, children }: { cls: string; icon: string; children: string }) {
  return <span className={"st " + cls}><Icon id={icon} /><span>{children}</span></span>;
}

function Setup({ steps, onDone }: { steps: [string, boolean, string?][]; onDone: number }) {
  return (
    <section className="card">
      <div className="hd"><h2>Set up in {steps.length} steps</h2><span className="sp" /><span className="muted">{onDone} of {steps.length} done</span></div>
      <div className="bd stack">
        <div className="bar" role="progressbar" aria-label="Setup" aria-valuemin={0} aria-valuemax={steps.length} aria-valuenow={onDone}><i style={{ width: (100 * onDone) / steps.length + "%" }} /></div>
        <ol className="stack" style={{ margin: 0, paddingLeft: 20 }}>
          {steps.map(([name, done, note]) => (
            <li key={name}>{done ? <St cls="ok" icon="i-check-c">{name}</St> : name}{note && <> <span className="muted">{note}</span></>}</li>
          ))}
        </ol>
        <p className="muted sm">The setup pages come in a later step of the new interface; until then, use <a href="/">the current interface</a>.</p>
      </div>
    </section>
  );
}

export function Dashboard() {
  const s = useMock(), sum = summary(s), updated = updatedAt;
  // Online: connected and taking or finishing work. A stopped (paused) worker is not counted.
  const online = sum.workers.filter((w) => w.status === "online" || w.status === "busy" || w.status === "draining").length;
  const running = sum.activity.workersRunning > 0;
  const fresh = !s.settings.firstRunDone;
  // First run's four steps (IA.md); progress from settings.firstRunStep plus what exists. Test one file waits for the
  // engine's test run (SCREENS: first run has three steps until then).
  const steps: [string, boolean, string?][] = [
    ["Add a library", s.libraries.length > 0, s.libraries[0]?.name],
    ["Create a workflow", s.wf.length > 0],
    ["Check your worker", s.settings.firstRunStep > 3],
  ];
  const libName = (id: string) => s.libraries.find((l) => l.id === id)?.name ?? id;
  return (
    <div className="stack">
      <div className="pagehead"><div><h1>Dashboard</h1></div></div>
      {fresh && <Setup steps={steps} onDone={steps.filter((x) => x[1]).length} />}

      <section className="card"><div className="bd row">
        {running
          ? <span style={{ fontSize: "var(--fs-lg)" }}><St cls="ok" icon="i-play">{"Running · " + plural(sum.activity.workersRunning, "worker", "workers")}</St></span>
          : <span style={{ fontSize: "var(--fs-lg)" }}><St cls="wait" icon="i-stop">Stopped · nothing converts until you start a worker</St></span>}
        {sum.badges.workersOffline > 0 && <St cls="bad" icon="i-wifi-off">{plural(sum.badges.workersOffline, "worker offline", "workers offline")}</St>}
        <span className="sp" />
        {!fresh && <span className="muted">Queue: <strong style={{ color: "var(--text)" }}>{plural(sum.activity.waiting, "file waiting", "files waiting")}</strong> · Updated {clockS(updated)}</span>}
        {running
          ? <button className="btn" type="button" onClick={stopAll}><Icon id="i-stop" />Stop all</button>
          : <button className="btn" type="button" onClick={startAll}><Icon id="i-play" />Start all</button>}
        <button className="btn" type="button" onClick={scanAll} disabled={s.libraries.some((l) => l.scan.state === "scanning")}>
          <Icon id="i-refresh" />{s.libraries.some((l) => l.scan.state === "scanning") ? "Scanning..." : "Scan all libraries"}
        </button>
      </div></section>

      {!fresh && (
        <div className="kpis">
          <div className="card kpi"><span className="l">Waiting</span><span className="v">{count(sum.activity.waiting)}</span></div>
          <div className="card kpi"><span className="l">Converting now</span><span className="v">{count(sum.activity.converting)}</span></div>
          <div className="card kpi"><span className="l">Done (24 h)</span><span className="v">{count(sum.last24h.completed)}</span></div>
          <Link className="card kpi" to="/jobs?tab=failed" style={{ textDecoration: "none", color: "inherit" }}>
            <span className="l">Failed (24 h)</span><span className="v" style={sum.last24h.failed ? { color: "var(--error-text)" } : undefined}>{count(sum.last24h.failed)}</span>
            {sum.last24h.failed > 0 && <span className="sub"><St cls="bad" icon="i-x-c">See what failed</St></span>}
          </Link>
          <div className="card kpi">
            <span className="l">Space saved</span><span className="v">{bytes(sum.bytes.freed)}</span>
            {s.trash.bytes > 0 && <span className="sub">Will be freed: {bytes(s.trash.bytes)}{s.trash.lastDeleteAt && " by " + day(s.trash.lastDeleteAt)}</span>}
            {sum.bytes.waitingToFree > 0 && <><span className="sub">Could free: {bytes(sum.bytes.waitingToFree)} if you remove the originals</span><span className="sub">Updated when the library is scanned.</span></>}
          </div>
          <div className="card kpi"><span className="l">Success rate (24 h)</span><span className="v">{sum.last24h.successRate == null ? "-" : (sum.last24h.successRate * 100).toFixed(1) + "%"}</span></div>
          <div className="card kpi"><span className="l">Workers</span><span className="v">{online} of {sum.workers.length}</span><span className="sub">online</span></div>
        </div>
      )}

      <div className={fresh ? "grid cols2" : "grid dash"}>
        <section className="card w2">
          <div className="hd"><h2><Link to="/jobs?tab=running">Active jobs</Link></h2></div>
          {sum.activeJobs.length === 0 ? <div className="empty">{running ? "Nothing is converting right now." : "Converted files appear here once a worker is running."}</div> : (
            <div className="tscroll"><table className="tbl">
              <thead><tr><th>File</th><th>Current step</th><th>Progress</th><th className="num">Speed</th><th className="num">Elapsed</th></tr></thead>
              <tbody>{sum.activeJobs.map((j) => (
                <tr key={j.id}>
                  <td><span className="mono tnc" title={j.file.path}>{j.file.name}</span><span className="sub">{[j.library?.name, j.workflow.name, j.worker?.name].filter(Boolean).join(" · ")}</span></td>
                  <td>{(j.subState && STEP_WORD[j.subState]) || j.progress?.stage}{j.currentStep && " · step " + j.currentStep.index + " of " + j.currentStep.count}</td>
                  <td>{j.progress?.percent != null
                    ? <div className="prog"><div className="bar" role="progressbar" aria-valuenow={j.progress.percent} aria-valuemin={0} aria-valuemax={100} aria-label={"This step " + j.progress.percent + "%"}><i style={{ width: j.progress.percent + "%" }} /></div><span className="sm muted">{j.progress.percent}%</span></div>
                    : <span className="muted sm">-</span>}</td>
                  <td className="num nowrap">{j.progress?.fps != null ? j.progress.fps + " fps" : "-"}</td>
                  <td className="num nowrap">{j.progress ? elapsed(j.progress.elapsedS) : "-"}</td>
                </tr>))}</tbody>
            </table></div>)}
        </section>

        {!fresh && (
          <section className="card">
            <div className="hd"><h2><Link to="/workers">Workers</Link></h2></div>
            <div className="bd stack">{sum.workers.map((w) => {
              // Converting is processor-only for now (SCREENS V7: GPU files-at-once hidden), so busy counts processor slots.
              const [words, cls, icon] = STATUS[w.status], busy = w.slots.cpu.used, slots = w.slots.cpu.total;
              const full = s.workers.find((x) => x.id === w.id);
              return (
                <div className="row nw" key={w.id}>
                  <div className="sp"><strong>{w.name}</strong><div className="sm muted">{w.status === "offline" || w.status === "stalled" ? "Last heard from " + clock(full?.lastHeartbeatAt ?? full?.statusSince ?? "") : busy + " of " + slots + " busy"}</div></div>
                  <St cls={cls} icon={icon}>{w.status === "offline" || w.status === "stalled" ? words + " since " + clock(full?.statusSince ?? "") : words}</St>
                </div>);
            })}</div>
          </section>
        )}

        <section className="card w2">
          <div className="hd"><h2><Link to="/jobs?tab=failed">Recent failures</Link></h2></div>
          {sum.recentFailures.length === 0 ? <div className="empty">Nothing has failed.</div> : (
            <div className="tscroll"><table className="tbl">
              <thead><tr><th>File</th><th>Why it failed</th><th><span className="sr">Actions</span></th></tr></thead>
              <tbody>{sum.recentFailures.map((j) => (
                <tr key={j.id}>
                  <td><span className="mono tnc" title={j.file.path}>{j.file.name}</span><span className="sub">{[j.library?.name, j.endedAt && clock(j.endedAt), j.currentStep?.name].filter(Boolean).join(" · ")}</span></td>
                  <td>{j.reason?.message}</td>
                  <td className="nowrap"><button className="btn sm" type="button" onClick={() => retry(j.id)} aria-label={"Retry " + j.file.name}><Icon id="i-refresh" />Retry</button></td>
                </tr>))}</tbody>
            </table></div>)}
        </section>

        {!fresh && (
          <section className="card">
            <div className="hd"><h2><Link to="/libraries">Space saved by library</Link></h2></div>
            <div className="bd stack">
              {sum.bytes.perLibrary.map((l) => {
                const all = l.freed + l.waitingToFree;
                return (
                  <div key={l.libraryId}>
                    <div className="row nw"><strong className="sp">{libName(l.libraryId)}</strong><span className="sm">{l.freed ? bytes(l.freed) + " freed" : "Nothing freed yet"}</span></div>
                    {l.waitingToFree > 0 && <div className="sm muted">Could free {bytes(l.waitingToFree)} if you remove the originals · Updated when the library is scanned.</div>}
                    <div className="bar ok" style={{ marginTop: 4 }} role="img" aria-label={all ? Math.round((100 * l.freed) / all) + "% of what this library could free" : "Nothing to free yet"}><i style={{ width: all ? (100 * l.freed) / all + "%" : "0%" }} /></div>
                  </div>);
              })}
              <p className="hint">The bar shows space already freed against everything this library could free. The figures are never added together.</p>
            </div>
          </section>
        )}

        <section className={"card" + (fresh ? "" : " w2")}>
          <div className="hd"><h2><Link to="/jobs">Recent activity</Link></h2></div>
          {s.activity.length === 0 ? <div className="empty">Things that happen are listed here.</div> : (
            <div className="bd stack">{s.activity.slice(0, 8).map((e) => (
              <div className="row nw" key={e.id}><Icon id={EVENT_ICON[e.type]} /><span className="sp tnc" title={e.text}>{e.text}</span><span className="muted sm nowrap">{clock(e.at)}</span></div>
            ))}</div>)}
        </section>
      </div>
    </div>
  );
}
