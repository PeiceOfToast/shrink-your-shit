// V2 Dashboard (spec 4; DESIGN/v2 SCREENS V2, mockups/dashboard.html, the clean pass D1-D3) on the real
// GET /api/v2/summary (#122) and /api/v2/activity (#131) with live updates, or the mock with ?mock=1. What the server
// can't give yet (CAN'T YET: per-library bytes) is not shown on the real page, and no sample value fills it in: Space
// saved by library hides there. Workers come from summary.workers (#132), live from worker.heartbeat.
import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { PageHead } from "../PageHead";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, clock, count, elapsed, plural } from "../format";
import { retry, scanAll, startAll, stopAll, summary, updatedAt, useMock } from "../mock";
import { Link } from "../router";
import { getSettings, getTrash, listLibraries } from "../libraryApi";
import { getActivity, getSummary, scanAllLibraries, startAllWorkers, stopAllWorkers, type RealSummary } from "../dashApi";
import { jobAction } from "../jobsApi";
import { onLive } from "../live";
import { useData } from "../useData";
import { StopAll } from "./Workers";
import type { ActivityEvent, Summary } from "../types/v2";
import type { JobItem, JobProgressEvent } from "../types/jobs";

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

// What the page shows, from either source. null = the source doesn't have it: that part isn't shown.
interface Row { id: string; name: string; sub: string; percent: number | null; line: string; reason?: string; meta?: string }
interface View {
  running: boolean; workersRunning: number | null; offline: number | null;
  busy: boolean;                  // a worker is converting now (live): Stop all asks first
  waiting: number; converting: number; done24: number; failed24: number; success: number | null; freed: number; could: number;
  soon: number;                   // "N more by <date>": waitingToFree (V5c, net: original minus new). Never the trash's gross bytes
  online: string | null;          // the Workers tile: "2 of 3"
  active: Row[]; failures: Row[];
  workers: { id: string; name: string; sub: string; words: string; cls: string; icon: string; gone: boolean }[] | null;
  perLibrary: { id: string; name: string; freed: number; waiting: number; could: number }[] | null;
  activity: { id: string; icon: string; text: string; at: string }[] | null;
  fresh: boolean; steps: [string, boolean, string?][]; libraries: number; scanning: boolean; updated: Date;
}
interface Acts { start: () => void; stop: (now: boolean) => void; scan: () => void; retry: (id: string) => void }

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
          {steps.map(([name, done, note]) => <li key={name}>{done ? <St cls="ok" icon="i-check-c">{name}</St> : name}{note && <> <span className="muted">{note}</span></>}</li>)}
        </ol>
        <div className="row"><Link className="btn primary" to="/start">Continue setup</Link></div>
      </div>
    </section>
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

export function Dashboard() {
  return MOCK ? <MockDashboard /> : <RealDashboard />;
}

// ?mock=1: the sample install (mock module), as before.
function MockDashboard() {
  const s = useMock(), sum = summary(s);
  const trash = useData(getTrash, [], 60000).data;
  const libName = (id: string) => s.libraries.find((l) => l.id === id)?.name ?? id;
  const v: View = {
    running: sum.activity.workersRunning > 0, workersRunning: sum.activity.workersRunning, offline: sum.badges.workersOffline, busy: s.workers.some((w) => w.currentJobIds.length > 0),
    waiting: sum.activity.waiting, converting: sum.activity.converting, done24: sum.last24h.completed, failed24: sum.last24h.failed,
    success: sum.last24h.successRate, freed: sum.bytes.freed, soon: s.trash.bytes, could: sum.bytes.waitingToFree, // the sample's figures: its trash stands for the net "more", its waitingToFree for "could free"
    online: sum.workers.filter((w) => w.status === "online" || w.status === "busy" || w.status === "draining").length + " of " + sum.workers.length,
    active: sum.activeJobs.map((j) => ({ id: j.id, name: j.file.name, sub: [j.library?.name, j.workflow.name, j.worker?.name].filter(Boolean).join(" · "), percent: j.progress?.percent ?? null,
      line: [((j.subState && STEP_WORD[j.subState]) || j.progress?.stage) + (j.currentStep ? " · step " + j.currentStep.index + " of " + j.currentStep.count : ""), j.progress?.fps != null && j.progress.fps + " fps", j.progress && elapsed(j.progress.elapsedS)].filter(Boolean).join(" · ") })),
    failures: sum.recentFailures.map((j) => ({ id: j.id, name: j.file.name, sub: "", percent: null, line: "", reason: j.reason?.message, meta: [j.library?.name, j.endedAt && clock(j.endedAt), j.currentStep?.name].filter(Boolean).join(" · ") })),
    workers: sum.workers.map((w) => {
      const [words, cls, icon] = STATUS[w.status], full = s.workers.find((x) => x.id === w.id), gone = w.status === "offline" || w.status === "stalled";
      return { id: w.id, name: w.name, sub: gone ? "Last heard from " + clock(full?.lastHeartbeatAt ?? full?.statusSince ?? "") : w.slots.cpu.used + " of " + w.slots.cpu.total + " busy", words: gone ? words + " since " + clock(full?.statusSince ?? "") : words, cls, icon, gone };
    }),
    perLibrary: sum.bytes.perLibrary.map((l) => ({ id: l.libraryId, name: libName(l.libraryId), freed: l.freed, waiting: 0, could: l.waitingToFree })), // the sample's per-library figures are originals left in place
    activity: s.activity.slice(0, 8).map((e) => ({ id: e.id, icon: EVENT_ICON[e.type], text: e.text, at: e.at })),
    fresh: !s.settings.firstRunDone,
    steps: [["Add a library", s.libraries.length > 0, s.libraries[0]?.name], ["Create a workflow", s.wf.length > 0], ["Check your worker", s.settings.firstRunStep > 3]],
    libraries: s.libraries.length, scanning: s.libraries.some((l) => l.scan.state === "scanning"), updated: updatedAt,
  };
  return <DashboardView v={v} trash={trash} acts={{ start: startAll, stop: stopAll, scan: scanAll, retry }} />;
}

// The real server: /api/v2/summary (again every 15 s, and shortly after a job changes state), progress live.
function RealDashboard() {
  const sum = useData(getSummary, [], 15000), set = useData(getSettings, []), libs = useData(listLibraries, [], 30000), tr = useData(getTrash, [], 60000), trash = tr.data;
  const feed = useData(() => getActivity(8), [], 60000), [liveFeed, setLiveFeed] = useState<ActivityEvent[]>([]);
  const [live, setLive] = useState<Record<string, JobProgressEvent["progress"]>>({}), [said, setSaid] = useState("");
  const [beat, setBeat] = useState<Record<string, { status: W["status"]; slots: W["slots"]; at: string }>>({});
  // a job's change reloads the summary and the trash (a replaced original goes into it)
  const reload = useRef(() => {}); reload.current = () => (sum.reload(), tr.reload());
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined;
    return onLive((type, data) => {
      if (type === "job.progress") { const e = data as JobProgressEvent; setLive((m) => ({ ...m, [e.jobId]: e.progress })); return; }
      if (type === "worker.heartbeat") { const h = data as { workerId: string; status: W["status"]; slots: W["slots"]; at: string }; setBeat((m) => ({ ...m, [h.workerId]: h })); return; }
      if (type === "activity") { const a = (data as { event?: ActivityEvent })?.event; if (a) setLiveFeed((f) => [a, ...f.filter((x) => x.id !== a.id)]); return; }
      if (type === "job.state" || type === "resync" || type.startsWith("processing.")) { clearTimeout(t); t = setTimeout(() => (reload.current(), setLive({}), setBeat({})), 400); }
    });
  }, []);
  const act = async (p: Promise<Answer<unknown>>) => { try { const r = await p; if (failed(r)) setSaid(r.error.message); else setSaid(""); } catch (e) { if (!(e instanceof SignedOut)) throw e; } sum.reload(); libs.reload(); };
  if (!sum.data) return <div className="stack"><PageHead icon="i-dashboard" title="Dashboard" /><div className="empty">{sum.error?.message ?? "Loading..."}</div></div>;
  const s: RealSummary = sum.data, st = set.data, nlib = libs.data?.length ?? 0;
  const row = (j: JobItem): Row => {
    const p = live[j.id] ?? j.progress;
    return { id: j.id, name: j.name, sub: [j.library, j.worker === "builtin" ? "This computer" : j.worker].filter(Boolean).join(" · "), percent: p ? Math.round(p.percent ?? p.workflowPercent) : null,
      line: p ? [p.stage, p.fps != null && p.fps + " fps", elapsed(p.elapsedS)].filter(Boolean).join(" · ") : "",
      reason: typeof j.reason === "object" && j.reason ? j.reason.message : j.detail ?? undefined, meta: [j.library, j.finishedAt && clock(j.finishedAt)].filter(Boolean).join(" · ") };
  };
  // the workers as the summary has them, with the latest heartbeat's status and slots
  const ws = s.workers?.map((w) => (beat[w.id] ? { ...w, status: beat[w.id].status, slots: beat[w.id].slots, lastHeartbeatAt: beat[w.id].at } : w)) ?? null;
  const going = ws?.filter((w) => w.status === "busy" || w.status === "online" || w.status === "draining").length ?? 0;
  const v: View = {
    running: ws ? going > 0 : s.activity.workersRunning && !s.processingPaused, workersRunning: ws ? going : null, offline: s.badges.workersOffline,
    busy: ws ? ws.some((w) => w.status === "busy" || w.currentJobIds.length > 0) : s.activity.converting > 0, // the heartbeat's status is live; the counts lag
    waiting: s.activity.waiting, converting: s.activity.converting, done24: s.last24h.completed, failed24: s.last24h.failed, success: s.last24h.successRate,
    // V5c: "Could free" is couldFree (originals left in place); waitingToFree is the trash, the "Will be freed" line
    freed: s.bytes.freed, could: s.bytes.couldFree ?? 0, soon: s.bytes.waitingToFree, online: ws ? going + " of " + ws.length : null,
    active: s.activeJobs.map(row), failures: s.recentFailures.map(row),
    workers: ws?.map((w) => {
      const [words, cls, icon] = STATUS[w.status], gone = w.status === "offline" || w.status === "stalled";
      return { id: w.id, name: w.name, sub: gone ? "Last heard from " + clock(w.lastHeartbeatAt ?? w.statusSince) : w.slots.cpu.used + " of " + w.slots.cpu.total + " busy", words: gone ? words + " since " + clock(w.statusSince) : words, cls, icon, gone };
    }) ?? null,
    perLibrary: s.bytes.perLibrary && libs.data ? s.bytes.perLibrary.map((l) => ({ id: l.libraryId, name: libs.data!.find((x) => x.id === l.libraryId)?.name ?? l.libraryId, freed: l.freed, waiting: l.waitingToFree, could: l.couldFree ?? 0 })) : null,
    // the feed (newest first) with the entries that came live since it was loaded on top
    activity: feed.data ? [...liveFeed, ...feed.data.filter((a) => !liveFeed.some((x) => x.id === a.id))].slice(0, 8).map((e) => ({ id: e.id, icon: EVENT_ICON[e.type] ?? "i-info", text: e.text, at: e.at })) : null,
    fresh: st ? !st.firstRunDone : false,
    steps: [["Add a library", nlib > 0], ["Create a workflow", nlib > 0], ["Check your worker", !!st?.firstRunDone]],
    libraries: nlib, scanning: !!libs.data?.some((l) => l.status.kind === "scanning"), updated: new Date(),
  };
  return <>
    <DashboardView v={v} trash={trash} acts={{ start: () => act(startAllWorkers()), stop: (now) => act(stopAllWorkers(now)), scan: () => act(scanAllLibraries()), retry: (id) => act(jobAction("retry", [id])) }} />
    {said && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{said}</span></p>}
  </>;
}

function DashboardView({ v, trash, acts }: { v: View; trash?: { bytes: number; lastDeleteAt: string | null }; acts: Acts }) {
  const [why, setWhy] = useState(false);
  // D2: the status and the page's actions sit in the header row; on phones the actions go under "...".
  const actions = (inline?: boolean) => <>
    {v.running
      ? <StopAll busy={v.busy} onStop={acts.stop} inline={inline} />
      : <button className="btn sm" type="button" onClick={acts.start}><Icon id="i-play" />Start all</button>}
    {v.libraries > 0 && <button className="btn sm ghost" type="button" onClick={acts.scan} disabled={v.scanning}><Icon id="i-refresh" />{v.scanning ? "Scanning..." : "Scan all libraries"}</button>}
  </>;
  const soon = v.soon, by = trash?.lastDeleteAt ? " by " + day(trash.lastDeleteAt) : ""; // the trash gives only the date (design 05:34Z)
  return (
    <div className="stack">
      <PageHead icon="i-dashboard" title="Dashboard">
        {v.running
          ? <span className="pill run"><Icon id="i-play" />{v.workersRunning != null ? "Running · " + plural(v.workersRunning, "worker", "workers") : "Running"}</span>
          : <span className="pill" title="Nothing converts until you start a worker."><Icon id="i-stop" />Stopped</span>}
        {v.offline != null && v.offline > 0 && <span className="pill bad"><Icon id="i-wifi-off" />{plural(v.offline, "worker offline", "workers offline")}</span>}
        <span className="desk">{actions()}</span>
        <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{actions(true)}</div></details>
      </PageHead>
      {!v.running && !v.fresh && <p className="muted sm" style={{ marginTop: -8 }}>Nothing converts until you start a worker.</p>}
      {v.fresh && <Setup steps={v.steps} onDone={v.steps.filter((x) => x[1]).length} />}

      {!v.fresh && <>
        {/* D1: one row of even tiles: a short label, the number, one line */}
        <div className="kpis">
          <div className="card kpi"><span className="l">Waiting</span><span className="v">{count(v.waiting)}</span><span className="sub">Updated {clock(v.updated)}</span></div>
          <div className="card kpi"><span className="l">Converting now</span><span className="v">{count(v.converting)}</span></div>
          <div className="card kpi"><span className="l">Done (24 h)</span><span className="v">{count(v.done24)}</span></div>
          <div className="card kpi"><span className="l">Failed (24 h)</span><span className="v" style={v.failed24 ? { color: "var(--error-text)" } : undefined}>{count(v.failed24)}</span>
            {v.failed24 > 0 && <Link className="sub" to="/jobs?tab=failed">See what failed</Link>}</div>
          <div className="card kpi wide">
            <span className="l">Space saved <button className="btn icon ghost help" type="button" aria-label="About space saved" aria-expanded={why} onClick={() => setWhy(!why)}><Icon id="i-info" /></button></span>
            <span className="v">{bytes(v.freed)}</span>
            {/* design D1 (clean pass: one short line; the full words are under "?"): freed space is the big figure; never added */}
            <span className="sub">{[soon > 0 && bytes(soon) + " more" + by, v.could > 0 && "could free " + bytes(v.could)].filter(Boolean).join(" · ") || "Nothing waiting"}</span>
          </div>
          <div className="card kpi"><span className="l">Success (24 h)</span><span className="v">{v.success == null ? "-" : (v.success * 100).toFixed(1) + "%"}</span></div>
          {v.online != null && <div className="card kpi"><span className="l">Workers</span><span className="v">{v.online}</span><span className="sub">online</span></div>}
        </div>
        {why && <p className="muted sm kpiwhy">
          {bytes(v.freed)} is space already freed.{soon > 0 && " Replaced originals are kept in the trash" + (trash?.lastDeleteAt ? " until " + day(trash.lastDeleteAt) : "") + ". When they're deleted, Space saved goes up by " + bytes(soon) + "."}
          {v.could > 0 && " Could free: " + bytes(v.could) + " if you remove the originals; nothing removes them automatically."} Updated when the library is scanned.</p>}
      </>}

      {/* D3: panels are a title row and a flat list */}
      <div className={v.fresh ? "grid cols2" : "grid dash"}>
        <Panel title="Active jobs" all="/jobs?tab=running" wide>
          {v.active.length === 0 ? <div className="empty">{v.running ? "Nothing is converting right now." : "Converted files appear here once a worker is running."}</div> : (
            <ul className="plist">{v.active.map((j) => (
              <li key={j.id}>
                <div className="sp"><Name className="ttl" text={j.name} /><span className="sub">{j.sub}</span></div>
                <div className="prog">
                  {j.percent != null && <div className="row nw"><div className="bar" role="progressbar" aria-valuenow={j.percent} aria-valuemin={0} aria-valuemax={100} aria-label={"This step " + j.percent + "%"}><i style={{ width: j.percent + "%" }} /></div><span className="sm">{j.percent}%</span></div>}
                  <span className="sm muted">{j.line}</span>
                </div>
              </li>))}</ul>)}
        </Panel>

        {!v.fresh && v.workers && (
          <Panel title="Workers" all="/workers">
            <ul className="plist">{v.workers.map((w) => (
              <li key={w.id} className={w.gone ? "dim" : undefined}>
                <div className="sp"><span className="ttl">{w.name}</span><span className="sub">{w.sub}</span></div>
                <span className={"pill " + w.cls}><Icon id={w.icon} />{w.words}</span>
              </li>))}</ul>
          </Panel>
        )}

        <Panel title="Recent failures" all="/jobs?tab=failed" wide>
          {v.failures.length === 0 ? <div className="empty">Nothing has failed.</div> : (
            <ul className="plist">{v.failures.map((j) => (
              <li key={j.id}>
                <div className="sp"><Name className="ttl" text={j.name} />
                  <span className="sub" title={j.reason}>{j.reason}</span>
                  <span className="sub">{j.meta}</span></div>
                <button className="btn sm ghost" type="button" onClick={() => acts.retry(j.id)} aria-label={"Retry " + j.name}><Icon id="i-refresh" />Retry</button>
              </li>))}</ul>)}
        </Panel>

        {!v.fresh && v.perLibrary && (
          <Panel title="Space saved by library" all="/libraries">
            <ul className="plist">{v.perLibrary.map((l) => {
              const all = l.freed + l.could;
              return (
                <li key={l.id} className="col">
                  <div className="row nw"><span className="ttl sp">{l.name}</span><span className="sm">{l.freed ? bytes(l.freed) + " freed" : "Nothing freed yet"}{l.waiting > 0 && <span className="muted"> · {bytes(l.waiting) + " more" + by}</span>}{l.could > 0 && <span className="muted"> · could free {bytes(l.could)}</span>}</span></div>
                  <div className="bar thin ok" role="img" aria-label={all ? Math.round((100 * l.freed) / all) + "% of what this library could free" : "Nothing to free yet"}><i style={{ width: all ? (100 * l.freed) / all + "%" : "0%" }} /></div>
                </li>);
            })}</ul>
            <p className="hint" style={{ padding: "0 var(--pad) var(--pad)" }}>The bar shows space already freed against everything the library could free, if you removed the originals. The figures are never added together. Updated when the library is scanned.</p>
          </Panel>
        )}

        {v.activity && (
          <Panel title="Recent activity" all="/jobs" wide={!v.fresh}>
            {v.activity.length === 0 ? <div className="empty">Things that happen are listed here.</div> : (
              <ul className="plist">{v.activity.map((e) => (
                <li key={e.id}><Icon id={e.icon} /><Name className="sp" text={e.text} /><span className="muted sm nowrap">{clock(e.at)}</span></li>
              ))}</ul>)}
          </Panel>
        )}
      </div>
    </div>
  );
}
