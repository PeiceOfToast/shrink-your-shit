// V2 Dashboard (spec 4; DESIGN/v2 SCREENS V2, mockups/dashboard.html and dashboard-empty.html), on the typed mock.
// "Will be freed" comes from Trash (GET /api/v2/trash, mocked until it is on dev). Hidden until the contract has their
// data: Throughput (job_events), the GPU count on the Workers card, per-worker encoders. Not here yet: Edit dashboard, View (the job drawer, V6).
import { Fragment, useState, type ReactNode } from "react";
import { PageHead } from "../PageHead";
import { Icon } from "../Icon";
import { bytes, clock, count, elapsed, plural } from "../format";
import { retry, scanAll, startAll, stopAll, summary, updatedAt, useMock } from "../mock";
import { Link } from "../router";
import type { ActivityEvent, Summary } from "../types/v2";

type W = Summary["workers"][number];
// The short step label (design + review 02:18Z): "Converting · step 3 of 5".
const STEP_WORD: Record<string, string> = { probing: "Reading the file", converting: "Converting", checking: "Checking", saving: "Saving" };
const day = (t: string) => new Date(t).toLocaleDateString("en-GB", { day: "numeric", month: "short" });
const STATUS: Record<W["status"], [string, string, string]> = { // words, pill class (S1: neutral unless active or a problem), icon
  busy: ["Running", "run", "i-play"], online: ["Ready", "", "i-check-c"], draining: ["Stopping", "warn", "i-pause"],
  paused: ["Stopped", "", "i-stop"], offline: ["Offline", "bad", "i-wifi-off"], stalled: ["Not responding", "bad", "i-alert"],
};
const EVENT_ICON: Record<ActivityEvent["type"], string> = {
  "job.completed": "i-check-c", "job.skipped": "i-skip", "job.failed": "i-x-c", "worker.joined": "i-worker", "worker.left": "i-wifi-off",
  "setting.changed": "i-settings", "workflow.published": "i-workflow", "library.scanned": "i-refresh",
};

// A file name may wrap after ".", "_" or "-" (never mid-word), at most 2 lines, then "..."; the whole name in title.
function Name({ text, className = "" }: { text: string; className?: string }) {
  const parts = text.split(/(?<=[._-])/);
  return <span className={"clamp2 " + className} title={text}>{parts.map((p, i) => <Fragment key={i}>{p}{i < parts.length - 1 && <wbr />}</Fragment>)}</span>;
}

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
        <div className="row"><Link className="btn primary" to="/start">Continue setup</Link></div>
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
  const scanning = s.libraries.some((l) => l.scan.state === "scanning");
  const [why, setWhy] = useState(false);
  // D2: the status and the page's actions sit in the header row; on phones the actions go under "...".
  const actions = <>
    {running
      ? <button className="btn sm" type="button" onClick={stopAll}><Icon id="i-stop" />Stop all</button>
      : <button className="btn sm" type="button" onClick={startAll}><Icon id="i-play" />Start all</button>}
    {s.libraries.length > 0 && <button className="btn sm ghost" type="button" onClick={scanAll} disabled={scanning}><Icon id="i-refresh" />{scanning ? "Scanning..." : "Scan all libraries"}</button>}
  </>;
  const saved = sum.bytes, could = saved.waitingToFree, soon = s.trash.bytes;
  return (
    <div className="stack">
      <PageHead icon="i-dashboard" title="Dashboard">
        {running
          ? <span className="pill run"><Icon id="i-play" />{"Running · " + plural(sum.activity.workersRunning, "worker", "workers")}</span>
          : <span className="pill" title="Nothing converts until you start a worker."><Icon id="i-stop" />Stopped</span>}
        {sum.badges.workersOffline > 0 && <span className="pill bad"><Icon id="i-wifi-off" />{plural(sum.badges.workersOffline, "worker offline", "workers offline")}</span>}
        <span className="desk">{actions}</span>
        <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{actions}</div></details>
      </PageHead>
      {!running && !fresh && <p className="muted sm" style={{ marginTop: -8 }}>Nothing converts until you start a worker.</p>}
      {fresh && <Setup steps={steps} onDone={steps.filter((x) => x[1]).length} />}

      {!fresh && <>
        {/* D1: one row of even tiles: a short label, the number, one line */}
        <div className="kpis">
          <div className="card kpi"><span className="l">Waiting</span><span className="v">{count(sum.activity.waiting)}</span><span className="sub">Updated {clock(updated)}</span></div>
          <div className="card kpi"><span className="l">Converting now</span><span className="v">{count(sum.activity.converting)}</span></div>
          <div className="card kpi"><span className="l">Done (24 h)</span><span className="v">{count(sum.last24h.completed)}</span></div>
          <div className="card kpi"><span className="l">Failed (24 h)</span><span className="v" style={sum.last24h.failed ? { color: "var(--error-text)" } : undefined}>{count(sum.last24h.failed)}</span>
            {sum.last24h.failed > 0 && <Link className="sub" to="/jobs?tab=failed">See what failed</Link>}</div>
          <div className="card kpi wide">
            <span className="l">Space saved <button className="btn icon ghost help" type="button" aria-label="About space saved" aria-expanded={why} onClick={() => setWhy(!why)}><Icon id="i-info" /></button></span>
            <span className="v">{bytes(saved.freed)}</span>
            <span className="sub">{[soon > 0 && "+" + bytes(soon) + (s.trash.lastDeleteAt ? " by " + day(s.trash.lastDeleteAt) : ""), could > 0 && "could free " + bytes(could)].filter(Boolean).join(" · ") || "Nothing waiting"}</span>
          </div>
          <div className="card kpi"><span className="l">Success (24 h)</span><span className="v">{sum.last24h.successRate == null ? "-" : (sum.last24h.successRate * 100).toFixed(1) + "%"}</span></div>
          <div className="card kpi"><span className="l">Workers</span><span className="v">{online} of {sum.workers.length}</span><span className="sub">online</span></div>
        </div>
        {why && <p className="muted sm kpiwhy">
          {bytes(saved.freed)} is space already freed.{soon > 0 && " " + bytes(soon) + " more will be freed when the kept originals are removed" + (s.trash.lastDeleteAt ? " by " + day(s.trash.lastDeleteAt) : "") + "."}
          {could > 0 && " Could free " + bytes(could) + " counts originals still in place; nothing removes them automatically."} Updated when the library is scanned.</p>}
      </>}

      {/* D3: panels are a title row and a flat list */}
      <div className={fresh ? "grid cols2" : "grid dash"}>
        <Panel title="Active jobs" all="/jobs?tab=running" wide>
          {sum.activeJobs.length === 0 ? <div className="empty">{running ? "Nothing is converting right now." : "Converted files appear here once a worker is running."}</div> : (
            <ul className="plist">{sum.activeJobs.map((j) => (
              <li key={j.id}>
                <div className="sp"><Name className="ttl" text={j.file.name} /><span className="sub">{[j.library?.name, j.workflow.name, j.worker?.name].filter(Boolean).join(" · ")}</span></div>
                <div className="prog">
                  {j.progress?.percent != null
                    ? <div className="row nw"><div className="bar" role="progressbar" aria-valuenow={j.progress.percent} aria-valuemin={0} aria-valuemax={100} aria-label={"This step " + j.progress.percent + "%"}><i style={{ width: j.progress.percent + "%" }} /></div><span className="sm">{j.progress.percent}%</span></div>
                    : null}
                  <span className="sm muted">{[((j.subState && STEP_WORD[j.subState]) || j.progress?.stage) + (j.currentStep ? " · step " + j.currentStep.index + " of " + j.currentStep.count : ""),
                    j.progress?.fps != null && j.progress.fps + " fps", j.progress && elapsed(j.progress.elapsedS)].filter(Boolean).join(" · ")}</span>
                </div>
              </li>))}</ul>)}
        </Panel>

        {!fresh && (
          <Panel title="Workers" all="/workers">
            <ul className="plist">{sum.workers.map((w) => {
              // Converting is processor-only for now (SCREENS V7: GPU files-at-once hidden), so busy counts processor slots.
              const [words, cls, icon] = STATUS[w.status], busy = w.slots.cpu.used, slots = w.slots.cpu.total;
              const full = s.workers.find((x) => x.id === w.id), gone = w.status === "offline" || w.status === "stalled";
              return (
                <li key={w.id} className={gone ? "dim" : undefined}>
                  <div className="sp"><span className="ttl">{w.name}</span><span className="sub">{gone ? "Last heard from " + clock(full?.lastHeartbeatAt ?? full?.statusSince ?? "") : busy + " of " + slots + " busy"}</span></div>
                  <span className={"pill " + cls}><Icon id={icon} />{gone ? words + " since " + clock(full?.statusSince ?? "") : words}</span>
                </li>);
            })}</ul>
          </Panel>
        )}

        <Panel title="Recent failures" all="/jobs?tab=failed" wide>
          {sum.recentFailures.length === 0 ? <div className="empty">Nothing has failed.</div> : (
            <ul className="plist">{sum.recentFailures.map((j) => (
              <li key={j.id}>
                <div className="sp"><Name className="ttl" text={j.file.name} />
                  <span className="sub">{j.reason?.message}</span>
                  <span className="sub">{[j.library?.name, j.endedAt && clock(j.endedAt), j.currentStep?.name].filter(Boolean).join(" · ")}</span></div>
                <button className="btn sm ghost" type="button" onClick={() => retry(j.id)} aria-label={"Retry " + j.file.name}><Icon id="i-refresh" />Retry</button>
              </li>))}</ul>)}
        </Panel>

        {!fresh && (
          <Panel title="Space saved by library" all="/libraries">
            <ul className="plist">{saved.perLibrary.map((l) => {
              const all = l.freed + l.waitingToFree;
              return (
                <li key={l.libraryId} className="col">
                  <div className="row nw"><span className="ttl sp">{libName(l.libraryId)}</span><span className="sm">{l.freed ? bytes(l.freed) + " freed" : "Nothing freed yet"}{l.waitingToFree > 0 && <span className="muted"> · could free {bytes(l.waitingToFree)}</span>}</span></div>
                  <div className="bar thin ok" role="img" aria-label={all ? Math.round((100 * l.freed) / all) + "% of what this library could free" : "Nothing to free yet"}><i style={{ width: all ? (100 * l.freed) / all + "%" : "0%" }} /></div>
                </li>);
            })}</ul>
            <p className="hint" style={{ padding: "0 var(--pad) var(--pad)" }}>The bar shows space already freed against everything the library could free, if you removed the originals. The figures are never added together. Updated when the library is scanned.</p>
          </Panel>
        )}

        <Panel title="Recent activity" all="/jobs" wide={!fresh}>
          {s.activity.length === 0 ? <div className="empty">Things that happen are listed here.</div> : (
            <ul className="plist">{s.activity.slice(0, 8).map((e) => (
              <li key={e.id}><Icon id={EVENT_ICON[e.type]} /><Name className="sp" text={e.text} /><span className="muted sm nowrap">{clock(e.at)}</span></li>
            ))}</ul>)}
        </Panel>
      </div>
    </div>
  );
}

function Panel({ title, all, wide, children }: { title: string; all: string; wide?: boolean; children: ReactNode }) {
  return (
    <section className={"card panel" + (wide ? " w2" : "")}>
      <div className="hd"><h2>{title}</h2><span className="sp" /><Link className="btn sm ghost" to={all}>View all</Link></div>
      {children}
    </section>
  );
}
