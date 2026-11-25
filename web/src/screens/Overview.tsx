// The Dashboard as an overview (user review 11-12 item 5; DESIGN-DIRECTION-clean.md 7.3, mockups/overview.html) on
// GET /api/v2/overview (#177): the headline (space saved, NET, never added to the trash's GROSS bytes), the workers,
// what is converting now, the libraries (the Libraries page's own figures) and the last 30 days. Live: job.progress
// moves the bars in place; a job, library or worker change refetches. Every number comes from the server.
import { Fragment, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, clock, count, dayClock, encoderName, lastScan, plural } from "../format";
import { getSummary, scanAllLibraries, type RealSummary } from "../dashApi";
import { getDoctor } from "../doctorApi";
import { listJobs } from "../jobsApi";
import { listWorkflows } from "../workflowApi";
import { listLibrariesV2 } from "../libraryApi";
import { convertedLine } from "../convertedLine";
import { getStartedSteps, nextStep, showGetStarted } from "../getStarted";
import { runFields, startedAt } from "../jobCards";
const HEALTH_HIDDEN = "syp-health-dismissed"; // the failed checks the Dismiss was for
import { onLive } from "../live";
import { LoadFailed, PageHead } from "../PageHead";
import { Link } from "../router";
import { useData } from "../useData";
import { allWorkers, listWorkers } from "../workersApi";
import { getOverview, type Overview as O, type OverviewJob } from "../overviewApi";
import { mockState, scanAll, summary } from "../mock";
import { GpuPct, NoGpu, WorkersSwitch } from "./Workers";
import type { JobProgressEvent } from "../types/jobs";

const STATE: Record<string, [string, string]> = { busy: ["Converting", "run"], online: ["Ready", ""], draining: ["Finishing", "warn"], paused: ["Off", ""], offline: ["Offline", "bad"], stalled: ["Not responding", "bad"] };
const slots = (w: O["workers"][number]) => w.slots.cpu ?? { used: w.slots.used ?? 0, total: w.slots.total ?? 0 };
const pct = (n: number | null) => (n == null ? null : Math.max(0, Math.min(100, Math.round(n))));
const perGB = (s: number | null) => (s == null ? "not recorded" : s < 60 ? Math.round(s) + " s" : Math.floor(s / 60) + " min" + (Math.round(s % 60) ? " " + Math.round(s % 60) + " s" : ""));
const dayName = (d: string) => new Date(d + "T12:00:00").toLocaleDateString(undefined, { day: "numeric", month: "short" }).replace(" ", "\u00a0"); // "Nov 20" never splits


type Day = O["daily"][number];
type Series = { label: string; color: string; value: (d: Day) => number };
// dataviz: one measure per chart (never two scales), stacked parts never added into a figure they aren't (freed and
// waiting are shown and named apart), thin bars with a 4 px rounded top on the baseline, 2 px gaps between parts,
// hairline solid grid, a legend for 2 or more parts, a tooltip per day and the table view below. Days before history
// are a flat stub, not a zero.
function DayBars({ title, note, days, series, fmt, tick = true, extra, quiet }: { title: string; note: ReactNode; days: Day[]; series: Series[]; fmt: (n: number) => string; tick?: boolean; extra?: (d: Day) => string | null; quiet: string }) {
  const [at, setAt] = useState<number | null>(null), ref = useRef<SVGSVGElement>(null), [W, setW] = useState(600);
  const n = days.length, sum = (d: Day) => series.reduce((a, x) => a + x.value(d), 0), allZero = days.every((x) => !x.recorded || sum(x) === 0);
  // the viewBox is the measured width in px, so a label's font-size is its real size (the small text size at any width) and the plot is 122 px tall
  const H = 160, top = 16, base = H - 22, bw = W / n, barW = Math.min(16, bw * 0.7);
  useLayoutEffect(() => { const el = ref.current; if (!el) return; const fit = () => setW(el.clientWidth || 600); fit(); const ro = new ResizeObserver(fit); ro.observe(el); return () => ro.disconnect(); }, [allZero]);
  const max = Math.max(1, ...days.filter((d) => d.recorded).map(sum));
  const y = (v: number) => ((base - top) * v) / max, notRec = days.filter((d) => !d.recorded).length;
  const roundTop = (x: number, yy: number, w: number, h: number) => { const r = Math.min(4, h, w / 2);
    return `M${x},${yy + h}V${yy + r}Q${x},${yy} ${x + r},${yy}H${x + w - r}Q${x + w},${yy} ${x + w},${yy + r}V${yy + h}Z`; };
  const d = at == null ? null : days[at];
  return <figure className="dchart" onMouseLeave={() => setAt(null)}>
    <h3 className="ch">{title} <span className="muted">{note}</span></h3>
    {allZero ? <p className="muted quiet">{quiet}</p> : <>
    {series.length > 1 ? <ul className="legend">{series.map((x) => <li key={x.label}><i style={{ background: x.color }} />{x.label}</li>)}</ul>
      : <ul className="legend ph" aria-hidden="true"><li>&nbsp;</li></ul>/* design 11-19: keeps side-by-side baselines level; hidden when the charts stack */}
    <svg ref={ref} viewBox={`0 0 ${W} ${H}`} role="img" aria-label={title + ", last " + n + " days" + (notRec ? "; the first " + notRec + " not recorded" : "") + ". The table below has every day."} className="daychart">
      {[0, 0.5, 1].map((f) => <line key={f} x1="0" x2={W} y1={base - y(max * f)} y2={base - y(max * f)} stroke={f ? "var(--border)" : "var(--border-strong)"} strokeWidth="1" vectorEffect="non-scaling-stroke" />)}
      {tick && <text x="0" y={top - 4} fill="var(--text-2)" style={{ fontSize: "var(--fs-sm)" }}>{fmt(max)}</text>}
      {at != null && <rect x={at * bw} y={top} width={bw} height={base - top} fill="var(--raised)" />}
      {days.map((dd, i) => { const x = i * bw + (bw - barW) / 2;
        if (!dd.recorded) return <rect key={dd.day} x={x} y={base - 2} width={barW} height={2} fill="var(--border)" />;
        let yy = base; const parts = series.map((s) => ({ s, h: y(s.value(dd)) })).filter((p) => p.h > 0);
        return <g key={dd.day}>{parts.map((p, k) => { const last = k === parts.length - 1, gap = k ? 2 : 0, h = Math.max(1, p.h - gap); yy -= p.h;
          return last ? <path key={k} d={roundTop(x, yy, barW, h)} fill={p.s.color} /> : <rect key={k} x={x} y={yy} width={barW} height={h} fill={p.s.color} />; })}</g>; })}
      <text x="0" y={H - 4} fill="var(--text-2)" style={{ fontSize: "var(--fs-sm)" }}>{dayName(days[0].day)}</text>
      {W >= 480 && <text x={W / 2} y={H - 4} fill="var(--text-2)" style={{ fontSize: "var(--fs-sm)" }} textAnchor="middle">{dayName(days[Math.floor(n / 2)].day)}</text>}
      <text x={W} y={H - 4} fill="var(--text-2)" style={{ fontSize: "var(--fs-sm)" }} textAnchor="end">{dayName(days[n - 1].day)}</text>
      {days.map((dd, i) => <rect key={dd.day} x={i * bw} y="0" width={bw} height={H} fill="transparent" onMouseEnter={() => setAt(i)} />)}
    </svg>
    {d && <div className="tip" style={{ left: `clamp(80px, ${((at! + 0.5) / n) * 100}%, calc(100% - 80px))` }}>
      <b>{dayName(d.day)}</b>
      {d.recorded ? series.map((x) => <span key={x.label}><i style={{ background: x.color }} />{x.label}<em>{x.value(d) ? fmt(x.value(d)) : "none"}</em></span>) : <span className="muted">not recorded</span>}
      {d.recorded && extra?.(d) && <span className="muted">{extra(d)}</span>}
    </div>}
    </>}
  </figure>;
}

// U6: a tile's sparkline, drawn from daily[] only. A day not recorded is a short baseline tick, a gap, never a zero
function Spark({ values, label, color }: { values: (number | null)[]; label: string; color: string }) {
  const W = 128, H = 56, n = values.length, max = Math.max(1, ...values.map((v) => v ?? 0));
  const x = (i: number) => (n > 1 ? (i * W) / (n - 1) : W / 2), y = (v: number) => H - 6 - ((H - 12) * v) / max;
  const runs: string[] = [];
  let cur = "";
  // a run of one point gets a zero-length segment ("M p L p"), so the round cap draws a dot instead of nothing
  const end = () => { if (cur) runs.push(cur.includes("L") ? cur : cur + "L" + cur.slice(1)); cur = ""; };
  values.forEach((v, i) => { if (v == null) end(); else cur += (cur ? "L" : "M") + x(i).toFixed(1) + "," + y(v).toFixed(1); });
  end();
  return <svg className="spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={label + (values.some((v) => v == null) ? "; the days not recorded are left blank" : "")}>
    <line x1="0" x2={W} y1={H - 2} y2={H - 2} stroke="var(--border-strong)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
    {runs.map((d) => <path key={d} d={d} fill="none" stroke={color} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />)}
    {values.map((v, i) => (v == null ? <rect key={i} x={x(i) - 1} y={H - 3} width="2" height="2" fill="var(--border-strong)" /> : null))}
  </svg>;
}

export function Overview() {
  // the sample install never calls the server (a 401 there would send the page to Sign in)
  // review 61dc891 P2-2: the workers (count, switch and rows) come from GET /api/v2/workers, the Workers page's source,
  // which has remote workers too; the overview's own worker entries add their files' progress
  const wl = useData(listWorkers, [], 15000);
  const doc = useData(() => getDoctor(), [], 300000); // a Fail shows a line linking to Settings > Health; no answer, no line
  // Dismiss hides it until the failed checks change (design V9h): an encoder Fail can stay for good while the CPU converts
  const failedNow = (doc.data?.checks ?? []).filter((c) => c.status === "bad"), failKey = failedNow.map((c) => c.id + ":" + c.detail).join("|");
  const [hidKey, setHidKey] = useState(() => { try { return localStorage.getItem(HEALTH_HIDDEN) ?? ""; } catch { return ""; } });
  const hideHealth = () => { setHidKey(failKey); try { localStorage.setItem(HEALTH_HIDDEN, failKey); } catch { /* hidden for this visit only */ }
    const h = document.querySelector<HTMLElement>(".pagehead h1"); if (h) { h.tabIndex = -1; h.focus(); } }; // the button goes away: focus the heading, as Workers does
  const o = useData(getOverview, [], 15000), sum = useData<Pick<RealSummary, "last24h" | "recentFailures">>(async () => {
    if (!MOCK) return getSummary();
    // ?mock=1: the sample summary's failures are sample jobs, not list rows; take the rows from the sample jobs list (the same shape as the real one)
    const f = await listJobs("failed", "", "", 3);
    return { ...summary(mockState()), recentFailures: failed(f) ? [] : f.items };
  }, [], 30000);
  const wf = useData(listWorkflows, [], 30000); // Get started's "a workflow exists" tick
  const libs = useData(listLibrariesV2, [], 30000); // the workflow and scanned chips: the Libraries page's own data, joined by library id
  const running = useData(() => listJobs("running", "", "", 50), [], 15000); // codec and resolution for the converting cards (the running list), joined by job id
  const [live, setLive] = useState<Record<string, Partial<OverviewJob> & { stage?: string; bitrateKbps?: number | null }>>({}), [said, setSaid] = useState("");
  const reload = useRef(o.reload); reload.current = () => (o.reload(), wl.reload(), wf.reload(), libs.reload(), running.reload());
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined, clear = false;
    return onLive((type, data) => {
      // CT 124 flicker (manager row 11-18): within one step, a progress event's unknown (null) figure never hides one already known, and the
      // 5 s worker.heartbeat refreshes the figures without dropping the live progress; a job or library change does drop it.
      // A new step (e.g. "Checking the result") takes its figures as sent, nulls included, so the last converting speed and time left go
      if (type === "job.progress") { const e = data as JobProgressEvent, p = { percent: e.progress.percent, fps: e.progress.fps, speed: e.progress.speed, etaS: e.progress.etaS, stage: e.progress.stage, bitrateKbps: e.progress.bitrateKbps };
        setLive((m) => { const newStep = p.stage != null && m[e.jobId]?.stage != null && p.stage !== m[e.jobId].stage;
          return { ...m, [e.jobId]: { ...m[e.jobId], ...(newStep ? p : Object.fromEntries(Object.entries(p).filter(([, x]) => x != null))) } }; }); return; }
      if (type === "job.state" || type === "library.changed" || type === "resync" || type.startsWith("processing.") || type.startsWith("worker.")) {
        clear ||= type !== "worker.heartbeat"; clearTimeout(t);
        t = setTimeout(() => { reload.current(); if (clear) setLive({}); clear = false; }, 400); }
    });
  }, []);
  const act = async (p: Promise<Answer<unknown>>) => { try { const r = await p; setSaid(failed(r) ? r.error.message : ""); } catch (e) { if (!(e instanceof SignedOut)) throw e; } o.reload(); wl.reload(); };
  // loading (design loading-1 to -3): the head's switch and Scan don't wait on the summary; each section keeps its real heading over card-shaped placeholders
  if (!o.data) {
    const lw = wl.data ?? [], bar = (w: number, h: number, r: number) => <span className="skel-ph" style={{ width: w, height: h, borderRadius: r }} />;
    // the design's inner shape: a square tile, three pills on the left, a pill over a short bar on the right
    const ph = (k: number) => <li key={k} className="lcard lskel" aria-hidden="true"><span className="ltile skel-ph" /><div className="lmain"><div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>{bar(72, 22, 11)}{bar(110, 22, 11)}{bar(88, 22, 11)}</div></div><div style={{ marginLeft: "auto", display: "grid", gap: 6, justifyItems: "end" }}>{bar(110, 22, 11)}{bar(70, 10, 4)}</div></li>;
    return <div className="stack ov">
      <PageHead icon="i-dashboard" title="Dashboard">
        {wl.data ? <WorkersSwitch n={lw.filter((w) => w.status === "busy" || w.status === "online").length} m={lw.length} busy={lw.some((w) => w.currentJobIds.length > 0)} finishing={lw.filter((w) => w.status === "draining").length} onSet={(a) => act(allWorkers(a))} /> : <span className="skel-ph skel-sw" aria-hidden="true" />}
        <button className="btn sm ghost" type="button" onClick={() => (MOCK ? scanAll() : act(scanAllLibraries()))}><Icon id="i-refresh" />Scan all libraries</button>
      </PageHead>
      {o.error ? <LoadFailed error={o.error} reload={o.reload} /> : <>
        <section className="tiles" aria-hidden="true">{[0, 1, 2].map((k) => <div key={k} className="tile"><span className="skel-ph" style={{ width: "40%", height: 12 }} /><span className="skel-ph" style={{ width: 120, height: 30, gridColumn: 2, gridRow: 1, justifySelf: "end" }} /><span className="skel-ph" style={{ width: "60%", height: 12 }} /></div>)}</section>
        <section className="sec"><h2>Converting now</h2><ul className="lcards" aria-hidden="true">{[0, 1].map(ph)}</ul></section>
        <section className="sec"><h2>Libraries</h2><ul className="lcards" aria-hidden="true">{[0, 1, 2].map(ph)}</ul></section>
        <section className="sec"><h2>Workers</h2><ul className="lcards" aria-hidden="true">{[0].map(ph)}</ul></section>
        <section className="sec"><h2>Last 30 days</h2><div className="lcard lskel" aria-hidden="true" style={{ minHeight: 160, display: "block" }}><span className="skel-ph" style={{ width: "40%", height: 12 }} /><div className="skel-ph" style={{ display: "block", width: "100%", height: 110, marginTop: 12 }} /></div></section>
      </>}
    </div>;
  }
  const v = o.data, h = v.headline, noLibs = v.libraries.length === 0, job = (j: OverviewJob) => ({ ...j, ...live[j.jobId] });
  // one row per worker of GET /api/v2/workers; its files' progress from the overview (or, for a worker the overview
  // doesn't list, from Converting now by job id)
  // review a7d25ec P2-2: the overview's worker entries put every job under this computer, so a row's files are the
  // worker's own currentJobIds; progress, fps and speed are looked up by job id in the overview (wherever it put them)
  const jobInfo = (id: string): OverviewJob => { const j = v.workers.flatMap((x) => x.jobs).find((x) => x.jobId === id), a = v.active.find((x) => x.jobId === id);
    return j ?? { jobId: id, name: a?.name ?? "Job " + id, percent: a?.percent ?? null, fps: null, speed: null, etaS: a?.etaS ?? null }; };
  const rows: O["workers"] = (wl.data ?? []).length ? wl.data!.map((w) => { const ow = v.workers.find((x) => x.id === w.id);
    return { id: w.id, name: w.name, kind: ow?.kind ?? "remote", status: w.status, slots: w.slots, cpuPercent: ow?.cpuPercent ?? null, gpuPercent: ow?.gpuPercent ?? null, jobs: w.currentJobIds.map(jobInfo) }; }) : v.workers;
  const on = rows.filter((w) => w.status === "busy" || w.status === "online").length, fin = rows.filter((w) => w.status === "draining").length;
  const busy = rows.some((w) => w.jobs.length > 0), failed24 = sum.data && !failed(sum.data) ? sum.data.last24h.failed : null;
  // Needs attention: the summary's newest failures (the same rows as the jobs list, so the list's own fields)
  const failedRows = sum.data && !failed(sum.data) ? sum.data.recentFailures.slice(0, 3) : [], s24 = sum.data && !failed(sum.data) ? sum.data.last24h : null, stopped = rows.filter((w) => w.status === "paused").length;
  const slotUsed = rows.reduce((a, w) => a + slots(w).used, 0), slotTotal = rows.reduce((a, w) => a + slots(w).total, 0);
  const rec = v.daily.filter((d) => d.recorded), first = rec[0]?.day, add = (f: (d: Day) => number) => rec.reduce((a, d) => a + f(d), 0);
  const tot = { done: add((d) => d.completed), skipped: add((d) => d.skipped), failed: add((d) => d.failed), freed: add((d) => d.freedBytes), saved: add((d) => d.savedBytes ?? 0), waiting: add((d) => d.waitingBytes ?? 0), kept: add((d) => d.couldFreeBytes ?? 0) };
  // run 2 (11-19): a kept original (headline couldFree) is in savedBytes but neither freed nor in the trash. A server with
  // daily couldFreeBytes (#367) says which part is kept: Freed / Waiting in the trash / Originals left in place. An older one doesn't,
  // so plot savedBytes as one series, "Files made smaller per day" (design 11-19, SCREENS line 36)
  const hasSaved = rec.some((d) => d.savedBytes != null), three = hasSaved && rec.some((d) => d.couldFreeBytes != null), kept = !three && h.couldFree > 0 && hasSaved, split = !kept && hasSaved, load = rows.some((w) => w.cpuPercent != null || w.gpuPercent != null);
  // design 11-19 (SCREENS.md:37): never "0 KB" in the note, only the parts there are; the legend keeps all three, kept is --chart-muted
  const freedPart = Math.max(0, tot.saved - tot.waiting - tot.kept), spaceNote = [freedPart > 0 ? bytes(freedPart) + " freed" : "nothing freed yet",
    tot.waiting > 0 && <Link to="/jobs?tab=completed&kept=1">{bytes(tot.waiting) + " waiting in the trash"}</Link>, tot.kept > 0 && <Link to="/jobs?tab=completed&left=1">{bytes(tot.kept) + " in originals left in place"}</Link>]
    .filter(Boolean).map((p, i) => <Fragment key={i}>{i > 0 && " · "}{p}</Fragment>);
  // Get started's three ticks from data this page already has: a library, a workflow, a worker on
  const wfCount = wf.data && !failed(wf.data) ? wf.data.length : 0, steps = getStartedSteps(v.libraries.length, wfCount, on);
  // a step's button is live once its prerequisite is done: a library needs a published workflow (step 2), so step 1 waits for it
  const side = (i: number, to: string, label: string, ready = true, after = 0) => steps[i] ? <span className="pill ok">Done</span> : ready ? <Link className="btn sm primary" to={to}>{label}</Link> : <span className="muted sm">after step {after}</span>;
  const workerOf = (id: string) => rows.find((w) => w.jobs.some((j) => j.jobId === id))?.name;
  // the sections below wait for Get started (design empty-1): Converting now, Libraries, Workers and Last 30 days hide while it shows
  const starting = showGetStarted(steps, h.filesConverted);
  const libOf = new Map((libs.data && !failed(libs.data) ? libs.data : []).map((l) => [String(l.id), l] as const));
  const runOf = new Map((running.data && !failed(running.data) ? running.data.items : []).map((j) => [j.jobId ?? j.id, j] as const));
  const perDay = convertedLine(v.daily);
  // the two averages, shown under the charts and under the "start after two days" placeholders alike
  const hasAvg = v.averages.fileShrinkPercent != null || v.averages.secondsPerGB != null;
  const avgLine = <><span>New files, on average <b>{v.averages.fileShrinkPercent == null ? "not recorded" : Math.round(v.averages.fileShrinkPercent) + "% smaller"}</b></span>
            <span>Time per GB <b>{perGB(v.averages.secondsPerGB)}</b></span></>;
  const more = [h.waitingToFree > 0 && bytes(h.waitingToFree) + (h.freed > 0 ? " more" : " frees") + (h.nextFreeAt ? " by " + dayName(h.nextFreeAt.slice(0, 10)) : ""), h.couldFree > 0 && <Link to="/jobs?tab=completed&left=1">{"could free " + bytes(h.couldFree) + " if you remove the originals"}</Link>].filter(Boolean).map((p, i) => <Fragment key={i}>{i > 0 && " · "}{p}</Fragment>);
  return (
    <div className="stack ov">
      <PageHead icon="i-dashboard" title="Dashboard">
        <WorkersSwitch n={on} m={rows.length} busy={busy} finishing={fin} onSet={(a) => act(allWorkers(a))} />
        {!noLibs && <button className="btn sm ghost" type="button" onClick={() => (MOCK ? scanAll() : act(scanAllLibraries()))}><Icon id="i-refresh" />Scan all libraries</button>}
      </PageHead>
      {said && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{said}</span></p>}
      {failedNow.length > 0 && failKey !== hidKey && <div className="note bad"><Icon id="i-alert" /><div style={{ flex: 1 }}>
        <b>{failedNow.length === 1 ? "A health check failed" : plural(failedNow.length, "health check", "health checks") + " failed"}:</b> {failedNow[0].detail} <Link to="/settings/system#health">Open Health</Link>
      </div><button className="btn sm ghost" type="button" onClick={hideHealth}>Dismiss</button></div>}
      {v.encoderFallbacks?.map((f) => <div key={f.workerId} className="note warn"><Icon id="i-alert" /><div>
        <b>{f.worker}'s graphics encoder ({encoderName(f.encoder)}) failed {f.count === 1 ? "once" : plural(f.count, "time", "times")} today.</b>{" "}
        {f.pausedUntil ? <>It converts on the processor until {clock(f.pausedUntil)}, which is slower. <Link to={"/workers/" + encodeURIComponent(f.workerId) + "?tab=capabilities"}>Test the encoders</Link></> : (f.count === 1 ? "That file was" : "Those files were") + " converted on the processor instead, which is slower."}
      </div></div>)}
      {/* U6 (C2): three tiles replace the headline band. Sparklines come from daily[] only: a day not recorded is a gap, never a zero */}
      <section className="tiles" aria-label="Totals">
        <div className="tile">
          <span className="l">Space saved</span>
          <span className={"v" + (h.freed > 0 ? " ok" : "")}>{bytes(h.freed)}</span>
          <div className="d">
            {more.length > 0 && <div>{more}</div>}
            {noLibs && h.freed <= 0 && <div><Link to="/libraries/new">Add a library</Link> to start.</div>}
            {/* design SCREENS V2 Space rules: neutral, on its own line, only when > 0; never added to or taken from the figures */}
            {(h.grew ?? 0) > 0 && <div>{bytes(h.grew!)} added by files that came out bigger</div>}
            {rec.length >= 2 && hasSaved && <div className={split ? "hide-phone" : undefined}>Last 30 days: {spaceNote}</div>}
            {rec.length < 2 && <div>Charts start after two days of conversions.</div>}
          </div>
          {rec.length >= 2 && <Spark label={(kept ? "Files made smaller per day" : "Space saved per day") + ", last " + v.daily.length + " days"} color="var(--success)" values={v.daily.map((d) => (d.recorded ? (hasSaved ? d.savedBytes : d.freedBytes) ?? 0 : null))} />}
        </div>
        <div className="tile">
          <span className="l">Files converted</span>
          <span className="v">{count(h.filesConverted)}</span>
          <div className="d"><div>{perDay != null ? <>{count(perDay)} per day{s24 && <> · last 24 h {count(s24.completed)} done, {count(s24.skipped)} skipped</>}</> : "Charts start after two days of conversions."}</div></div>
          {rec.length >= 2 && <Spark label={"Files converted per day, last " + v.daily.length + " days"} color="var(--accent)" values={v.daily.map((d) => (d.recorded ? d.completed : null))} />}
        </div>
        <div className="tile rt-desk">
          <span className="l">Right now</span>
          <span className="v">{count(v.active.length)} <small>converting</small></span>
          {/* no daily series for a live count, so no sparkline: there is no history to draw it from */}
          <div className="d"><div>{count(h.filesWaiting)} waiting{failed24 != null && <> · <Link to="/jobs?tab=failed" className={failed24 > 0 ? "bad" : undefined}>{count(failed24)} failed in 24 h</Link></>}</div>
            {stopped > 0 && <div>{plural(stopped, "worker", "workers")} stopped</div>}</div>
        </div>
      </section>

      {/* empty install: the three steps to a first conversion (design empty-1); each step's line says what it waits for */}
      {/* Get started (design empty-1): the three steps ticked from real data; the section shows until all three are done, and the next step's button is live */}
      {starting && <section className="sec" aria-labelledby="ov-g"><h2 id="ov-g">Get started</h2>
        <ul className="lcards gs" aria-label="Get started">
          <li className="lcard"><span className="ltile" aria-hidden="true"><Icon id="i-library" /></span>
            <div className="lmain"><div className="lname"><span className="ttl">Add your first library</span></div><span className="muted sm">The folder with your videos. Nothing is converted by looking.</span></div>
            <div className="lside">{side(0, "/libraries/new", "Set up", nextStep(steps) === 0)}</div></li>
          {/* step 2 is made by step 1 (Add library makes the workflow): no button of its own */}
          <li className="lcard"><span className="ltile" aria-hidden="true"><Icon id="i-workflow" /></span>
            <div className="lmain"><div className="lname"><span className="ttl">A workflow is made for you</span></div><span className="muted sm">Named after your library: HEVC, Balanced quality, same resolution. Change it any time.</span></div>
            <div className="lside">{steps[1] ? <span className="pill ok">Done</span> : <span className="muted sm">after step 1</span>}</div></li>
          <li className="lcard"><span className="ltile" aria-hidden="true"><Icon id="i-worker" /></span>
            <div className="lmain"><div className="lname"><span className="ttl">Start the worker on this computer</span></div><span className="muted sm">Nothing converts until a worker is on.</span></div>
            <div className="lside">{side(2, "/workers", "Open Workers", nextStep(steps) === 2, 2)}</div></li>
        </ul></section>}

      {/* design busy-1 / failures-1: failed jobs first, each with its reason in red; the list is the newest three */}
      {failedRows.length > 0 && <section className="sec" aria-labelledby="ov-f"><h2 id="ov-f">Needs attention {failed24 != null && <span className={"sm " + (failed24 > 0 ? "bad" : "muted")}>{count(failed24)} failed in 24 h</span>} <Link className="more" to="/jobs?tab=failed">Failed jobs</Link></h2>
        <ul className="lcards" aria-label="Failed jobs">
          {failedRows.map((j) => <li key={j.id} className="lcard">
            <span className="ltile codec" aria-hidden="true">{j.codec ?? ""}{j.resolution && <><br />{j.resolution}</>}</span>
            <div className="lmain">
              <div className="lname"><Link className="ttl" to={"/jobs?tab=failed&job=" + encodeURIComponent(j.jobId ?? j.id)} title={j.path}>{j.name}</Link><span className="lchev" aria-hidden="true">›</span></div>
              <div className="chips"><span className="mchip"><Icon id="i-folder" />{j.library}</span>{libOf.get(String(j.libraryId))?.workflowName && <span className="mchip"><Icon id="i-workflow" />{libOf.get(String(j.libraryId))!.workflowName}</span>}{j.newFileRefused && <span className="mchip">Original kept</span>}</div>
            </div>
            <div className="lside">
              <span className="sm" style={{ color: "var(--error-text)" }}>{j.reason?.message ?? "Failed"}</span>
              <span className="pill bad"><Icon id="i-alert" />Failed</span>
              {j.finishedAt && <span className="lnote">{dayClock(j.finishedAt)}</span>}
            </div>
          </li>)}
        </ul></section>}

      {!starting && <>
      <section className="sec" aria-labelledby="ov-j"><h2 id="ov-j">Converting now {rows.length > 0 && <span className="muted sm">{slotUsed} of {slotTotal} slots</span>} <Link className="more" to="/jobs?tab=running">All jobs</Link></h2>
        {!v.active.length ? <p className="muted">{on > 0 ? "Nothing is converting." : "Nothing is converting. Turn workers on to start."}</p> : (
          // design busy-1: the Jobs running card (the M4 mini card's grammar): tile, name, the Step / Speed / Time left fields, a status pill, a bar along the bottom
          <ul className="lcards" aria-label="Converting now">{v.active.map((a0) => { const a = { ...a0, ...live[a0.jobId] }, p = pct(a.percent), w = workerOf(a.jobId), rj = runOf.get(a.jobId), lib = a.library ? libOf.get(a.library.id) : undefined,
                f = runFields({ stage: a.stage ?? "", percent: a.percent, workflowPercent: 0, elapsedS: 0, speed: a.speed ?? null, etaS: a.etaS, fps: a.fps ?? null, bitrateKbps: a.bitrateKbps ?? null });
              return <li key={a.jobId} className="lcard">
                <span className="ltile codec" aria-hidden="true">{rj?.codec ? <>{rj.codec}{rj.resolution && <><br />{rj.resolution}</>}</> : <Icon id="i-file" />}</span>
                <div className="lmain">
                  <div className="lname"><Link className="ttl" to={"/jobs?tab=running&job=" + encodeURIComponent(a.jobId)}>{a.name}</Link><span className="lchev" aria-hidden="true">›</span></div>
                  <div className="chips">{a.library && <span className="mchip"><Icon id="i-folder" />{a.library.name}</span>}{lib?.workflowName && <span className="mchip"><Icon id="i-workflow" />{lib.workflowName}</span>}<span className="mchip"><Icon id="i-worker" />{w ?? "This computer"}</span></div>
                  {startedAt(rj?.progress?.elapsedS, new Date()) && <span className="muted sm">started {clock(startedAt(rj?.progress?.elapsedS, new Date())!)}</span>}
                  <div className="rfields">
                    <div className="rf"><span className="muted sm">Step</span><span className="rfv">{f.step}</span></div>
                    <div className="rf"><span className="muted sm">Speed</span><span className="rfv">{f.speed}</span></div>
                    <div className="rf"><span className="muted sm">Time left</span><span className="rfv">{f.timeLeft}</span></div>
                    <div className="rf"><span className="muted sm">Bitrate</span><span className="rfv">{f.bitrate}</span></div>
                  </div>
                </div>
                <div className="lside">
                  <span className="pill run"><Icon id="i-play" />{p != null ? "Running · " + p + "%" : "Running"}</span>
                  {a.expectedSavingBytes != null && <span className="lnote">{a.expectedSavingBytes < 0 ? "Saving: none, about " + bytes(-a.expectedSavingBytes) + " bigger so far" : "about " + bytes(a.expectedSavingBytes) + " saving expected"}</span>}
                </div>
                {p != null && <div className="lbar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={p} aria-label={a.name}><i style={{ width: p + "%" }} /></div>}
              </li>; })}</ul>)}
      </section>

      <section className="sec" aria-labelledby="ov-l"><h2 id="ov-l">Libraries <Link className="more" to="/libraries">Libraries page</Link></h2>
        {!v.libraries.length ? <p className="muted">No libraries yet. <Link to="/libraries">Add one</Link>.</p> : <>
          {/* U7: one row per library; the bar is saved ÷ (saved + size now), the figures are the Libraries page's own */}
          {/* design busy-2: a library is a card (the Libraries page's card grammar): tile, name, fact chips; saved and the share of the total on the right; a bar along the bottom */}
          <ul className="lcards" aria-label="Libraries">{v.libraries.map((l) => { const total = l.saved + l.bytesNow, share = total > 0 ? (100 * l.saved) / total : null, sp = share == null ? null : Math.round(share);
              const frees = h.nextFreeAt ? ", frees from " + dayName(h.nextFreeAt.slice(0, 10)) : "";
              const lib = libOf.get(String(l.id)); // the Libraries page's workflow and scan figures
              const facts = [l.saved === 0 && l.waitingToFree > 0 ? bytes(l.waitingToFree) + " smaller" + frees : null, l.saved > 0 && l.waitingToFree > 0 ? bytes(l.waitingToFree) + " more" + frees : null, (l.grew ?? 0) > 0 ? bytes(l.grew!) + " added by bigger files" : null].filter(Boolean);
              const files = l.counts.done + l.counts.waiting + l.counts.skipped + l.counts.failed;
              return <li key={l.id} className="lcard">
                <span className="ltile" aria-hidden="true"><Icon id="i-library" /></span>
                <div className="lmain">
                  <div className="lname"><Link className="ttl" to={"/libraries/" + l.id}>{l.name}</Link><span className="lchev" aria-hidden="true">›</span></div>
                  <div className="chips"><span className="mchip">{count(l.counts.waiting)} waiting</span><span className="mchip">{plural(files, "file", "files")}</span>{lib?.workflowName && <span className="mchip"><Icon id="i-workflow" />{lib.workflowName}</span>}{lib && <span className="mchip">{lib.scan.lastScanAt ? "scanned " + lastScan(lib.scan.lastScanAt) : "not scanned yet"}</span>}{facts.map((f) => <span key={f} className="mchip">{f}</span>)}</div>
                </div>
                <div className="lside">
                  {l.saved > 0 ? <span className="saved nowrap"><b className="gain">{bytes(l.saved)}</b> saved</span> : <span className="sm nowrap">{l.waitingToFree > 0 ? bytes(l.waitingToFree) + " smaller" : "Nothing saved yet"}</span>}
                  <span className="lnote">
                    {sp != null && <span>{sp}% of {bytes(total)}</span>}
                    {l.counts.failed > 0 && <Link className="over" to={"/jobs?tab=failed&libraryId=" + encodeURIComponent(String(l.id))}>{count(l.counts.failed)} failed</Link>}
                    {l.couldFree > 0 && l.saved === 0 && <Link to={"/jobs?tab=completed&left=1&libraryId=" + encodeURIComponent(String(l.id))}>{"could free " + bytes(l.couldFree)}</Link>}
                    {l.state === "no_workflow" ? <span className="pill warn">No workflow</span> : l.state === "paused" ? <span className="pill warn">Paused</span> : l.state === "paused_share" ? <span className="pill warn">Share not connected yet</span> : null}
                  </span>
                </div>
                {sp != null && <div className="lbar" role="progressbar" aria-label={l.name + ": " + sp + "% saved"} aria-valuemin={0} aria-valuemax={100} aria-valuenow={sp}><i style={{ width: (share ?? 0) + "%" }} /></div>}
              </li>; })}</ul>
          <details className="libhelp"><summary>What Saved and Size now mean</summary>
            <p className="muted sm">Saved is space already freed. A replaced original waits in the trash until its date: it no longer counts in Size now, but it isn't freed yet, which is the 'frees from' figure. Sizes are updated when a library is scanned.</p></details></>}
      </section>

      <section className="sec" aria-labelledby="ov-w"><h2 id="ov-w">Workers <Link className="more" to="/workers">Workers page</Link></h2>
        {!rows.length ? (wl.error && !wl.data ? <LoadFailed error={wl.error} reload={wl.reload} /> : <p className="muted">No workers yet.</p>) : (
          <table className="tbl ovt"><thead><tr><th>Worker</th><th className="num">Speed</th><th className="num">Slots</th>{load && <th className="num">CPU · GPU</th>}</tr></thead>
            <tbody>{rows.map((w) => { const [t, cls] = STATE[w.status] ?? [w.status, ""], js = w.jobs.map(job), sl = slots(w);
              return <tr key={w.id}>
                <td className="nm"><b>{w.name}</b> <span className={"pill " + cls}>{t}</span> <Link className="btn icon ghost gear" to={"/workers/" + encodeURIComponent(w.id) + "?tab=limits"} aria-label={"Settings for " + w.name} title="Settings"><Icon id="i-gear" /></Link></td>
                <td className="num" data-l="Speed">{js.map((j) => [j.fps != null && Math.round(j.fps) + " fps", j.speed != null && j.speed.toFixed(1) + "× realtime"].filter(Boolean).join(" · ")).filter(Boolean).join(", ") || (js.length ? js.map((j) => j.stage).find(Boolean) ?? "—" : "")}</td>
                <td className="num" data-l="Slots">{sl.used} of {sl.total}</td>
                {load && <td className="num" data-l="CPU · GPU">{w.status === "paused" || w.status === "offline" ? "—" : w.cpuPercent == null && w.gpuPercent == null ? "Not measured yet" : <>{w.cpuPercent == null ? "—" : Math.round(w.cpuPercent) + "%"} · {w.gpuPercent == null ? <NoGpu /> : <GpuPct n={w.gpuPercent} />}</>}</td>}
              </tr>; })}</tbody></table>)}
      </section>


      {/* the 30-day block is a closed disclosure at every width (design 1440 and 390) */}
      <section className="sec" aria-labelledby="ov-a"><details className="lastdays"><summary><h2 id="ov-a">Last 30 days</h2><span className="muted sm">: space saved per day, files per day</span></summary>
        {rec.length < 2 ? <><p className="muted quiet short">Charts start after two days of conversions. So far: {count(h.filesConverted)} {h.filesConverted === 1 ? "file" : "files"} converted.</p>{hasAvg && <div className="chartfoot">{avgLine}</div>}</> : <>
          <div className="charts">
            {/* workshop 11-19: savedBytes and waitingBytes are dated by the day a conversion finished, freedBytes by the day an
                original was deleted, so the split is savedBytes - waitingBytes freed + waitingBytes waiting; never freedBytes + waitingBytes.
                An older server without savedBytes: freed per delete day only. No top tick: a bar's height adds parts kept apart */}
            <DayBars title={kept ? "Files made smaller per day" : "Space saved per day"} days={v.daily} fmt={bytes} tick={!split} quiet="Nothing saved in the last 30 days." note={kept ? bytes(tot.saved) + " smaller · " + (h.freed > 0 ? bytes(h.freed) + " freed so far" : "nothing freed yet: originals are kept") : split ? spaceNote : bytes(tot.freed) + " freed"}
              extra={(d) => (split && (d.waitingBytes ?? 0) > 0 && h.nextFreeAt ? "Frees from " + dayName(h.nextFreeAt.slice(0, 10)) : null)}
              series={split ? [{ label: "Freed", color: "var(--success)", value: (d) => Math.max(0, (d.savedBytes ?? 0) - (d.waitingBytes ?? 0) - (d.couldFreeBytes ?? 0)) }, { label: "Waiting in the trash", color: "var(--chart-pending)", value: (d) => d.waitingBytes ?? 0 },
                ...(three ? [{ label: "Originals left in place", color: "var(--chart-muted)", value: (d: Day) => d.couldFreeBytes ?? 0 }] : [])]
                : kept ? [{ label: "Smaller", color: "var(--success)", value: (d) => d.savedBytes ?? 0 }]
                : [{ label: "Freed", color: "var(--success)", value: (d) => d.freedBytes }]} />
            <DayBars title="Files per day" days={v.daily} fmt={count} quiet="No files in the last 30 days." note={[count(tot.done) + " done", count(tot.skipped) + " skipped", count(tot.failed) + " failed"].join(" · ")}
              series={[{ label: "Done", color: "var(--success)", value: (d) => d.completed }, { label: "Skipped", color: "var(--chart-muted)", value: (d) => d.skipped }, { label: "Failed", color: "var(--error)", value: (d) => d.failed }]} />
          </div>
          <div className="chartfoot">
            {avgLine}
            <details><summary className="muted">Show as a table</summary>
              <div className="daytbl"><table className="tbl"><thead><tr><th>Day</th><th className="num">{kept ? "Smaller" : split ? "Saved" : "Freed"}</th>{split && <th className="num">Waiting in the trash</th>}{three && <th className="num">Originals left in place</th>}<th className="num">Done</th><th className="num">Skipped</th><th className="num">Failed</th></tr></thead>
                <tbody>{[...v.daily].reverse().map((d) => <tr key={d.day}><td>{dayName(d.day)}</td>{d.recorded ? <><td className="num">{bytes(split || kept ? d.savedBytes ?? 0 : d.freedBytes)}</td>{split && <td className="num">{bytes(d.waitingBytes ?? 0)}</td>}{three && <td className="num">{bytes(d.couldFreeBytes ?? 0)}</td>}<td className="num">{count(d.completed)}</td><td className="num">{count(d.skipped)}</td><td className="num">{count(d.failed)}</td></> : <td colSpan={(split ? 5 : 4) + (three ? 1 : 0)} className="muted">not recorded</td>}</tr>)}</tbody></table></div>
            </details>
          </div></>}
        {first && rec.length >= 2 && v.daily.length > 0 && first > v.daily[0].day && <p className="muted sm">Recorded from {dayName(first)}.</p>}
        {rec.length >= 2 && !(MOCK || /^(Etc\/)?UTC$/.test(v.timeZone)) && <p className="muted sm">Days run midnight to midnight, {v.timeZone.split("/").pop()!.replace(/_/g, " ")} time.</p>}
      </details></section>
      </>}
    </div>
  );
}
