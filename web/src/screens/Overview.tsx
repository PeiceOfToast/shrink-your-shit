// The Dashboard as an overview (user review 11-12 item 5; DESIGN-DIRECTION-clean.md 7.3, mockups/overview.html) on
// GET /api/v2/overview (#177): the headline (space saved, NET, never added to the trash's GROSS bytes), the workers,
// what is converting now, the libraries (the Libraries page's own figures) and the last 30 days. Live: job.progress
// moves the bars in place; a job, library or worker change refetches. Every number comes from the server.
import { Fragment, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent as RPointer, type ReactNode } from "react";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, clock, count, dayClock, encoderName, lastScan, plural } from "../format";
import { getSummary, scanAllLibraries, type RealSummary } from "../dashApi";
import { getDoctor } from "../doctorApi";
import { listJobs } from "../jobsApi";
import { listWorkflows } from "../workflowApi";
import { listLibrariesV2 } from "../libraryApi";
import { convertedLine } from "../convertedLine";
import { barPath } from "../sparkBars";
import { dayLine, dayRows, keyTarget } from "../barKeys";
import { filesFoot, shrinkLine, spaceFoot, spaceHeadline } from "../cardLines";
import { getStartedSteps, nextStep, showGetStarted } from "../getStarted";
import { runFields, startedAt } from "../jobCards";
import { filesFact, freshFact, lastDoneFact, nextScanFact, nothingFact, stateWord, workersFact, type Fact } from "../statusStrip";
import { convertingEmpty, libraryRight } from "../dashRowText";
import { attentionRows, LIBRARY_WORD } from "../attention";
const HEALTH_HIDDEN = "syp-health-dismissed"; // the failed checks the Dismiss was for
import { onLive } from "../live";
import { LoadFailed, PageHead } from "../PageHead";
import { Link } from "../router";
import { useData } from "../useData";
import { allWorkers, listWorkers } from "../workersApi";
import { getOverview, type Overview as O, type OverviewJob } from "../overviewApi";
import { mockState, scanAll, summary } from "../mock";
import { WorkersSwitch } from "./Workers";
import type { JobProgressEvent } from "../types/jobs";

const slots = (w: O["workers"][number]) => w.slots.cpu ?? { used: w.slots.used ?? 0, total: w.slots.total ?? 0 };
const pct = (n: number | null) => (n == null ? null : Math.max(0, Math.min(100, Math.round(n))));
const perGB = (s: number | null) => (s == null ? "not recorded" : s < 60 ? Math.round(s) + " s" : Math.floor(s / 60) + " min" + (Math.round(s % 60) ? " " + Math.round(s % 60) + " s" : ""));
const dayName = (d: string) => new Date(d + "T12:00:00").toLocaleDateString(undefined, { day: "numeric", month: "short" }).replace(" ", "\u00a0"); // "Nov 20" never splits


type Day = O["daily"][number];
type Series = { label: string; color: string; value: (d: Day) => number };
// The card bars' keyboard and tip (design-direction-tile-bars). The 30-day bars in both cards use this one hook (the old tile sparkline is gone):
// one Tab stop on the wrapper, ←/→ Home/End, the last day on focus, Esc hides the tip, a tap toggles, and the polite live
// text speaks on keys only (never on hover). Mouse hover shows the same tip.
function useBarPick(n: number, spoken: (i: number) => string) {
  const [at, setAt] = useState<number | null>(null), [said, setSaid] = useState(""), ref = useRef<HTMLDivElement>(null), tipRef = useRef<HTMLDivElement>(null);
  // the tip is centred on its day, but a phone tile or card can be narrower than the tip: shift it back inside
  useLayoutEffect(() => {
    const tip = tipRef.current, box = ref.current?.closest(".tile, .card")?.getBoundingClientRect();
    if (!tip || !box) return;
    tip.style.marginLeft = "0px";
    const r = tip.getBoundingClientRect(), pad = 4;
    const dx = Math.max(0, box.left + pad - r.left) - Math.max(0, r.right - (box.right - pad));
    if (dx) tip.style.marginLeft = dx + "px";
  }, [at]);
  // a tap outside the chart clears its tip (a touch never sends pointerleave, so this is the only way off a phone)
  useEffect(() => {
    if (at == null) return;
    const outside = (e: PointerEvent) => { if (!ref.current?.contains(e.target as Node)) setAt(null); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [at != null]);
  const onKey = (e: KeyboardEvent) => {
    const r = keyTarget(e.key, at ?? n - 1, n);
    if (r === null) return;
    if (r === "hide") return setAt(null);
    e.preventDefault();
    setAt(r); setSaid(spoken(r));
  };
  return {
    at, said, tipRef,
    wrap: { ref, tabIndex: 0, role: "group" as const, onFocus: () => setAt((a) => a ?? n - 1), onBlur: () => setAt(null), onKeyDown: onKey,
      onPointerLeave: (e: RPointer) => { if (e.pointerType === "mouse") setAt(null); } },
    hit: (i: number) => ({ onPointerEnter: (e: RPointer) => { if (e.pointerType === "mouse") setAt(i); },
      onPointerDown: (e: RPointer) => setAt((a) => (a === i && e.pointerType !== "mouse" ? null : i)) }),
  };
}

// A card's 30 days: the parts stacked bottom up in series order (Space saved's Freed and Waiting in the trash; Files
// converted's Done, Skipped and Failed), a 2-unit tick on the baseline for a day not recorded, nothing for a recorded zero.
// The tip and the live text list every part of that day (the tip's rows and the spoken line are the same rows).
function CardBars({ label, days, series, fmt, extra, quiet }: { label: string; days: Day[]; series: Series[]; fmt: (n: number) => string; extra?: (d: Day) => string | null; quiet: string }) {
  const n = days.length, W = 600, H = 120, base = H - 2, top = 2, gap = 1.5, slot = W / n, w = Math.max(1, slot * 0.7);
  const sum = (d: Day) => series.reduce((a, s) => a + s.value(d), 0), allZero = days.every((d) => !d.recorded || sum(d) === 0);
  const max = Math.max(1, ...days.filter((d) => d.recorded).map(sum)), notRec = days.filter((d) => !d.recorded).length;
  const rowsOf = (d: Day) => dayRows(d.recorded, series.map((s) => ({ label: s.label, color: s.color, value: s.value(d) })), fmt);
  const { at, said, tipRef, wrap, hit } = useBarPick(n, (i) => dayLine(dayName(days[i].day), rowsOf(days[i]), days[i].recorded ? extra?.(days[i]) : null));
  const y = (v: number) => ((base - top) * v) / max, d = at == null ? null : days[at];
  if (allZero) return <p className="muted quiet short">{quiet}</p>;
  return <div className="cbars sbars" aria-label={label} {...wrap}>
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={"One bar per day" + (notRec ? "; the first " + notRec + " not recorded" : "") + ". The table below has every day."}>
      {[0, 0.5, 1].map((f) => <line key={f} x1="0" x2={W} y1={base - y(max * f)} y2={base - y(max * f)} stroke={f ? "var(--border)" : "var(--border-strong)"} strokeWidth="1" vectorEffect="non-scaling-stroke" />)}
      {at != null && <rect x={at * slot} y="0" width={slot} height={H} fill="var(--raised)" />}
      {days.map((dd, i) => { const x = i * slot + (slot - w) / 2;
        if (!dd.recorded) return <rect key={dd.day} x={x} y={base - 2} width={w} height={2} fill="var(--border)" />;
        // a part with a value is drawn at least 1 unit tall, so a small one beside a big one (Failed 1 beside Done 4,000) still shows
        const parts = series.filter((s) => s.value(dd) > 0).map((s) => ({ s, h: Math.max(1, y(s.value(dd))) }));
        let yy = base;
        return <g key={dd.day}>{parts.map((p, k) => { const bottom = k === 0, last = k === parts.length - 1, hh = bottom ? p.h : Math.max(1, p.h - gap); yy -= p.h;
          return last ? <path key={k} d={barPath({ i, x, y: yy, w, h: hh })} fill={p.s.color} /> : <rect key={k} x={x} y={yy} width={w} height={hh} fill={p.s.color} />; })}</g>; })}
      {days.map((_, i) => <rect key={"h" + i} x={i * slot} y="0" width={slot} height={H} fill="transparent" {...hit(i)} />)}
    </svg>
    <div className="cbar-axis" aria-hidden="true"><span>{dayName(days[0].day)}</span><span>{dayName(days[n - 1].day)}</span></div>
    {d && <div ref={tipRef} className="tip" style={{ left: `${((at! + 0.5) / n) * 100}%` }}>
      <b>{dayName(d.day)}</b>
      {d.recorded ? rowsOf(d).map((r, k) => <span key={k}>{r.color && <i style={{ background: r.color }} />}{r.label}<em>{r.text}</em></span>) : <span className="muted">not recorded</span>}
      {d.recorded && extra?.(d) && <span className="muted">{extra(d)}</span>}
    </div>}
    <span aria-live="polite" style={{ position: "absolute", width: 1, height: 1, overflow: "hidden", clip: "rect(0 0 0 0)", whiteSpace: "nowrap" }}>{said}</span>
  </div>;
}

// The table behind "Show as a table": every day, newest first; a day not recorded says so across the row
function DayTable({ days, cols }: { days: Day[]; cols: { head: string; get: (d: Day) => string }[] }) {
  return <div className="daytbl"><table className="tbl"><thead><tr><th>Day</th>{cols.map((c) => <th key={c.head} className="num">{c.head}</th>)}</tr></thead>
    <tbody>{[...days].reverse().map((d) => <tr key={d.day}><td>{dayName(d.day)}</td>{d.recorded ? cols.map((c) => <td key={c.head} className="num">{c.get(d)}</td>) : <td colSpan={cols.length} className="muted">not recorded</td>}</tr>)}</tbody></table></div>;
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
      <PageHead icon="i-dashboard" title="Dashboard" />
      <section className="status" aria-label="Status"><div className="status-main"><span className="skel-ph" style={{ width: 220, height: 16 }} /></div>
        <div className="status-acts">
        {wl.data ? <WorkersSwitch n={lw.filter((w) => w.status === "busy" || w.status === "online").length} m={lw.length} busy={lw.some((w) => w.currentJobIds.length > 0)} finishing={lw.filter((w) => w.status === "draining").length} onSet={(a) => act(allWorkers(a))} /> : <span className="skel-ph skel-sw" aria-hidden="true" />}
        <button className="btn sm ghost" type="button" onClick={() => (MOCK ? scanAll() : act(scanAllLibraries()))}><Icon id="i-refresh" />Scan all libraries</button>
        </div>
      </section>
      {o.error ? <LoadFailed error={o.error} reload={o.reload} /> : <>
        <section className="cards" aria-hidden="true">{[0, 1].map((k) => <div key={k} className="card"><span className="skel-ph" style={{ width: "40%", height: 12 }} /><span className="skel-ph" style={{ width: "60%", height: 30, marginTop: 8 }} /><span className="skel-ph" style={{ display: "block", width: "100%", height: 110, marginTop: 12 }} /></div>)}</section>
        <section className="sec"><h2>Converting now</h2><ul className="lcards" aria-hidden="true">{[0, 1].map(ph)}</ul></section>
        <section className="sec"><h2>Libraries</h2><ul className="lcards" aria-hidden="true">{[0, 1, 2].map(ph)}</ul></section>
      </>}
    </div>;
  }
  const v = o.data, h = v.headline, noLibs = v.libraries.length === 0;
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
  const s24 = sum.data && !failed(sum.data) ? sum.data.last24h : null;
  const last24 = s24 && <>last 24 h: {count(s24.completed)} done, {count(s24.skipped)} skipped, <Link to="/jobs?tab=failed">{count(s24.failed)} failed</Link></>;
  const slotUsed = rows.reduce((a, w) => a + slots(w).used, 0), slotTotal = rows.reduce((a, w) => a + slots(w).total, 0);
  const rec = v.daily.filter((d) => d.recorded), first = rec[0]?.day, add = (f: (d: Day) => number) => rec.reduce((a, d) => a + f(d), 0);
  const tot = { done: add((d) => d.completed), skipped: add((d) => d.skipped), failed: add((d) => d.failed), freed: add((d) => d.freedBytes), saved: add((d) => d.savedBytes ?? 0), waiting: add((d) => d.waitingBytes ?? 0), kept: add((d) => d.couldFreeBytes ?? 0) };
  // run 2 (11-19): a kept original (headline couldFree) is in savedBytes but neither freed nor in the trash. A server with
  // daily couldFreeBytes (#367) says which part is kept: Freed / Waiting in the trash / Originals left in place. An older one doesn't,
  // so plot savedBytes as one series, "Files made smaller per day" (design 11-19, SCREENS line 36)
  const hasSaved = rec.some((d) => d.savedBytes != null), three = hasSaved && rec.some((d) => d.couldFreeBytes != null), kept = !three && h.couldFree > 0 && hasSaved, split = !kept && hasSaved;
  // design 11-19 (SCREENS.md:37): never "0 KB" in the note, only the parts there are; the legend keeps all three, kept is --chart-muted
  const freedPart = Math.max(0, tot.saved - tot.waiting - tot.kept), spaceNote = [freedPart > 0 ? bytes(freedPart) + " freed" : "nothing freed yet",
    tot.waiting > 0 && <Link to="/jobs?tab=completed&kept=1">{bytes(tot.waiting) + " waiting in the trash"}</Link>, tot.kept > 0 && <Link to="/jobs?tab=completed&left=1">{bytes(tot.kept) + " in originals left in place"}</Link>]
    .filter(Boolean).map((p, i) => <Fragment key={i}>{i > 0 && " · "}{p}</Fragment>);
  // D3: the two cards' bar parts and table columns (the SCREENS V2 Space split rules, unchanged): Freed / Waiting in the trash /
  // Originals left in place when the server has them; one series "Smaller" when only the kept split is known; else "Freed"
  const spaceSeries: Series[] = split ? [{ label: "Freed", color: "var(--success)", value: (d) => Math.max(0, (d.savedBytes ?? 0) - (d.waitingBytes ?? 0) - (d.couldFreeBytes ?? 0)) }, { label: "Waiting in the trash", color: "var(--chart-pending)", value: (d) => d.waitingBytes ?? 0 },
      ...(three ? [{ label: "Originals left in place", color: "var(--chart-muted)", value: (d: Day) => d.couldFreeBytes ?? 0 }] : [])]
    : kept ? [{ label: "Smaller", color: "var(--success)", value: (d: Day) => d.savedBytes ?? 0 }]
    : [{ label: "Freed", color: "var(--success)", value: (d: Day) => d.freedBytes }];
  const spaceCols = [{ head: kept ? "Smaller" : split ? "Saved" : "Freed", get: (d: Day) => bytes(split || kept ? d.savedBytes ?? 0 : d.freedBytes) },
    ...(split ? [{ head: "Waiting in the trash", get: (d: Day) => bytes(d.waitingBytes ?? 0) }] : []),
    ...(three ? [{ head: "Originals left in place", get: (d: Day) => bytes(d.couldFreeBytes ?? 0) }] : [])];
  const filesSeries: Series[] = [{ label: "Done", color: "var(--success)", value: (d) => d.completed }, { label: "Skipped", color: "var(--chart-muted)", value: (d) => d.skipped }, { label: "Failed", color: "var(--error)", value: (d) => d.failed }];
  const filesCols = [{ head: "Done", get: (d: Day) => count(d.completed) }, { head: "Skipped", get: (d: Day) => count(d.skipped) }, { head: "Failed", get: (d: Day) => count(d.failed) }];
  // Get started's three ticks from data this page already has: a library, a workflow, a worker on
  const wfCount = wf.data && !failed(wf.data) ? wf.data.length : 0, steps = getStartedSteps(v.libraries.length, wfCount, rows.filter((w) => w.status === "busy" || w.status === "online" || w.status === "draining").length);
  // a step's button is live once its prerequisite is done: a library needs a published workflow (step 2), so step 1 waits for it
  const side = (i: number, to: string, label: string, ready = true, after = 0) => steps[i] ? <span className="pill ok">Done</span> : ready ? <Link className="btn sm primary" to={to}>{label}</Link> : <span className="muted sm">after step {after}</span>;
  const workerOf = (id: string) => rows.find((w) => w.jobs.some((j) => j.jobId === id))?.name;
  // the sections below wait for Get started (design empty-1): Converting now, Libraries, Workers and Last 30 days hide while it shows
  const starting = showGetStarted(steps, h.filesConverted);
  const libOf = new Map((libs.data && !failed(libs.data) ? libs.data : []).map((l) => [String(l.id), l] as const));
  const runOf = new Map((running.data && !failed(running.data) ? running.data.items : []).map((j) => [j.jobId ?? j.id, j] as const));
  const perDay = convertedLine(v.daily);
  const more = [h.waitingToFree > 0 && bytes(h.waitingToFree) + (h.freed > 0 ? " more" : " frees") + (h.nextFreeAt ? " by " + dayName(h.nextFreeAt.slice(0, 10)) : ""), h.couldFree > 0 && <Link to="/jobs?tab=completed&left=1">{"could free " + bytes(h.couldFree) + " if you remove the originals"}</Link>, v.trash.bytes > 0 && <Link to="/settings/safety">{bytes(v.trash.bytes) + " in the trash"}</Link>].filter(Boolean).map((p, i) => <Fragment key={i}>{i > 0 && " · "}{p}</Fragment>);
  // Needs attention (D2): one list from the page's own data; the card and the Status strip's "Nothing needs you" both read attn.none
  const healthShown = failedNow.length > 0 && failKey !== hidKey, summaryOk = !!sum.data && !failed(sum.data), libsOk = !!libs.data && !failed(libs.data);
  const attn = attentionRows({ failures: sum.data && !failed(sum.data) ? sum.data.recentFailures : [], libraryIds: libsOk ? new Set([...libOf.keys()]) : null, healthShown, fallbacks: v.encoderFallbacks?.length ?? 0, libraries: v.libraries });
  // the Status strip (dashboard cards §1): "Nothing needs you" only once the summary and the libraries have loaded
  const docOk = !!doc.data && !failed(doc.data); // no health answer yet, or none at all, is not "nothing needs you"
  const needsNone = summaryOk && libsOk && docOk && attn.none;
  // while the Get started card shows, the strip says which step is missing (Off), not the usual facts
  const fresh = freshFact(starting ? nextStep(steps) : null);
  const sw = fresh ? { word: "Off", tone: "muted" as const } : stateWord(rows, h.filesWaiting), wfact = workersFact(rows), facts: ReactNode[] = [];
  const shown = (f: Fact) => typeof f === "string" ? f : <>{f.pre}<b>{f.value}</b>{f.post}</>; // the value is bold, the words are not
  if (fresh) facts.push(fresh);
  else {
    if (wfact) { facts.push(shown(wfact.text)); if (wfact.notReady) facts.push(<Link key="nr" to="/workers">{wfact.notReady}</Link>); }
    facts.push(shown(filesFact(h.filesWaiting)));
    for (const f of [lastDoneFact(h.lastDoneAt, new Date(), dayClock), nextScanFact(v.nextScanAt, clock), nothingFact(needsNone)]) if (f) facts.push(shown(f));
  }
  return (
    <div className="stack ov">
      <PageHead icon="i-dashboard" title="Dashboard" />
      <section className="status" aria-label="Status">
        <div className="status-main">
          <p className="status-state"><span className={"status-dot " + sw.tone} aria-hidden="true" /><b>{sw.word}</b></p>
          <p className="status-facts">{facts.map((f, k) => <Fragment key={k}>{k > 0 && " · "}{f}</Fragment>)}</p>
        </div>
        <div className="status-acts">
          <WorkersSwitch n={on} m={rows.length} busy={busy} finishing={fin} onSet={(a) => act(allWorkers(a))} />
          {!noLibs && <button className="btn sm ghost" type="button" onClick={() => (MOCK ? scanAll() : act(scanAllLibraries()))}><Icon id="i-refresh" />Scan all libraries</button>}
        </div>
      </section>
      {said && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{said}</span></p>}
      {/* Needs attention (D2, design busy-1 / failures-1): one card under the strip; failed jobs first, then the health and encoder notes and the libraries that aren't running */}
      {!attn.none && <section className="sec" aria-labelledby="ov-f"><h2 id="ov-f">Needs attention {failed24 != null && <span className={"sm " + (failed24 > 0 ? "bad" : "muted")}>{count(failed24)} failed in 24 h</span>} <Link className="more" to="/jobs?tab=failed">Failed jobs</Link></h2>
        <ul className="lcards" aria-label="Needs attention">
          {attn.failures.map((j) => <li key={j.id} className="lcard">
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
          {attn.health && <li className="lcard">
            <span className="ltile" aria-hidden="true"><Icon id="i-alert" /></span>
            <div className="lmain"><div className="lname"><b>{failedNow.length === 1 ? "A health check failed" : plural(failedNow.length, "health check", "health checks") + " failed"}:</b></div>
              <span className="sm">{failedNow[0].detail} <Link to="/settings/system#health">Open Health</Link></span></div>
            <div className="lside"><button className="btn sm ghost" type="button" aria-label="Dismiss the health check note" onClick={hideHealth}>Dismiss</button></div>
          </li>}
          {v.encoderFallbacks?.map((f) => <li key={f.workerId} className="lcard">
            <span className="ltile" aria-hidden="true"><Icon id="i-alert" /></span>
            <div className="lmain"><div className="lname"><b>{f.worker}'s graphics encoder ({encoderName(f.encoder)}) failed {f.count === 1 ? "once" : plural(f.count, "time", "times")} today.</b></div>
              <span className="sm">{f.pausedUntil ? <>It converts on the processor until {clock(f.pausedUntil)}, which is slower. <Link to={"/workers/" + encodeURIComponent(f.workerId) + "?tab=capabilities"}>Test the encoders</Link></> : (f.count === 1 ? "That file was" : "Those files were") + " converted on the processor instead, which is slower."}</span></div>
          </li>)}
          {attn.libraries.map((l) => { const [word, meaning] = LIBRARY_WORD[l.state] ?? [l.state, ""]; return <li key={"lib" + l.id} className="lcard">
            <span className="ltile" aria-hidden="true"><Icon id="i-library" /></span>
            <div className="lmain"><div className="lname"><Link className="ttl" to={"/libraries/" + encodeURIComponent(String(l.id))}>{l.name}</Link><span className="lchev" aria-hidden="true">›</span></div>
              <span className="sm muted">{meaning}</span></div>
            <div className="lside"><span className="pill warn">{word}</span></div>
          </li>; })}
        </ul>
        <p className="muted sm">Failures from libraries you removed don't show here; they stay in the Jobs log.</p>
      </section>}
      {/* D3: Space saved and Files converted are two cards (one column under 900 px) with their 30 days as bars. A day not recorded
          is a tick, never a zero; the charts need two recorded days, and the tiles' words say so until then */}
      <section className="cards" aria-label="Totals">
        <div className="card">
          <span className="l">Space saved</span>
          <span className={"v" + (h.freed > 0 ? " ok" : "")}>{spaceHeadline(h.freed, bytes)}</span>
          <div className="d">
            {more.length > 0 && <div>{more}</div>}
            {noLibs && h.freed <= 0 && <div><Link to="/libraries/new">Add a library</Link> to start.</div>}
            {/* design SCREENS V2 Space rules: neutral, on its own line, only when > 0; never added to or taken from the figures */}
            {shrinkLine(v.averages.fileShrinkPercent) && <div>{shrinkLine(v.averages.fileShrinkPercent)}</div>}
            {(h.grew ?? 0) > 0 && <div>{bytes(h.grew!)} added by files that came out bigger</div>}
            {rec.length >= 2 && hasSaved && <div>Last 30 days: {spaceNote}</div>}
            {rec.length < 2 && <div>Charts start after two days of conversions.</div>}
          </div>
          {rec.length >= 2 && <>
            {spaceSeries.length > 1 && <ul className="legend">{spaceSeries.map((x) => <li key={x.label}><i style={{ background: x.color }} />{x.label}</li>)}</ul>}
            <CardBars label={(kept ? "Files made smaller per day" : "Space saved per day") + ", last " + v.daily.length + " days"} days={v.daily} series={spaceSeries} fmt={bytes} quiet="Nothing saved in the last 30 days."
              extra={(d) => (split && (d.waitingBytes ?? 0) > 0 && h.nextFreeAt ? "Frees from " + dayName(h.nextFreeAt.slice(0, 10)) : null)} />
            <div className="cardfoot"><span>{spaceFoot(split || kept ? tot.saved : tot.freed, bytes)}</span>
              <details><summary className="muted">Show as a table</summary><DayTable days={v.daily} cols={spaceCols} /></details></div>
          </>}
        </div>
        <div className="card">
          <span className="l">Files converted</span>
          <span className="v">{count(h.filesConverted)}<small>all time</small></span>
          <div className="d">
            {perDay != null ? <div>{count(perDay)} per day lately{s24 && <> · {last24}</>}</div> : <>
              <div>Charts start after two days of conversions.</div>
              {s24 && <div>{last24}</div>}
            </>}
            {v.averages.secondsPerGB != null && <div>About {perGB(v.averages.secondsPerGB)} of converting per GB on this computer</div>}
          </div>
          {rec.length >= 2 && <>
            <ul className="legend">{filesSeries.map((x) => <li key={x.label}><i style={{ background: x.color }} />{x.label}</li>)}</ul>
            <CardBars label={"Files converted per day, last " + v.daily.length + " days"} days={v.daily} series={filesSeries} fmt={count} quiet="No files in the last 30 days."
              extra={() => null} />
            <div className="cardfoot"><span>{filesFoot(tot.done, tot.skipped, tot.failed, count)}</span>
              <details><summary className="muted">Show as a table</summary><DayTable days={v.daily} cols={filesCols} /></details></div>
          </>}
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

      {!starting && <>
      <section className="sec" aria-labelledby="ov-j"><h2 id="ov-j">Converting now {rows.length > 0 && <span className="muted sm">{slotUsed} of {slotTotal} slots</span>} <Link className="more" to="/jobs?tab=running">All jobs</Link></h2>
        {!v.active.length ? <p className="muted">{convertingEmpty(on > 0, h.filesWaiting, v.nextScanAt, clock)}</p> : (
          // design busy-1: the Jobs running card (the M4 mini card's grammar): tile, name, the Step / Speed / Time left fields, a status pill, a bar along the bottom
          <ul className="lcards" aria-label="Converting now">{v.active.map((a0) => { const a = { ...a0, ...live[a0.jobId] }, p = pct(a.percent), w = workerOf(a.jobId), rj = runOf.get(a.jobId), st = startedAt(rj?.progress?.elapsedS, new Date()), lib = a.library ? libOf.get(a.library.id) : undefined,
                f = runFields({ stage: a.stage ?? "", percent: a.percent, workflowPercent: 0, elapsedS: 0, speed: a.speed ?? null, etaS: a.etaS, fps: a.fps ?? null, bitrateKbps: a.bitrateKbps ?? null });
              return <li key={a.jobId} className="lcard">
                <span className="ltile codec" aria-hidden="true">{rj?.codec ? <>{rj.codec}{rj.resolution && <><br />{rj.resolution}</>}</> : <Icon id="i-file" />}</span>
                <div className="lmain">
                  <div className="lname"><Link className="ttl" to={"/jobs?tab=running&job=" + encodeURIComponent(a.jobId)}>{a.name}</Link><span className="lchev" aria-hidden="true">›</span></div>
                  <div className="chips">{a.library && <span className="mchip"><Icon id="i-folder" />{a.library.name}</span>}{lib?.workflowName && <span className="mchip"><Icon id="i-workflow" />{lib.workflowName}</span>}<span className="mchip"><Icon id="i-worker" />{w ?? "This computer"}</span>{st && <span className="mchip">started {clock(st)}</span>}</div>
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
              const nextFreeDay = h.nextFreeAt ? dayName(h.nextFreeAt.slice(0, 10)) : null;
              const lib = libOf.get(String(l.id)); // the Libraries page's workflow and scan figures
              const facts = [l.saved === 0 && l.waitingToFree > 0 ? bytes(l.waitingToFree) + " smaller" + frees : null, l.saved > 0 && l.waitingToFree > 0 ? bytes(l.waitingToFree) + " more" + frees : null, (l.grew ?? 0) > 0 ? bytes(l.grew!) + " added by bigger files" : null].filter(Boolean);
              const files = l.counts.done + l.counts.waiting + l.counts.skipped + l.counts.failed, right = libraryRight(l, nextFreeDay, bytes);
              return <li key={l.id} className="lcard">
                <span className="ltile" aria-hidden="true"><Icon id="i-library" /></span>
                <div className="lmain">
                  <div className="lname"><Link className="ttl" to={"/libraries/" + l.id}>{l.name}</Link><span className="lchev" aria-hidden="true">›</span></div>
                  <div className="chips"><span className="mchip">{count(l.counts.waiting)} waiting</span><span className="mchip">{plural(files, "file", "files")}</span>{lib?.workflowName && <span className="mchip"><Icon id="i-workflow" />{lib.workflowName}</span>}{lib && <span className="mchip">{lib.scan.lastScanAt ? "scanned " + lastScan(lib.scan.lastScanAt) : "not scanned yet"}</span>}{facts.map((f) => <span key={f} className="mchip">{f}</span>)}</div>
                </div>
                <div className="lside">
                  <span className={l.saved > 0 ? "saved nowrap" : "sm nowrap"}>{right.gain && <b className="gain">{right.gain}</b>}{right.rest}</span>
                  <span className="lnote">
                    {l.counts.failed > 0 && <Link className="over" to={"/jobs?tab=failed&libraryId=" + encodeURIComponent(String(l.id))}>{count(l.counts.failed)} failed</Link>}
                    {l.couldFree > 0 && l.saved === 0 && <Link to={"/jobs?tab=completed&left=1&libraryId=" + encodeURIComponent(String(l.id))}>{"could free " + bytes(l.couldFree)}</Link>}
                    {l.state === "no_workflow" ? <span className="pill warn">No workflow</span> : l.state === "paused" ? <span className="pill warn">Paused</span> : l.state === "paused_share" ? <span className="pill warn">Share not connected yet</span> : null}
                  </span>
                </div>
                {sp != null && <div className="lbar" role="progressbar" aria-label={l.name + ": " + sp + "% saved"} aria-valuemin={0} aria-valuemax={100} aria-valuenow={sp}><i style={{ width: (share ?? 0) + "%" }} /></div>}
              </li>; })}</ul></>}
      </section>



      {/* the 30-day block is a closed disclosure at every width (design 1440 and 390) */}
        {first && rec.length >= 2 && v.daily.length > 0 && first > v.daily[0].day && <p className="muted sm">Recorded from {dayName(first)}.</p>}
        {rec.length >= 2 && !(MOCK || /^(Etc\/)?UTC$/.test(v.timeZone)) && <p className="muted sm">Days run midnight to midnight, {v.timeZone.split("/").pop()!.replace(/_/g, " ")} time.</p>}
      </>}
    </div>
  );
}
