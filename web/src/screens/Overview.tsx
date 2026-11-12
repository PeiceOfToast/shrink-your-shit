// The Dashboard as an overview (user review 11-12 item 5; DESIGN-DIRECTION-clean.md 7.3, mockups/overview.html) on
// GET /api/v2/overview (#177): the headline (space saved, NET, never added to the trash's GROSS bytes), the workers,
// what is converting now, the libraries (the Libraries page's own figures) and the last 30 days. Live: job.progress
// moves the bars in place; a job, library or worker change refetches. Every number comes from the server.
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, count } from "../format";
import { getSummary, scanAllLibraries } from "../dashApi";
import { onLive } from "../live";
import { PageHead } from "../PageHead";
import { Link } from "../router";
import { useData } from "../useData";
import { allWorkers, listWorkers } from "../workersApi";
import { getOverview, type Overview as O, type OverviewJob } from "../overviewApi";
import { mockState, scanAll, summary } from "../mock";
import { WorkersSwitch } from "./Workers";
import type { JobProgressEvent } from "../types/jobs";

const STATE: Record<string, [string, string]> = { busy: ["Converting", "run"], online: ["Ready", ""], draining: ["Finishing", "warn"], paused: ["Off", ""], offline: ["Offline", "bad"], stalled: ["Not responding", "bad"] };
const slots = (w: O["workers"][number]) => w.slots.cpu ?? { used: w.slots.used ?? 0, total: w.slots.total ?? 0 };
// review a7d25ec P2-1: a percentage as people read it (one decimal below 10, whole above; "under 0.1%" for a sliver)
const pctTxt = (n: number) => (n > 0 && n < 0.1 ? "under 0.1%" : (n < 10 ? Math.round(n * 10) / 10 : Math.round(n)) + "%");
const pct = (n: number | null) => (n == null ? null : Math.max(0, Math.min(100, Math.round(n))));
const left = (s: number | null) => (s == null ? "not known yet" : s < 90 ? "under 2 min" : "about " + (s < 5400 ? Math.round(s / 60) + " min" : Math.round(s / 3600) + " h"));
const perGB = (s: number | null) => (s == null ? "not recorded" : s < 60 ? Math.round(s) + " s" : Math.floor(s / 60) + " min" + (Math.round(s % 60) ? " " + Math.round(s % 60) + " s" : ""));
const dayName = (d: string) => new Date(d + "T12:00:00").toLocaleDateString(undefined, { day: "numeric", month: "short" });

function Bar({ p, label }: { p: number | null; label?: string }) {
  return <div className="prog"><div className="bar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={p ?? undefined} aria-label={label ?? (p == null ? "Progress not known" : p + "%")}><i style={{ width: (p ?? 0) + "%" }} /></div><span className="num">{p == null ? "" : p + "%"}</span></div>;
}

// One measure per chart (two scales on one chart mislead): bars per day, the days before history a flat stub.
function DayChart({ title, days, value, fmt, color }: { title: string; days: O["daily"]; value: (d: O["daily"][number]) => number; fmt: (n: number) => string; color: string }) {
  const W = 600, H = 110, base = H - 8, max = Math.max(1, ...days.filter((d) => d.recorded).map(value)), bw = W / days.length, total = days.filter((d) => d.recorded).reduce((a, d) => a + value(d), 0);
  const notRec = days.filter((d) => !d.recorded).length;
  return <>
    <h3 className="ch">{title} <span className="muted">{fmt(total)} in {days.length} days</span></h3>
    <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={title + ", last " + days.length + " days" + (notRec ? "; the first " + notRec + " not recorded" : "")} className="daychart">
      <line x1="0" x2={W} y1={base} y2={base} stroke="var(--border)" />
      <line x1="0" x2={W} y1="10" y2="10" stroke="var(--border)" strokeDasharray="2 4" />
      <text x={W} y="8" fill="var(--text-2)" fontSize="11" textAnchor="end">{fmt(max)}</text>
      {days.map((d, i) => { const h = d.recorded ? Math.max(value(d) > 0 ? 2 : 0, ((base - 12) * value(d)) / max) : 3;
        return <rect key={d.day} x={i * bw + 2} y={base - h} width={bw - 4} height={h} rx={d.recorded && h > 4 ? 2 : 0} fill={d.recorded ? color : "var(--border)"}><title>{dayName(d.day) + ": " + (d.recorded ? fmt(value(d)) : "not recorded")}</title></rect>; })}
    </svg>
  </>;
}

export function Overview() {
  // the sample install never calls the server (a 401 there would send the page to Sign in)
  // review 61dc891 P2-2: the workers (count, switch and rows) come from GET /api/v2/workers, the Workers page's source,
  // which has remote workers too; the overview's own worker entries add their files' progress
  const wl = useData(listWorkers, [], 15000);
  const o = useData(getOverview, [], 15000), sum = useData<{ last24h: { failed: number } }>(() => (MOCK ? Promise.resolve(summary(mockState())) : getSummary()), [], 30000);
  const [live, setLive] = useState<Record<string, Partial<OverviewJob>>>({}), [said, setSaid] = useState("");
  const reload = useRef(o.reload); reload.current = () => (o.reload(), wl.reload());
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined;
    return onLive((type, data) => {
      if (type === "job.progress") { const e = data as JobProgressEvent; setLive((m) => ({ ...m, [e.jobId]: { percent: e.progress.percent ?? null, fps: e.progress.fps ?? null, speed: e.progress.speed ?? null, etaS: e.progress.etaS ?? null } })); return; }
      if (type === "job.state" || type === "library.changed" || type === "resync" || type.startsWith("processing.") || type.startsWith("worker.")) { clearTimeout(t); t = setTimeout(() => (reload.current(), setLive({})), 400); }
    });
  }, []);
  const act = async (p: Promise<Answer<unknown>>) => { try { const r = await p; setSaid(failed(r) ? r.error.message : ""); } catch (e) { if (!(e instanceof SignedOut)) throw e; } o.reload(); wl.reload(); };
  if (!o.data) return <div className="stack"><PageHead icon="i-dashboard" title="Dashboard" /><div className="empty">{o.error?.message ?? "Loading..."}</div></div>;
  const v = o.data, h = v.headline, job = (j: OverviewJob) => ({ ...j, ...live[j.jobId] });
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
  const rec = v.daily.filter((d) => d.recorded), first = rec[0]?.day, tot = { done: rec.reduce((a, d) => a + d.completed, 0), skipped: rec.reduce((a, d) => a + d.skipped, 0), failed: rec.reduce((a, d) => a + d.failed, 0) };
  const workerOf = (id: string) => rows.find((w) => w.jobs.some((j) => j.jobId === id))?.name;
  const more = [h.waitingToFree > 0 && bytes(h.waitingToFree) + " more" + (h.nextFreeAt ? " by " + dayName(h.nextFreeAt.slice(0, 10)) : ""), h.couldFree > 0 && "could free " + bytes(h.couldFree) + " if you remove the originals"].filter(Boolean).join(" · ");
  return (
    <div className="stack ov">
      <PageHead icon="i-dashboard" title="Dashboard">
        <WorkersSwitch n={on} m={rows.length} busy={busy} finishing={fin} onSet={(a) => act(allWorkers(a))} />
        <button className="btn sm ghost" type="button" onClick={() => (MOCK ? scanAll() : act(scanAllLibraries()))}><Icon id="i-refresh" />Scan all libraries</button>
      </PageHead>
      {said && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{said}</span></p>}
      <section className="headline" aria-label="Totals">
        <div><div className="big">{bytes(h.freed)}<small>saved</small></div>{more && <div className="muted">{more}</div>}</div>
        <div className="stats">
          <div><b>{count(h.filesConverted)}</b><span>files converted</span></div>
          <div><b>{count(h.filesWaiting)}</b><span>waiting</span></div>
          <div><b>{count(v.active.length)}</b><span>converting now</span></div>
          {failed24 != null && <div><Link to="/jobs?tab=failed" className={failed24 > 0 ? "bad" : undefined}><b>{count(failed24)}</b></Link><span>failed in 24 h</span></div>}
        </div>
      </section>

      <section className="sec" aria-labelledby="ov-w"><h2 id="ov-w">Workers <Link className="more" to="/workers">Workers page</Link></h2>
        {!rows.length ? <p className="muted">No workers yet.</p> : (
          <table className="tbl ovt"><thead><tr><th>Worker</th><th>Now</th><th>Progress</th><th className="num">Speed</th><th className="num">Slots</th><th className="num">CPU · GPU</th></tr></thead>
            <tbody>{rows.map((w) => { const [t, cls] = STATE[w.status] ?? [w.status, ""], js = w.jobs.map(job), sl = slots(w);
              return <tr key={w.id}>
                <td className="nm"><b>{w.name}</b> <span className={"pill " + cls}>{t}</span></td>
                <td className="full">{js.length ? js.map((j) => <span key={j.jobId} className="mono sm fname">{j.name}</span>) : <span className="muted">Nothing</span>}</td>
                <td className="full">{js.map((j) => <Bar key={j.jobId} p={pct(j.percent)} label={j.name} />)}</td>
                <td className="num" data-l="Speed">{js.map((j) => [j.fps != null && Math.round(j.fps) + " fps", j.speed != null && j.speed.toFixed(1) + "× realtime"].filter(Boolean).join(" · ")).filter(Boolean).join(", ") || (js.length ? "not known yet" : "")}</td>
                <td className="num" data-l="Slots">{sl.used} of {sl.total}</td>
                <td className="num" data-l="CPU · GPU">{w.cpuPercent == null && w.gpuPercent == null ? "not measured yet" : (w.cpuPercent == null ? "-" : Math.round(w.cpuPercent) + "%") + " · " + (w.gpuPercent == null ? "none" : Math.round(w.gpuPercent) + "%")}</td>
              </tr>; })}</tbody></table>)}
      </section>

      <section className="sec" aria-labelledby="ov-j"><h2 id="ov-j">Converting now <Link className="more" to="/jobs?tab=running">All jobs</Link></h2>
        {!v.active.length ? <p className="muted">Nothing is converting. Workers on starts them.</p> : (
          <table className="tbl ovt"><thead><tr><th>File</th><th>Progress</th><th className="num">Time left</th><th className="num">Expected saving</th></tr></thead>
            <tbody>{v.active.map((a0) => { const a = { ...a0, ...live[a0.jobId] };
              return <tr key={a.jobId}>
                <td className="full"><Link className="mono fname" to={"/jobs?tab=running&job=" + encodeURIComponent(a.jobId)}>{a.name}</Link><span className="sub">{[a.library?.name, workerOf(a.jobId)].filter(Boolean).join(" · ")}</span></td>
                <td className="full"><Bar p={pct(a.percent)} label={a.name} /></td>
                <td className="num" data-l="Time left">{left(a.etaS)}</td>
                <td className="num" data-l="Expected saving">{a.expectedSavingBytes == null ? "not known yet" : "about " + bytes(a.expectedSavingBytes)}</td>
              </tr>; })}</tbody></table>)}
      </section>

      <section className="sec" aria-labelledby="ov-l"><h2 id="ov-l">Libraries <Link className="more" to="/libraries">Libraries page</Link></h2>
        {!v.libraries.length ? <p className="muted">No libraries yet. <Link to="/libraries">Add one</Link>.</p> : <>
          <table className="tbl ovt"><thead><tr><th>Library</th><th className="num">Size before</th><th className="num">Size now</th><th className="num">Saved</th><th className="num">Done · Waiting · Skipped · Failed</th><th>Status</th></tr></thead>
            <tbody>{v.libraries.map((l) => <tr key={l.id}>
              <td className="nm"><Link to={"/libraries/" + l.id}><b>{l.name}</b></Link></td>
              <td className="num" data-l="Before">{bytes(l.bytesBefore)}</td>
              <td className="num" data-l="Now">{bytes(l.bytesNow)}</td>
              <td className="num" data-l="Saved">{l.saved > 0 ? <><b>{bytes(l.saved)}</b>{l.savedPercent != null && " · " + pctTxt(l.savedPercent)}</> : "nothing yet"}{l.waitingToFree > 0 && <span className="sub">{bytes(l.waitingToFree)} more{h.nextFreeAt ? " by " + dayName(h.nextFreeAt.slice(0, 10)) : ""}</span>}{l.saved === 0 && l.couldFree > 0 && <span className="sub">could free {bytes(l.couldFree)} by removing the originals</span>}</td>
              <td className="num full" data-l="Done · Waiting · Skipped · Failed">{[l.counts.done, l.counts.waiting, l.counts.skipped, l.counts.failed].map(count).join(" · ")}</td>
              <td>{l.state === "paused" ? <span className="pill warn">Paused</span> : <span className="pill">Watching</span>}</td>
            </tr>)}</tbody></table>
          <p className="muted sm">Saved is space already freed. A replaced original waits in the trash until its date: it no longer counts in Size now, but it isn't freed yet, which is the 'more by' figure. Sizes are updated when a library is scanned.</p></>}
      </section>

      <section className="sec" aria-labelledby="ov-a"><h2 id="ov-a">Last 30 days</h2>
        {rec.length < 2 ? <p className="muted">Charts appear after two days of history.</p> : (
          <div className="analytics">
            <figure className="chart2">
              <DayChart title="Space saved per day" days={v.daily} value={(d) => d.freedBytes} fmt={bytes} color="var(--success)" />
              <DayChart title="Files converted per day" days={v.daily} value={(d) => d.completed} fmt={count} color="var(--accent)" />
              <details><summary className="muted sm">Show as a table</summary>
                <div className="daytbl"><table className="tbl"><thead><tr><th>Day</th><th className="num">Space saved</th><th className="num">Converted</th><th className="num">Skipped</th><th className="num">Failed</th></tr></thead>
                  <tbody>{[...v.daily].reverse().map((d) => <tr key={d.day}><td>{dayName(d.day)}</td>{d.recorded ? <><td className="num">{bytes(d.freedBytes)}</td><td className="num">{count(d.completed)}</td><td className="num">{count(d.skipped)}</td><td className="num">{count(d.failed)}</td></> : <td colSpan={4} className="muted">not recorded</td>}</tr>)}</tbody></table></div>
              </details>
            </figure>
            <div>
              <dl className="kv"><dt>Done</dt><dd>{count(tot.done)}</dd><dt>Skipped</dt><dd>{count(tot.skipped)}</dd><dt>Failed</dt><dd>{count(tot.failed)}</dd></dl>
              {tot.done + tot.skipped + tot.failed > 0 && <div className="outcome" role="img" aria-label={`Done ${tot.done}, skipped ${tot.skipped}, failed ${tot.failed}`}>
                <i style={{ flex: tot.done, background: "var(--success)" }} /><i style={{ flex: tot.skipped, background: "var(--text-2)" }} /><i style={{ flex: tot.failed, background: "var(--error)" }} /></div>}
              <dl className="kv" style={{ marginTop: 16 }}>
                <dt>New files, on average</dt><dd>{v.averages.fileShrinkPercent == null ? "not recorded" : Math.round(v.averages.fileShrinkPercent) + "% smaller than the original"}</dd>
                <dt>Time per GB</dt><dd>{perGB(v.averages.secondsPerGB)}</dd></dl>
            </div>
          </div>)}
        {first && <p className="muted sm">History starts {dayName(first)}, when this install began keeping it. Earlier days are not recorded, not estimated.{MOCK || /^(Etc\/)?UTC$/.test(v.timeZone) ? "" : " Days run midnight to midnight, " + v.timeZone.split("/").pop()!.replace(/_/g, " ") + " time."}</p>}
      </section>
    </div>
  );
}
