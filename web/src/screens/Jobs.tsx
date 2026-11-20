// V6 Jobs (SCREENS V6; DESIGN/v2 mockups/jobs.html, jobs-drawer.html, jobs-drawer-done.html) on the real /api/v2/jobs
// (#113) with live updates from /api/v2/events (#118/#120/#122), or the sample jobs with ?mock=1. The server lists one
// tab at a time (Waiting, Running, Done, Skipped, Failed); All and Paused tabs and the codec/step/worker filters come
// when it has them. Not here yet: the output path (v2 sends it in convertAgain.outputPath; the drawer does not show it).
import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, clock, count, elapsed, plural, stepTime } from "../format";
import { convertDoneAgain, fileJobs, findSkip, getJob, getJobLog, jobAction, listJobs, listKept, restoreKept, type JobLog, type LogStep, type LogVariable } from "../jobsApi";
import { getWorkflow } from "../workflowApi";
import { getHealth, getSettings } from "../libraryApi";
import { onLive } from "../live";
import { FileName } from "../FileName";
import { listLibraries, listLibrariesV2 } from "../libraryApi";
import { Empty, LoadFailed, PageHead } from "../PageHead";
import { Link, navigate, useSearch } from "../router";
import { listWorkers, noWorker } from "../workersApi";
import { useData } from "../useData";
import type { Failure, JobAction, JobDetail, JobItem, JobProgressEvent, JobState, Probe, Tab, Pin } from "../types/jobs";

const TABS: [Tab, string][] = [["waiting", "Waiting"], ["running", "Running"], ["completed", "Done"], ["skipped", "Skipped"], ["failed", "Failed"]];
// G5: the Done filter for originals waiting in the trash. The words come from design (SCREENS V6); keep them here.
const TRASH_WORDS = "Original in the trash";
const TRASH_EMPTY = "No files with their original in the trash.";
const LEFT_WORDS = "Original left in place"; // #377 ?left=1; the empty line is a placeholder until design confirms it
const LEFT_EMPTY = "No files with their original left in place.";
// Spec states in words (SCREENS V6); a cancelled file lists under Skipped and reads "Cancelled".
const tabOf = (st: JobState): Tab => (st === "queued" || st === "retry_wait" ? "waiting" : st === "completed" ? "completed" : st === "skipped" || st === "cancelled" ? "skipped" : st === "failed" ? "failed" : "running");
const WORD: Record<string, string> = { queued: "Waiting", preparing: "Preparing", running: "Running", validating: "Checking", committing: "Saving",
  completed: "Done", skipped: "Skipped", failed: "Failed", cancelled: "Cancelled", retry_wait: "Retry", done: "Done", kept: "Original kept: the new file wasn't used." };
const PILL: Partial<Record<JobState, string>> = { preparing: "run", running: "run", validating: "run", committing: "run", completed: "ok", failed: "bad", retry_wait: "warn" };
const ICON: Partial<Record<JobState, string>> = { completed: "i-check-c", failed: "i-x-c", skipped: "i-skip", cancelled: "i-stop", retry_wait: "i-clock", queued: "i-clock" };
// V5b: a waiting file whose library is paused (subState library_disabled) reads "Library paused" (design SCREENS V5);
// it has no place in the queue, so its '#' is '–'.
type PillJob = { state: JobState; retryAt?: string | null; subState?: string | null; reason?: { code: string } | null; warning?: string | null };
// steps that warn without failing the job: the processor fallback (#323) and a media app (Sonarr, Radarr, Plex, Jellyfin) that wasn't told (#333)
const SOFT = ["encoder_fallback", "media_refresh"];
// the top note, one line per warning: the server's warning names only the first (#333), so the steps give the rest
const warnLines = (j: JobDetail) => {
  const out = j.steps.some((s) => s.step === "encoder_fallback") ? ["The graphics encoder failed, so this file was converted on the processor, which is slower. The timeline below has the error."] : [];
  out.push(...j.steps.filter((s) => s.step === "media_refresh" && !s.ok).map((s) => s.words));
  return out.length ? out : [j.warning ?? ""];
};
const fell = (j: PillJob) => j.state === "completed" && !!j.warning; // #323: done on the processor after the graphics encoder failed
const libPaused = (j: PillJob) => j.state === "queued" && j.subState === "library_disabled";
// de2f664: a check that refused the new file kept the original; the server lists it as failed (code kept_original), it reads "Original kept"
const keptOrig = (j: PillJob) => j.state === "failed" && j.reason?.code === "kept_original";
const words = (j: PillJob) => (libPaused(j) ? "Library paused" : keptOrig(j) ? "Original kept" : fell(j) ? "Done, with a warning" : j.state === "retry_wait" && j.retryAt ? "Retry at " + clock(j.retryAt) : WORD[j.state]);
function Pill({ j }: { j: PillJob }) {
  return <span className={"pill " + (libPaused(j) || keptOrig(j) || fell(j) ? "warn" : PILL[j.state] ?? "")}><Icon id={libPaused(j) ? "i-pause" : keptOrig(j) ? "i-info" : fell(j) ? "i-alert" : ICON[j.state] ?? "i-play"} />{words(j)}</span>;
}
const reasonText = (j: JobItem) => j.reason?.message ?? j.detail ?? ""; // #130: every row's reason is {code, message}
const saved = (b?: number, a?: number) => {
  if (!b || a == null) return "";
  const pct = Math.round((100 * (b - a)) / b);
  return bytes(b) + " → " + bytes(a) + ", " + (pct >= 0 ? pct + "% smaller" : -pct + "% bigger");
};
async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}

function Detail({ j }: { j: JobItem }) {
  if (j.progress) {
    const p = j.progress, pct = p.percent ?? p.workflowPercent;
    return <div className="prog"><div className="row nw"><div className="bar" role="progressbar" aria-label={p.stage} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}><i style={{ width: pct + "%" }} /></div><span className="sm">{Math.round(pct)}%</span></div>
      <span className="sm muted">{[p.stage, p.fps != null && p.fps + " fps", elapsed(p.elapsedS), p.etaS ? "about " + elapsed(p.etaS) + " left" : ""].filter(Boolean).join(" · ")}</span></div>;
  }
  if (j.state === "completed") return <span className="sm">{saved(j.beforeBytes, j.afterBytes)}{j.originalState === "kept" && j.keptDaysLeft != null && <span className="muted"> · {j.keptDaysLeft > 0 ? "original in the trash " + plural(j.keptDaysLeft, "more day", "more days") : "original in the trash, deleted today"}</span>}</span>;
  return <span className="sm muted clamp2" title={reasonText(j)}>{reasonText(j)}</span>;
}

const RUNS: JobState[] = ["preparing", "running", "validating", "committing"]; // live progress only for a running row
// The drawer opens by job: a skipped row with a job behind it (#149: a workflow's skip after it failed, or one the user
// stopped) opens that job (steps, tries, log); a file a scan left out has no job and opens its row ("skip-N").
const key = (j: JobItem) => j.jobId ?? j.id;

export function Jobs() {
  const search = new URLSearchParams(useSearch());
  const tab = (TABS.find(([t]) => t === search.get("tab"))?.[0] ?? "waiting") as Tab, open = search.get("job"), library = search.get("libraryId") ?? "";
  // G5: ?kept=1 is Done-only (the server answers 400 on other tabs), so it reads as off on any other tab.
  // left (?left=1) is the same, and only one of the two is on at a time: the server answers 400 for both together
  const kept = tab === "completed" && search.get("kept") === "1", left = tab === "completed" && !kept && search.get("left") === "1";
  const query = (t: Tab, lib: string, f: "kept" | "left" | "" = kept ? "kept" : left ? "left" : "") => "/jobs?tab=" + t + (lib ? "&libraryId=" + encodeURIComponent(lib) : "") + (f && t === "completed" ? "&" + f + "=1" : "");
  // "See its jobs" (a library page) filters every tab to that library; the library's name shows as a chip that clears it
  const libs = useData(listLibraries, [], 60000);
  // parity item 8: with every worker stopped nothing here moves, so say so (a worker finishing its file still runs)
  const wk = useData(listWorkers, [], 15000), wl = wk.data && !failed(wk.data) ? wk.data : [];
  const off = noWorker(wl);
  const [q, setQ] = useState(""), [selecting, setSelecting] = useState(false), [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState(""), [prio, setPrio] = useState<Pin | "none">("first");
  // each answer remembers its tab and search, so a tab switch never shows the last tab's rows while it loads
  // Show more (parity 11-19 #1): 200 rows at first, 200 more per press; a new tab, search or library starts again at 200.
  // The rows already shown stay while more load (the answer's "for" leaves the count out)
  const scope = tab + "|" + q + "|" + library + (kept ? "|kept" : left ? "|left" : "");
  const [more, setMore] = useState({ for: "", n: 200 }), lim = more.for === scope ? more.n : 200;
  if (more.for && more.for !== scope) setMore({ for: "", n: 200 }); // forget it on leaving, so coming back is 200 too (review 29156ce P3)
  const page = useData(() => listJobs(tab, q, library, lim, kept, left).then((r) => (failed(r) ? r : { ...r, for: scope })), [tab, q, library, lim, kept, left], 15000);
  const [live, setLive] = useState<Record<string, JobProgressEvent["progress"]>>({});
  const reload = useRef(page.reload); reload.current = page.reload;
  const shown = useRef<{ tab: Tab; rows: Map<string, JobState> }>({ tab, rows: new Map() });
  // live: a state change reloads the list (and counts) shortly after; progress updates its row in place. The list can
  // be older than the stream (dev-qa V6 b: a job that started between loading the list and the stream opening): so the
  // stream's hello reloads it, and progress for a job the list doesn't show as converting (still Waiting there, or
  // missing from Running) reloads it too, at most every 3 s.
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined, lastHeal = 0;
    const soon = () => { clearTimeout(t); t = setTimeout(() => reload.current(), 400); };
    return onLive((type, data) => {
      if (type === "job.progress") {
        const e = data as JobProgressEvent, sh = shown.current, st = sh.rows.get(e.jobId);
        setLive((m) => ({ ...m, [e.jobId]: e.progress }));
        const stale = st ? !RUNS.includes(st) : sh.tab === "running";
        if (stale && Date.now() - lastHeal > 3000) { lastHeal = Date.now(); soon(); }
        return;
      }
      if (type === "job.state") { const id = (data as { jobId?: string })?.jobId; if (id) setLive((m) => { const n = { ...m }; delete n[id]; return n; }); }
      if (type === "job.state" || type === "resync" || type === "hello" || type === "library.changed") soon(); // #150: a paused library shows at once
    });
  }, []);
  useEffect(() => setSel([]), [tab]);
  const go = (t: Tab, job?: string | null) => navigate(query(t, library) +(job ? "&job=" + encodeURIComponent(job) : ""));
  const current = page.data?.for === scope ? page.data : undefined;
  const noLibs = !!libs.data && libs.data.length === 0; // first run: no library yet, so no job can come
  const items = (current?.items ?? []).map((j) => (live[j.id] && RUNS.includes(j.state) ? { ...j, progress: live[j.id] } : j)), counts = page.data?.counts;
  const firstRun = noLibs && !kept && !left && !!counts && TABS.every(([t]) => !counts[t]); // every tab empty: no bulk actions to offer
  shown.current = { tab, rows: new Map((current?.items ?? []).map((j) => [j.jobId ?? j.id, j.state])) };
  const picked = items.filter((j) => sel.includes(j.id)), none = !picked.length;
  const run = async (a: JobAction) => {
    if (a === "cancel" && !window.confirm("Cancel " + plural(picked.length, "file", "files") + "? The files stay as they are. Nothing is deleted.")) return;
    const r = await act(jobAction(a, picked.map((j) => j.id), a === "reprioritize" ? prio : undefined), setSaid);
    if (!r) return;
    const bad = r.results.filter((x) => !x.ok), n = r.results.length - bad.length;
    // design (SCREENS V6): say where the files went, not just that they changed
    const where = a === "reprioritize" ? (prio === "first" ? " moved to the front of the queue." : prio === "last" ? " moved to the back of the queue." : " put back in their normal place in the queue.") : " changed.";
    // review f1aaf30 P3: Put back in queue says what the drawer says. A skipped file (skip-N, with or without a job behind
    // it) goes through convert-again (jobsApi): the next scan looks at it; a job goes back in the queue at once.
    const okIds = r.results.filter((x) => x.ok).map((x) => x.id), skipsOk = okIds.filter((i) => i.startsWith("skip-")).length, jobsOk = okIds.length - skipsOk;
    const said = a !== "requeue" ? plural(n, "file", "files") + where
      : [skipsOk && plural(skipsOk, "file", "files") + " will be looked at again on the next scan.", jobsOk && plural(jobsOk, "file", "files") + " put back in the queue."].filter(Boolean).join(" ") || "0 files changed.";
    setSaid(said + (bad.length ? " Not changed: " + bad.map((x) => (items.find((j) => j.id === x.id)?.name ?? x.id) + ": " + x.message).join(" ") : ""));
    setSel([]); page.reload();
  };
  const actions = <>
    {/* one word per action (pass 5): Retry only on the Failed tab; a skip is Convert again */}
    {tab === "failed" && <button className="btn sm ghost" type="button" disabled={none} title={none ? "Select files first" : undefined} onClick={() => run("retry")}><Icon id="i-refresh" />Retry</button>}
    {tab === "skipped" && <button className="btn sm ghost" type="button" disabled={none} title={none ? "Select files first" : undefined} onClick={() => run("requeue")}><Icon id="i-arrow-r" />Convert again</button>}
    <button className="btn sm ghost" type="button" disabled={none} title={none ? "Select files first" : undefined} onClick={() => run("cancel")}><Icon id="i-stop" />Cancel</button>
    {tab === "waiting" && <span className="row nw" style={{ gap: 4 }}>
      <select className="input sm" aria-label="Priority" value={prio} onChange={(e) => setPrio(e.target.value as Pin | "none")}><option value="first">First</option><option value="none">Normal</option><option value="last">Last</option></select>
      <button className="btn sm ghost" type="button" disabled={none} onClick={() => run("reprioritize")}>Change priority</button></span>}
  </>;
  return (
    <div className="stack">
      <PageHead icon="i-jobs" title="Jobs" help="Every file the workers look at: waiting, converting, done, skipped and failed." />
      {off && <p className="st warn" role="status"><Icon id="i-alert" /><span>{off === "offline"
        ? <>No worker is connected. <Link to="/workers">See Workers.</Link></>
        : <>No worker is running. <Link to="/workers">Turn workers on.</Link></>}</span></p>}
      {library && <div className="row" style={{ gap: 6 }}><span className="mchip">Library: {libs.data?.find((l) => l.id === library)?.name ?? library}
        <button className="btn sm icon ghost" type="button" aria-label="Clear the library filter" onClick={() => navigate(query(tab, ""))}><Icon id="i-x" /></button></span></div>}
      {kept && <div className="row" style={{ gap: 6 }}><span className="mchip">{TRASH_WORDS}
        <button className="btn sm icon ghost" type="button" aria-label="Clear the original-in-the-trash filter" onClick={() => navigate(query(tab, library, ""))}><Icon id="i-x" /></button></span></div>}
      {left && <div className="row" style={{ gap: 6 }}><span className="mchip">{LEFT_WORDS}
        <button className="btn sm icon ghost" type="button" aria-label="Clear the original-left-in-place filter" onClick={() => navigate(query(tab, library, ""))}><Icon id="i-x" /></button></span></div>}
      <div className="tabs" role="tablist" aria-label="Jobs">
        {TABS.map(([t, label]) => <button key={t} role="tab" type="button" aria-selected={tab === t} onClick={() => tab !== t && go(t)}>{label}{counts && <span className="n">{count(t === "completed" && (kept || left) && current ? current.total : counts[t])}</span>}</button>)}
      </div>
      <div className="ltools" role="toolbar" aria-label="Jobs">
        {!firstRun && <>
          <span className="desk">{actions}</span>
          <button className="btn sm ghost phone" type="button" aria-pressed={selecting} onClick={() => (setSelecting(!selecting), setSel([]))}>{selecting ? "Done" : "Select"}</button>
          <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{actions}</div></details>
        </>}
        {sel.length > 0 && <span className="muted sm">{sel.length} selected</span>}
        {/* parity 11-19 #2 (G4): pick a library here too; it sets the same ?libraryId= as "See its jobs", and the chip above stays */}
        <span className="filt">{tab === "completed" && <button className="btn sm trash" type="button" aria-pressed={kept} onClick={() => navigate(query("completed", library, kept ? "" : "kept"))}>{TRASH_WORDS}</button>}{tab === "completed" && <button className="btn sm left-in-place" type="button" aria-pressed={left} onClick={() => navigate(query("completed", library, left ? "" : "left"))}>{LEFT_WORDS}</button>}
        {(libs.data?.length ?? 0) > 1 && <select className="input sm lib" aria-label="Library" value={library} onChange={(e) => navigate(query(tab, e.target.value))}>
          <option value="">Library: All</option>
          {libs.data?.map((l) => <option key={l.id} value={l.id}>Library: {l.name}</option>)}
          {library && !libs.data?.some((l) => l.id === library) && <option value={library}>Library: {library}</option>}
        </select>}</span>
        <input className="input find" type="search" placeholder="Search file names" aria-label="Search file names" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <p className="muted" role="status">{said}</p>
      {page.error && current && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{page.error.message}</span></p>}
      {!current ? (page.error ? <LoadFailed error={page.error} reload={page.reload} /> : <div className="empty">Loading…</div>) : !items.length ? (q ? <div className="empty">No file matches.</div>
        : firstRun ? <Empty icon="i-jobs" text="No jobs yet. Jobs start when a library finds videos."><Link className="btn primary" to="/libraries/new"><Icon id="i-plus" />Add library</Link></Empty>
        : <Empty icon="i-jobs" text={kept ? TRASH_EMPTY : left ? LEFT_EMPTY : { waiting: "Nothing is waiting.", running: "Nothing is converting right now.", completed: "No file is done yet.", skipped: "No file was skipped.", failed: "Nothing has failed." }[tab]} />) : (
        <div className={"list" + (selecting ? " selecting" : "")}><table className="tbl jobs">
          <thead><tr>
            <th className="selc"><label className="hit"><input type="checkbox" aria-label="Select all" checked={sel.length === items.length} onChange={(e) => setSel(e.target.checked ? items.map((j) => j.id) : [])} /></label></th>
            {tab === "waiting" && <th className="posc" title="Place in the queue: 1 is converted next">#</th>}
            <th>File</th><th className="stc">Status</th><th data-p="2">Details</th><th data-p="3">Worker</th><th data-p="4">{tab === "completed" ? "Finished" : "Size"}</th>
          </tr></thead>
          <tbody>{items.map((j) => (
            <tr key={j.id} className={sel.includes(j.id) ? "sel" : undefined}>
              <td className="selc"><label className="hit"><input type="checkbox" aria-label={"Select " + j.name} checked={sel.includes(j.id)} onChange={(e) => setSel(e.target.checked ? [...sel, j.id] : sel.filter((x) => x !== j.id))} /></label></td>
              {tab === "waiting" && <td className="posc" title={j.position != null ? "Place " + j.position + " in the queue" : "Not taken now"}>{j.position ?? "—"}</td>}
              <td className="idc"><a className="ttl" href={"/jobs?tab=" + tab + "&job=" + encodeURIComponent(key(j))} data-job={key(j)} title={j.path}
                onClick={(e) => { if (e.button === 0 && !e.metaKey && !e.ctrlKey) { e.preventDefault(); go(tab, key(j)); } }}><FileName n={j.name} /></a>
                {j.pin && <span className="mchip prio">{j.pin === "first" ? "First" : "Last"}</span>}
                <span className="sub">{[j.library, j.folder !== "." && j.folder, [j.codec, j.resolution].filter(Boolean).join(" ")].filter(Boolean).join(" · ")}</span>
                <span className="sub phone">{j.progress ? Math.round(j.progress.percent ?? j.progress.workflowPercent) + "% · " + j.progress.stage : j.state === "completed" ? saved(j.beforeBytes, j.afterBytes) : reasonText(j) || j.library}</span></td>
              <td className="stc"><Pill j={j} /></td>
              <td data-p="2"><Detail j={j} /></td>
              <td data-p="3" className="sm">{j.worker === "builtin" ? "This computer" : j.worker ?? ""}</td>
              <td data-p="4" className="sm nowrap">{tab === "completed" && j.finishedAt ? clock(j.finishedAt) : bytes(j.bytes)}</td>
            </tr>))}
          </tbody>
        </table></div>)}
      {current && current.total > items.length && <p className="row muted sm" style={{ gap: 8, justifyContent: "center" }}>Showing {items.length.toLocaleString()} of {current.total.toLocaleString()}
        <button className="btn sm" type="button" disabled={items.length < Math.min(lim, current.total)} onClick={() => setMore({ for: scope, n: lim + 200 })}>Show {Math.min(200, current.total - items.length)} more</button></p>}
      {open && <Drawer id={open} row={items.find((j) => key(j) === open)} onClose={() => go(tab)} onChanged={page.reload} />}
    </div>
  );
}

// The job's log (SCREENS V6: streaming, searchable, Download): GET /api/v2/jobs/{id}/log/stream (#131). Each piece
// comes as job.log {text, next} with next as its id, so the browser's own reconnect resumes where it stopped; "end"
// comes once the job is over and the whole log has been sent.
// E (SCREENS V6b 'Why it failed'): the sentence, where it stopped, the server's hint when it has one, and the last
// ffmpeg error lines (at most five) behind a disclosure. Each part is left out when the server sends none.
const text = (o: string | string[]) => Array.isArray(o) ? o.join("\n") : o;
// map server step labels to person-facing names (workshop #217: encode, check)
// The check step is named by what it checked (its found Check line); an older log without one is "Checking the new file".
const stepLabel = (s: string, found?: string[], scored = false) => s === "check" ? checkLabel(found, scored) : ({ encode: "Converting video", imported: "Older log, not split into steps", replace: "Save the result", audio: "Audio", filter: "Filter" } as Record<string, string>)[s] ?? s;
const checkLabel = (found?: string[], scored = false) => {
  const c = (found ?? []).find((l) => l.startsWith("Check: ")) ?? "";
  return /^Check: picture/.test(c) ? "Checking the picture" : /^Check: playback/.test(c) ? "Checking playback" : /^Check: damage/.test(c) ? "Checking the original for damage" : scored ? "Checking the picture" : "Checking the new file"; // a remote worker records the picture score with no Check: line
};
// SCREENS V6b: the sentence, the reason code small under it, where it stops (the step opens in the log), the hint as
// 'What you can do' (left out when null), the last ffmpeg lines with Copy, and the buttons. --error left border.
// SCREENS V6c What the check found: a check that kept the original or failed the job writes "Label: value" lines in its
// step's found (worker verify.go, worker.go). They become design's rows (a row with no data is left out); libva lines are
// driver messages; every other line is ffmpeg's output. A value this page can't read is shown as written.
const cap = (v: string) => v.charAt(0).toUpperCase() + v.slice(1);
export function readFound(lines: string[]) {
  const rows: [string, string][] = [], errors: string[] = [], driver: string[] = [];
  const add = (k: string, v?: string | null) => { if (v) rows.push([k, v]); };
  for (const l of lines) {
    const m = /^(Check|Scores|Alignment|Worst frames|Where|Errors|Encoder|Original|New file): (.+)$/.exec(l);
    if (!m) { (/^libva\b/i.test(l) ? driver : errors).push(l); continue; }
    const [, k, v] = m;
    if (k === "Check") add("Check", cap(v.replace(/( at \d+ places?, \d+ s each): .*$/, "$1")));
    else if (k === "Alignment") { const c = rows.find((r) => r[0] === "Check"); const t = "Also tried one frame earlier and later."; if (c) c[1] += "\n" + t; else add("Check", t); }
    else if (k === "Scores") { // "0:01:00 0.912, 0:12:00 0.961 (0.95 required); 0:30:00 not measured"
      const need = /\(([\d.]+) required\)/.exec(v)?.[1], at = v.replace(/ \([\d.]+ required\)/, "").split(/[;,] /).map((x) => x.trim()).filter(Boolean);
      const low = at.map((x) => /^\S+ (\d+(?:\.\d+)?)$/.exec(x)?.[1]).filter((x): x is string => !!x).sort((a, b) => Number(a) - Number(b))[0];
      add("Score", low ? "Lowest " + low + (need ? " · needs " + need : "") : null);
      add("At each place", at.join(" · "));
    }
    else if (k === "Worst frames") add("Worst frame", cap(v.split(", ")[0])); // the server sorts them, worst first
    else if (k === "Where") add("Stopped at", /reached frame (\d+)/.exec(v) ? "Frame " + /reached frame (\d+)/.exec(v)![1] : null);
    else if (k === "Errors") add("Errors", cap(v.replace("error lines from ffmpeg", "lines from ffmpeg")));
    else add(k, v);
  }
  return { rows, errors, driver };
}
const copyBox = (title: string, ls: string[]) => ls.length > 0 && <div className="stack" style={{ gap: 4 }}><div className="row" style={{ justifyContent: "space-between" }}><span className="sm">{title}</span>
  <button className="btn sm ghost" type="button" onClick={() => navigator.clipboard?.writeText(ls.join("\n")).catch(() => {})}><Icon id="i-copy" />Copy</button></div><pre className="joblog sm">{ls.join("\n")}</pre></div>;
const FoundRows = ({ rows }: { rows: [string, string][] }) => rows.length > 0 ? <dl className="kv">{rows.map(([k, v], i) => <Fragment key={i}><dt>{k}</dt><dd className="sm" style={{ whiteSpace: "pre-line", fontVariantNumeric: "tabular-nums" }}>{v}</dd></Fragment>)}</dl> : null;
const Driver = ({ ls }: { ls: string[] }) => ls.length > 0 ? <details><summary className="sm" style={{ cursor: "pointer" }}>Driver messages ({ls.length})</summary>{copyBox("Driver messages", ls)}</details> : null;
// A kept job: its own neutral section (keeping the original is the safe outcome, not an error).
function CheckFound({ lines, reason }: { lines: string[]; reason: string }) {
  const f = readFound(lines);
  return (
    <section className="note" role="region" aria-label="What the check found" style={{ display: "block" }}>
      <h3><Icon id="i-info" /> What the check found</h3>
      {reason && <p>{reason.charAt(0).toUpperCase() + reason.slice(1)}</p>}
      <FoundRows rows={f.rows} />
      {copyBox("What ffmpeg said", f.errors)}
      <Driver ls={f.driver} />
    </section>);
}

function WhyFailed({ id, msg, code, worker, f, found = [] }: { id: string; msg: string; code: string; worker: string | null; f: Failure; found?: string[] }) {
  const fd = readFound(found), ff = fd.errors.length ? fd.errors : f.keyLines; // V6c: one ffmpeg box
  const t = stepTime(f.afterMs), box = useRef<HTMLElement>(null), ver = useData(getHealth, []).data?.version;
  // Copy summary: the plain text a bug report needs (V6b item 5): the job, its worker and the app version, then the box
  const copy = () => navigator.clipboard?.writeText("Job " + id + " · " + (worker ?? "no worker") + " · Shrink Your Shit " + (ver ?? "") + "\n" + (box.current?.innerText ?? "")).catch(() => {});
  return (
    <section ref={box} className="note" role="region" aria-label="Why it failed" style={{ display: "block", borderLeft: "3px solid var(--error)" }}>
      <h3><Icon id="i-alert" /> Why it failed <span className="pill bad">Failed</span></h3>
      <p style={{ fontSize: "var(--fs-lg)" }}>{msg}</p>
      <p className="sm muted mono">{code}</p>
      <p className="sm">In step {f.stepIndex + 1} of {f.stepCount}, <a href={"#job-step-" + (f.stepIndex + 1)}>{stepLabel(f.step, found)}</a>, after {t}{f.mediaTime ? " (at " + f.mediaTime + " in the video)" : ""}.</p>
      <FoundRows rows={fd.rows} />
      {f.hint && <p><strong>What you can do:</strong> {f.hint}</p>}
      {f.ffmpegRan === false
        ? <p className="sm muted">ffmpeg didn't run.</p>
        : ff.length > 0 && <div className="stack" style={{ gap: 4 }}><div className="row" style={{ justifyContent: "space-between" }}><span className="sm">{(f.step === "encode" || f.step === "check" ? "What ffmpeg said" : "The error")}</span><button className="btn sm ghost" type="button" onClick={() => navigator.clipboard?.writeText(ff.join("\n")).catch(() => {})}><Icon id="i-copy" />Copy</button></div>
          <pre className="joblog">{ff.join("\n")}</pre></div>}
      <Driver ls={fd.driver} />
      <div className="row" style={{ gap: 8, flexWrap: "wrap", marginTop: 12 }}>
        <a className="btn sm" href={"#job-step-" + (f.stepIndex + 1)}><Icon id="i-file" />Open the full log</a>
        {!MOCK && <a className="btn sm ghost" href={"/api/v2/jobs/" + encodeURIComponent(id) + "/log/download"}><Icon id="i-download" />Download</a>}
        <button className="btn sm ghost" type="button" onClick={copy}><Icon id="i-copy" />Copy summary</button>
      </div>
    </section>);
}

// G6: copy the whole log, not just the search hits (spec: "Copy the whole log"); over about 1 MB it is too long for
// the clipboard. Plain http (http://IP:8484) has no clipboard API, so as in Workers the text is selected in a
// textarea and copied the old way.
const copyLogText = async (t: string, say: (s: string) => void) => {
  if (t.length > 1_000_000) return say("Too long to copy; use Download.");
  let ok = false;
  try { if (navigator.clipboard) { await navigator.clipboard.writeText(t); ok = true; } } catch { /* refused: fall back */ }
  if (!ok) {
    const a = document.createElement("textarea"); a.value = t; a.readOnly = true; a.style.position = "fixed"; a.style.opacity = "0";
    document.body.appendChild(a); a.select(); try { ok = document.execCommand("copy"); } catch { ok = false; } a.remove();
  }
  say(ok ? "Log copied." : "Couldn't copy; use Download.");
};
// count of q in a string (case-insensitive); a step's searchable text: its name, decision, reason, command, output,
// check lines, result, warnings, errors and the variables it set
const countIn = (s: string, q: string) => s.split(q.toLowerCase()).length - 1;
const stepText = (st: LogStep) => [st.name, st.decision, st.reason, st.command, text(st.output), (st.found ?? []).join("\n"), st.result, ...(st.warnings ?? []), ...(st.errors ?? []), ...(st.set ?? []).map((x) => x.name + " " + (x.value ?? ""))].filter(Boolean).join("\n").toLowerCase();
const plural1 = (n: number, one: string, many: string) => n + " " + (n === 1 ? one : many);
const val = (s?: string) => (s ? s : "—");
const MOVE: Record<string, string> = { move: "Moved", copy: "Copied", chmod: "Changed the permissions of", chown: "Changed the owner of", remove: "Removed", write_sidecar: "Wrote" };
// the probe summary V6b uses, as one line: "H.264 · 1920×1080 · 23.976 fps · 2 audio · 1:48:12 · 4.0 GB"
function sourceLine(pr: Probe) {
  const v = pr.streams?.find((x) => x.type === "video"), n = (t: string) => (pr.streams ?? []).filter((x) => x.type === t).length;
  const hdr = v?.dolbyVision ? "Dolby Vision" : v?.colorTransfer === "smpte2084" ? "HDR10" : v?.colorTransfer === "arib-std-b67" ? "HLG" : "";
  return [pr.videoCodec?.toUpperCase().replace("H264", "H.264"), v?.Width && v.Height ? v.Width + "×" + v.Height : "", v?.frameRate ? +v.frameRate.toFixed(3) + " fps" : "", [v?.bitDepth ? v.bitDepth + "-bit" : "", hdr].filter(Boolean).join(" "), n("audio") && n("audio") + " audio", n("subtitle") && n("subtitle") + " subtitles", pr.durationSec ? elapsed(pr.durationSec) : "", pr.sizeBytes ? bytes(pr.sizeBytes) : ""].filter(Boolean).join(" · ");
}
// every stream on its own line: "Audio 1 · eng · aac · 2 ch"
const streamLines = (pr: Probe) => (pr.streams ?? []).map((x, j, all) => [x.type.charAt(0).toUpperCase() + x.type.slice(1) + " " + all.slice(0, j + 1).filter((y) => y.type === x.type).length, x.language ?? "—", x.codec, x.channels ? x.channels + " ch" : ""].filter(Boolean).join(" · "));
// a variable's value: "Hidden" for a secret; one value when nothing changed it; else the start, the end and the step that set it
function varValue(v: LogVariable) {
  if (v.secret) return <span className="muted">Hidden</span>;
  if (v.changedBy == null) return <><span className="sr">At start and at end: </span>{val(v.end ?? v.start)}</>;
  return <><span className="pill">Changed</span> <span className="sr">At start: </span>{val(v.start)} <span aria-hidden="true">→</span> <span className="sr">At end: </span><strong>{val(v.end)}</strong><br /><span className="muted">Set by {stepLabel(v.changedBy)}</span></>;
}
type Raw = { key: string; id?: string; title: string; head: ReactNode; hits: number; dflt: boolean; show: boolean; body: ReactNode };
// E: a finished job's log (GET /log), in sections: Start, Variables, Source file, one section per step, Summary. Open by the
// design's rules (a failed or skipped step, the picture check when the original was kept, the Summary); search opens the
// sections with a match; "Errors and warnings only" shows just the flagged steps. A job from before full logs had no
// start, variables or summary: only its steps show, with one line saying so. Running jobs use JobLog.
function StepLog({ d, id, worker, failedIdx, kept, hasResult }: { d: { loaded: boolean; data?: JobLog; error?: { message: string } }; id: string; worker: string | null; failedIdx: number | null; kept: boolean; hasResult: boolean }) {
  const keep = useData(getSettings, []), [q, setQ] = useState(""), [copied, setCopied] = useState(""), [errOnly, setErrOnly] = useState(false), [only, setOnly] = useState(false), [open, setOpen] = useState<Record<string, boolean>>({});
  // qOpen: clicks made during the current search; they win while it lasts and are dropped when the query changes
  const [qOpen, setQOpen] = useState<Record<string, boolean>>({});
  useEffect(() => { setCopied(""); setOpen({}); }, [id]);
  useEffect(() => setQOpen({}), [q, id]);
  // a finished job's whole log is on the server (the steps here keep only the last 200 lines of a failure)
  const copyAll = () => MOCK ? copyLogText((d.data?.steps ?? []).map((st) => [st.name, st.command, text(st.output), st.result].filter(Boolean).join("\n")).join("\n"), setCopied)
    : fetch("/api/v2/jobs/" + encodeURIComponent(id) + "/log/download", { credentials: "same-origin" }).then((r) => (r.ok ? r.text() : Promise.reject())).then((t) => copyLogText(t, setCopied), () => setCopied("Couldn't copy; use Download."));
  if (!d.loaded) return <section><h3>Log</h3><p className="muted">Loading the log…</p></section>;
  if (!d.data) return <section><h3>Log</h3><p className="muted">{d.error?.message ?? "The log can't be shown."}</p></section>;
  const l = d.data, days = (keep.data as { logKeepDays?: number } | undefined)?.logKeepDays ?? 30;
  // SCREENS V6b 'States': a log that isn't all here says why, in the design's words
  const who = worker ?? "The worker";
  const note: Record<string, string | null> = {
    complete: null, coming_back: "The log from " + who + " is still coming back.", lost: who + " went offline before sending its log. The summary above is what we know.",
    none: "No log was kept for this job. Jobs that ran before job logs were added don't have one.", expired: "This job's log was deleted after " + days + " days (Settings > Processing).",
  };
  // an empty steps array (whatever the state) means the job ran before logs existed: same message as none
  if (!l.steps.length) return <section id="job-log"><h3>Log</h3><p className="sm muted">{note.none}</p></section>;
  if (note[l.state]) return <section id="job-log"><h3>Log</h3><p className="sm muted">{note[l.state]}</p></section>;
  const old = l.start == null, when = l.steps.find((s) => s.startedAt)?.startedAt ?? null, pr = l.steps.find((s) => s.probe)?.probe ?? null;
  const vars = [...(l.variables ?? [])].sort((a, b) => a.name.localeCompare(b.name)), changed = vars.filter((v) => v.changedBy != null);
  const flagged = (st: LogStep) => (st.warnings?.length ?? 0) + (st.errors?.length ?? 0) > 0;
  const raw: Raw[] = [];
  if (!old) {
    const s = l.start!;
    raw.push({ key: "start", title: "Start", head: when ? <span className="sm muted">{clock(when)}</span> : null, hits: q ? countIn([s.reason, s.workflow.name, worker ?? ""].join("\n").toLowerCase(), q) : 0, dflt: false, show: !errOnly,
      body: <><p className="sm">{s.reason}</p><p className="sm">Worker: {worker ?? "—"}</p><p className="sm">Workflow: {s.workflow.name}, revision {s.workflow.revision}</p><p className="sm">App version {s.appVersion}</p></> });
    raw.push({ key: "variables", title: "Variables", head: changed.length ? <span className="sm muted">{changed.length} changed</span> : null,
      hits: q ? countIn(vars.map((v) => [v.name, v.start, v.end].join(" ")).join("\n").toLowerCase(), q) : 0, dflt: false, show: !errOnly,
      body: vars.length === 0 ? <p className="sm muted">No variables were set for this job.</p> : <>
        <label className="sw"><input type="checkbox" role="switch" className="switch" checked={only} onChange={(e) => setOnly(e.target.checked)} /><span className="sm">Only changed ({changed.length})</span></label>
        {(only ? changed : vars).map((v) => <dl key={v.name} className="row" style={{ gap: 8, flexWrap: "wrap", borderTop: "1px solid var(--border)", padding: "6px 0", margin: 0 }}>
          <dt style={{ flex: "1 1 160px", minWidth: 0, overflowWrap: "anywhere" }}><code className="sm">{"{{" + v.name + "}}"}</code></dt>
          <dd className="sm" style={{ flex: "1 1 200px", minWidth: 0, margin: 0 }}>{varValue(v)}</dd></dl>)}</> });
    if (pr) raw.push({ key: "source", title: "Source file", head: null, hits: q ? countIn((sourceLine(pr) + "\n" + streamLines(pr).join("\n")).toLowerCase(), q) : 0, dflt: false, show: !errOnly,
      body: <><p className="sm">{sourceLine(pr)}</p>{streamLines(pr).map((x, j) => <p className="sm" key={j}>{x}</p>)}</> });
  }
  l.steps.forEach((st, i) => {
    const warn = st.warnings ?? [], errs = st.errors ?? [], hit = q ? countIn(errOnly ? [...warn, ...errs].join("\n").toLowerCase() : stepText(st), q) : 0;
    const took = st.tookMs != null ? "Took " + stepTime(st.tookMs) : "";
    const progress = st.speedX != null || st.avgKbps != null ? [took + (st.speedX != null ? (took ? " at " : "") + st.speedX + "× speed" : ""), st.avgKbps != null ? "average " + st.avgKbps.toLocaleString() + " kbps" : ""].filter(Boolean).join(", ") : "";
    const flags = <>
      {warn.length > 0 && <div className="stack" style={{ gap: 4 }}>{warn.map((w, j) => <p className="sm" key={j}><Icon id="i-alert" /> Warning: {w}</p>)}</div>}
      {errs.length > 0 && <div className="stack" style={{ gap: 4 }}>{errs.map((w, j) => <p className="sm" key={j}><Icon id="i-x-c" /> Error: {w}</p>)}</div>}</>;
    const pill = ["ok", "done", "completed"].includes(st.outcome) ? <span className="pill ok">Done</span> : st.outcome === "failed" ? (kept ? <span className="pill warn">Original kept</span> : <span className="pill bad">Failed</span>) : st.outcome === "skipped" ? <span className="pill">Skipped</span> : null;
    raw.push({ key: "step-" + i, id: "job-step-" + (i + 1), title: stepLabel(st.name, st.found, st.check != null), hits: hit, dflt: st.outcome === "failed" || st.outcome === "skipped" || i === failedIdx || (kept && st.check != null), show: !errOnly || (flagged(st) && (!q || hit > 0)),
      head: <>
        {st.startedAt && st.tookMs != null && <span className="sm muted">{clock(st.startedAt)} · took {stepTime(st.tookMs)}</span>}{pill}
        {warn.length > 0 && <span className="pill warn">{plural1(warn.length, "warning", "warnings")}</span>}{errs.length > 0 && <span className="pill bad">{plural1(errs.length, "error", "errors")}</span>}
        {st.outcome === "skipped" && st.decision && <span className="sm muted">{st.decision}</span>}</>,
      body: errOnly ? flags : <div className="stack" style={{ gap: 6 }}>
        {(st.decision || st.reason) && <p className="sm">{st.decision} {st.reason && <span className="muted">{st.reason}</span>}</p>}
        {(st.set ?? []).map((x) => <p className="sm" key={x.name}>Set <code>{"{{" + x.name + "}}"}</code> = {x.value ?? <span className="muted">Hidden</span>}</p>)}
        {st.command && <div className="stack" style={{ gap: 4 }}><div className="row" style={{ justifyContent: "space-between" }}><span className="sm">Command</span><button className="btn sm ghost" type="button" onClick={() => navigator.clipboard?.writeText(st.command ?? "").catch(() => {})}><Icon id="i-copy" />Copy</button></div>
          <pre className="joblog sm">{st.command}</pre></div>}
        {old && st.probe && <p className="sm">Source: {sourceLine(st.probe)}</p>}
        {progress && <p className="sm">{progress}</p>}
        {flags}
        {st.check && <p className="sm">Picture check: score {st.check.score.toFixed(3)}{st.check.min != null ? " (needs " + st.check.min.toFixed(3) + ")" : ""}{st.check.passed != null ? " · " + (st.check.passed ? "passed" : "failed") : ""}</p>}
        {(st.moves ?? []).map((m, j) => <p className="sm" key={j}>{MOVE[m.action] ?? m.action} <code>{m.from}</code>{m.to && <> to <code>{m.to}</code></>}{!m.ok && <span className="muted"> · {m.error ?? "failed"}</span>}</p>)}
        {st.outputLines > 0 && <details open={st.outcome === "failed" || st.outcome === "skipped"}><summary className="sm">Show ffmpeg output ({st.outputLines} lines)</summary>
          {st.outputLines > 200 && <p className="sm"><a href={"/api/v2/jobs/" + encodeURIComponent(id) + "/log/download"}>Download all {st.outputLines.toLocaleString()} lines</a></p>}
          <pre className="joblog sm">{text(st.output)}</pre></details>}
        {(st.found?.length ?? 0) > 0 && <pre className="joblog sm">{st.found!.join("\n")}</pre>}
        {st.result && <p className="sm">{st.result}</p>}
      </div> });
  });
  if (!old && l.summary) {
    const s = l.summary;
    // the before and after sizes live in the drawer's Result table, so the summary doesn't repeat them
    raw.push({ key: "summary", title: "Summary", head: null, hits: 0, dflt: true, show: !errOnly, body: <>
      <p className="sm">Time taken: {stepTime(s.tookMs)}</p>
      <p className="sm">Worker: {worker ?? "—"}</p>
      <p className="sm">Result: {WORD[l.final ?? ""] ?? l.final}</p>
      {hasResult && <p className="sm"><a href="#job-result" onClick={(e) => { e.preventDefault(); document.getElementById("job-result")?.scrollIntoView({ block: "start" }); }}>Before and after: see Result</a></p>}</> });
  }
  const secs = raw.map((r) => ({ ...r, open: errOnly || (r.key in qOpen ? qOpen[r.key] : (!!q && r.hits > 0) || (r.key in open ? open[r.key] : r.dflt)) })); // design 04:37Z: search only adds open sections
  const shown = secs.filter((s) => s.show), anyClosed = shown.some((s) => !s.open);
  const tot = shown.reduce((n, s) => n + s.hits, 0), ks = shown.filter((s) => s.hits > 0).length; // review2 #455: search and the switch must both match
  const vis = l.steps.filter((_, i) => shown.some((s) => s.key === "step-" + i)); // review2 #455: count only the sections on screen
  const E = vis.reduce((n, st) => n + (st.errors?.length ?? 0), 0), W = vis.reduce((n, st) => n + (st.warnings?.length ?? 0), 0), K = vis.filter(flagged).length;
  const status = [q && (tot ? tot + " matches in " + ks + " step" + (ks > 1 ? "s" : "") : "No match"), errOnly && (E + W ? plural1(E, "error", "errors") + " and " + plural1(W, "warning", "warnings") + " in " + plural1(K, "section", "sections") : !q && "No errors or warnings in this log.")].filter(Boolean).join(" · ");
  return (
    <section id="job-log"><h3>Log</h3>
      <div className="joblog-tools">
        <input className="input sm joblog-q" type="search" placeholder="Search the log" aria-label="Search the log" value={q} onChange={(e) => setQ(e.target.value)} />
        <label className="sw"><input type="checkbox" role="switch" className="switch" checked={errOnly} onChange={(e) => setErrOnly(e.target.checked)} /><span className="sm">Errors and warnings only</span></label>
        <div className="joblog-end">
          <button className="btn sm ghost" type="button" onClick={copyAll}><Icon id="i-copy" />Copy log</button>
          {!MOCK && <a className="btn sm" href={"/api/v2/jobs/" + encodeURIComponent(id) + "/log/download"}><Icon id="i-download" />Download the full log</a>}
          {!errOnly && <button className="btn sm ghost" type="button" onClick={() => { const all = Object.fromEntries(shown.map((s) => [s.key, anyClosed])); setOpen(all); if (q) setQOpen(all); }}>{anyClosed ? "Open all" : "Close all"}</button>}
        </div>
      </div>
      <p className="sm muted" role="status" style={{ margin: status ? "0 0 6px" : 0 }}>{status}</p>
      <p className="sm" role="status" style={{ margin: copied ? "0 0 6px" : 0 }}>{copied}</p>
      {old && <p className="sm muted">This job ran before full logs were kept, so only its steps are shown.</p>}
      {shown.map((s) => (
        <details key={s.key} id={s.id} open={s.open} className="stepd" style={{ borderTop: "1px solid var(--border)", padding: "8px 0" }}
          onToggle={(e) => { const o = e.currentTarget.open; if (o === s.open) return; setOpen((p) => ({ ...p, [s.key]: o })); if (q) setQOpen((p) => ({ ...p, [s.key]: o })); }}>{/* only the person's clicks are kept, so clearing a search leaves sections as they left them */}
          <summary className="row nw" style={{ gap: 8, flexWrap: "wrap", cursor: "pointer" }}><strong>{s.title}</strong>{s.head}</summary>
          <div className="stack" style={{ gap: 6, paddingTop: 6 }}>{s.body}</div>
        </details>))}
      {(!l.summary && l.final != null && WORD[l.final]) && <p className="sm" style={{ borderTop: "1px solid var(--border)", paddingTop: 8 }}>{WORD[l.final]}</p>}
    </section>);
}

function JobLog({ id, name }: { id: string; name: string }) {
  const [text, setText] = useState(""), [done, setDone] = useState(false), [gone, setGone] = useState(false), [q, setQ] = useState(""), [follow, setFollow] = useState(true), [copied, setCopied] = useState("");
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    setText(""); setDone(false); setGone(false); setCopied("");
    if (MOCK) { setText("01:12:03 started on This computer\n01:12:04 Converting video: HEVC, Balanced quality\n01:13:10 10% done · about 9 min left\n01:14:22 25% done · about 7 min left\n"); setDone(true); return; }
    const es = new EventSource("/api/v2/jobs/" + encodeURIComponent(id) + "/log/stream?from=0");
    es.addEventListener("job.log", (e) => { const d = JSON.parse((e as MessageEvent).data) as { text: string }; setText((t) => t + d.text); });
    es.addEventListener("end", () => { setDone(true); es.close(); });
    es.onerror = () => { if (es.readyState === EventSource.CLOSED) setGone(true); }; // a refused stream (404); a dropped one reconnects by itself
    return () => es.close();
  }, [id]);
  useEffect(() => { if (follow && box.current) box.current.scrollTop = box.current.scrollHeight; }, [text, follow]);
  const shown = q ? text.split("\n").filter((l) => l.toLowerCase().includes(q.toLowerCase())).join("\n") : text;
  const copyLog = () => copyLogText(text, setCopied);
  const download = () => { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" })); a.download = name.replace(/\.[^.]+$/, "") + ".log"; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000); };
  return (
    <section><h3>Log</h3>
      <div className="row" style={{ gap: 6, marginBottom: 6 }}>
        <input className="input sm" type="search" placeholder="Search the log" aria-label="Search the log" value={q} onChange={(e) => setQ(e.target.value)} style={{ flex: 1, minWidth: 0 }} />
        {!done && <button className="btn sm ghost" type="button" aria-pressed={follow} onClick={() => setFollow(!follow)}>{follow ? "Stop following" : "Follow"}</button>}
        <button className="btn sm ghost" type="button" disabled={!text} onClick={copyLog}><Icon id="i-copy" />Copy log</button>
        <button className="btn sm ghost" type="button" disabled={!text} onClick={download}><Icon id="i-download" />{q ? "Download the whole log" : "Download"}</button>
      </div>
      <p className="sm muted" role="status" style={{ margin: copied ? "0 0 6px" : 0 }}>{copied}</p>
      <pre className="joblog" ref={box} tabIndex={0} aria-label="Log" aria-live="off">{shown || (text ? "No line matches." : done ? "The log is empty." : gone ? "The log can't be shown." : "Waiting for the log…")}</pre>
      <p className="sm muted">{done ? "The log is complete." : gone ? "The server didn't send it. Close and open this file again to retry." : "Live: new lines appear as they are written."}</p>
    </section>
  );
}

const fmtV = (p: Probe | null | undefined) => {
  if (!p) return null;
  const v = p.streams?.find((s) => s.type === "video"), a = p.streams?.filter((s) => s.type === "audio") ?? [];
  return { video: [p.videoCodec?.toUpperCase().replace("H264", "H.264"), v?.Height ? v.Height + "p" : ""].filter(Boolean).join(" · ") || "—",
    audio: a.map((x) => x.codec.toUpperCase() + (x.channelLayout ? " " + x.channelLayout : "")).join(", ") || "None",
    bitrate: p.bitrateKbps ? count(p.bitrateKbps) + " kbps" : "—", size: p.sizeBytes ? bytes(p.sizeBytes) : "—" };
};

// The job drawer (?job=<id>): right side, 520 px; a full-screen sheet on phones. Esc closes; focus returns to the row.
function Drawer({ id, row, onClose, onChanged }: { id: string; row?: JobItem; onClose: () => void; onChanged: () => void }) {
  const scan = id.startsWith("skip-"); // a file a scan left out: no job behind it, only its row (key() opens a job when there is one)
  const d = useData(() => (scan ? Promise.resolve(null as unknown as JobDetail) : getJob(id)), [id]);
  const [said, setSaid] = useState(""), box = useRef<HTMLElement>(null);
  useEffect(() => onLive((type, data) => { if (type === "job.state" && (data as { jobId?: string })?.jobId === id) d.reload(); }), [id]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    box.current?.focus();
    const esc = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", esc);
    return () => { document.removeEventListener("keydown", esc); (document.querySelector('[data-job="' + CSS.escape(id) + '"]') as HTMLElement | null)?.focus(); };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps
  // review c610d24 P3: the actions follow the job's own state, not the tab it was opened from. A skipped job opened
  // without its row (another tab in the URL) finds its row in the Skipped list: Convert again needs its skip-N id.
  const ended = d.data && (d.data.state === "skipped" || d.data.state === "cancelled");
  // review a8f85e0 P3: the list holds the first 200 skips only; a job beyond them is looked for page by page (findSkip)
  const skips = useData(async () => {
    if (row || !ended) return null;
    const first = await listJobs("skipped", "");
    return failed(first) || first.items.some((x) => x.jobId === id) ? first : findSkip(id);
  }, [id, !!row, !!ended]);
  const own = row ?? skips.data?.items.find((x) => x.jobId === id);
  // E: this file's other jobs, newest first (not this job); left out when there are none
  const filePath = own?.path ?? "", fj = useData(() => (filePath ? fileJobs(filePath) : Promise.resolve([] as JobItem[])), [filePath]).data;
  const others = !fj || failed(fj) ? [] : fj.filter((x) => (x.jobId ?? x.id) !== id);
  const j = d.data, state = (j?.state ?? own?.state) as JobState | undefined, name = own?.name ?? j?.file?.path.split("/").pop() ?? "Job " + id;
  const active = state && ["queued", "retry_wait", "preparing", "running", "validating", "committing"].includes(state);
  const run = async (a: JobAction) => {
    if (a === "cancel" && !window.confirm((state === "queued" || state === "retry_wait" ? "Cancel this file?" : "Stop converting this file?") + " The file stays as it is. Nothing is deleted.")) return;
    const r = await act(jobAction(a, [own?.id ?? id]), setSaid); // the row's id: a job id, or skip-N for a file a scan left out (jobsApi)
    if (r) { const x = r.results[0]; setSaid(x?.ok ? (a === "requeue" && (own?.id ?? id).startsWith("skip-") ? "It will be looked at again on the next scan." : "Done.") : x?.message ?? ""); d.reload(); onChanged(); }
  };
  // G2 (one UI, design SCREENS Jobs drawer): a Done job whose original was replaced and kept in the trash offers
  // "Get original back", found in the kept list by its id (the row) or the original's path (a drawer opened by link)
  const [back, setBack] = useState(false), isDone = state === "completed", backBtn = useRef<HTMLButtonElement>(null);
  const closeBack = () => (setBack(false), backBtn.current?.focus()); // Escape and Cancel: the focus goes back to "Get original back" (review 66edc3d P3)
  const kl = useData(() => (isDone ? listKept() : Promise.resolve(null)), [id, isDone]);
  const src = own?.path ?? j?.file?.path, kept = kl.data?.items.find((k) => (own?.keptId ? k.keptId === own.keptId : k.path === src));
  const getBack = async () => {
    setBack(false);
    const r = await act(restoreKept(kept!.keptId), setSaid);
    if (r) { setSaid(r.message); kl.reload(); d.reload(); onChanged(); }
  };
  // design SCREENS V6 (11-19 14:09Z): the Original line by the server's originalState
  const keptWords = (n: number) => (n > 0 ? "In the trash for " + plural(n, "more day", "more days") + ", then deleted" : "In the trash, deleted today");
  const saving = own?.beforeBytes != null && own.afterBytes != null ? own.beforeBytes - own.afterBytes : 0; // the Size row's figure, as bytes
  const freeNote = saving > 0 ? " Removing the original frees " + bytes(saving) + "." : "";
  const keptLine = kept && <>{keptWords(Math.max(0, Math.ceil((new Date(kept.deleteOn).getTime() - Date.now()) / 864e5)))} · <button ref={backBtn} className="linkbtn" type="button" aria-expanded={back} onClick={() => setBack(!back)}>Get original back</button></>;
  // #163: a Done job converts again when the server says so (never decided here); the confirm names the workflow's
  // CURRENT version, which is what runs. Design's two variants (replace vs output-first, the keep days) need the job's
  // policy from the server: until then one sentence that holds for both.
  const [again, setAgain] = useState(false), ca = j?.convertAgain, done = state === "completed";
  const wf = useData(() => (done && ca?.possible && j?.workflow ? getWorkflow(j.workflow.id) : Promise.resolve(null)), [id, done, !!ca?.possible]);
  const runAgain = async () => {
    setAgain(false);
    const r = await act(convertDoneAgain(id), setSaid);
    if (r) { setSaid("Queued again. It converts from the original; the earlier new file stays until the new one passes its check."); d.reload(); onChanged(); }
  };
  const skip = state === "skipped", lw = useData(() => (skip ? listLibrariesV2() : Promise.resolve(null)), [id, skip]);
  const lib = lw.data && !failed(lw.data) ? lw.data.find((l) => l.id === (j?.library?.id ?? own?.libraryId)) : undefined;
  const rule = !skip ? null : j?.workflow ? { id: j.workflow.id, name: lib?.workflowId === j.workflow.id ? lib.workflowName : own?.flow?.name ?? "its workflow" } : lib ? { id: lib.workflowId, name: lib.workflowName } : null; // the workflow that skipped it; a scan's skip has no job, so its library's
  const before = fmtV(j?.metadata?.before), after = fmtV(j?.metadata?.after);
  const reason = j?.reason?.message ?? (row ? reasonText(row) : "");
  // one fetch of the log for the step list and What the check found; fetched again when the job stops running
  const log = useData(() => (scan || active ? Promise.resolve(null as unknown as JobLog) : getJobLog(id)), [id, scan, active]);
  // the Result table also takes the log's subtitles and length (contract v2 summary before/after); a row shows only with data
  const sumS = log.data?.summary, subsRow = !!(sumS?.before?.subtitles || sumS?.after?.subtitles), lenRow = sumS?.before?.durationSec != null || sumS?.after?.durationSec != null;
  const showResult = !!(before || after || subsRow || lenRow), lenText = (x?: { durationSec?: number }) => (x?.durationSec != null ? elapsed(x.durationSec) : "—");
  const found = (log.data?.steps ?? []).filter((st) => (st.found?.length ?? 0) > 0);
  const kept0 = !!state && keptOrig({ state, reason: j?.reason ?? own?.reason });
  return (
    <div className="drawer-wrap" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="drawer jobd" ref={box} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="jd-h">
        <div className={"hd" + (state === "failed" && !kept0 ? " failed" : "")}>
          <button className="btn icon ghost" type="button" aria-label="Close" onClick={onClose}><Icon id="i-chev-l" /></button>
          <div className="sp"><h2 id="jd-h" className="clamp2" title={name}><FileName n={name} /></h2>{state && <span style={{ alignSelf: "flex-start" }}><Pill j={{ state, retryAt: j?.retryAt ?? own?.retryAt, subState: own?.subState, reason: j?.reason ?? own?.reason, warning: j?.warning }} /></span>}</div>
          {/* SCREENS V6b header: Retry, Cancel or Stop, and the job ID to copy (the body keeps the rest) */}
          <div className="row nw" style={{ gap: 6, flexWrap: "wrap" }}>
            {active && <button className="btn sm" type="button" onClick={() => run("cancel")}><Icon id="i-stop" />{state === "queued" || state === "retry_wait" ? "Cancel" : "Stop"}</button>}
            <button className="btn sm ghost" type="button" title="Copy the job ID" onClick={() => navigator.clipboard?.writeText(id).then(() => setSaid("Job ID " + id + " copied."), () => setSaid("Job ID: " + id))}><Icon id="i-copy" />ID {id}</button>
          </div>
        </div>
        <div className="bd">
          {(done && ca?.possible) || (state === "skipped" || state === "cancelled" || state === "failed" || state === "retry_wait") ? <div className="row">
            {(state === "failed" || state === "retry_wait") && <button className="btn sm" type="button" onClick={() => run("retry")}><Icon id="i-refresh" />{state === "retry_wait" ? "Try now" : "Retry"}</button>}
            {done && ca?.possible && <button className="btn sm" type="button" aria-expanded={again} onClick={() => setAgain(!again)}><Icon id="i-arrow-r" />Convert again</button>}
            {(state === "skipped" || state === "cancelled") && (!j?.removed || !!own?.id.startsWith("skip-")) && <button className="btn sm" type="button" onClick={() => run("requeue")}><Icon id="i-arrow-r" />Convert again</button>}
            <span className="sp" />
          </div> : null}
          {done && ca && !ca.possible && ca.reason && <p className="muted sm">{ca.reason.message}</p>}
          {again && <div role="alertdialog" aria-label="Convert again?" style={{ padding: 12, border: "1px solid var(--border-strong)", borderRadius: "var(--radius)", background: "var(--raised)" }} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), setAgain(false))}>
            <p style={{ margin: "0 0 8px" }}>Convert {name} again{wf.data && !failed(wf.data) ? " with " + wf.data.name + (wf.data.version ? " v" + wf.data.version : "") : ""}? It starts from the original, never from the new file. The earlier new file is replaced once the new one passes its check.</p>
            <div className="row"><button className="btn sm primary" type="button" autoFocus onClick={runAgain}>Convert again</button><button className="btn sm ghost" type="button" onClick={() => setAgain(false)}>Cancel</button></div></div>}
          <p className="muted" role="status" style={said ? { margin: 0 } : { display: "contents" }}>{said}</p>{/* always present so it is announced; takes no space while empty */}
          {d.error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{d.error.message}</span></p>}
          {/* review db7b1f1 P3: "removed" is the server's flag, not a person's word. Only a job removed with its library
              gets a note; a job that ended as a skip shows its reason under Why like any other. */}
          {done && j?.warning && <div className="note warn"><Icon id="i-alert" /><div style={{ display: "grid", gap: 6 }}>{warnLines(j).map((w, i) => <div key={i}>{w}</div>)}</div></div>}
          {j?.removed && j.reason?.code === "removed" ? <div className="note"><Icon id="i-info" /><div>Its library was removed, so this file won't be converted.</div></div>
            : j?.failure ? <WhyFailed id={id} msg={reason} code={j.reason?.code ?? ""} worker={j.worker ?? null} f={j.failure} found={found.flatMap((st) => st.found!)} />
            : found.length ? <CheckFound lines={found.flatMap((st) => st.found!)} reason={reason} />
            : reason && <section><h3>Why</h3><p>{reason}</p></section>}
          <section><h3>File</h3><dl className="kv">
            {(own?.path ?? j?.file?.path) && <><dt>Source</dt><dd className="mono sm">{own?.path ?? j?.file?.path}</dd></>} {/* review 54ff228: no '-' when the server doesn't keep the path (a removed library's job) */}
            {(j?.library || own) && <><dt>Library</dt><dd>{j?.library?.name ?? own?.library}</dd></>}
            {own?.state === "completed" && <>{own.originalState !== "restored" && <><dt>Size</dt><dd>{saved(own.beforeBytes, own.afterBytes)}</dd>{(own.afterBytes ?? 0) > (own.beforeBytes ?? 0) && <><dt /><dd className="muted sm">Used because it passed every check, even though it is bigger.</dd></>}</>} {/* review 66edc3d P3: after a restore the file is the original again: no saving */}
              <dt>Original</dt><dd>{kept ? keptLine : own.originalState === "kept" ? keptWords(own.keptDaysLeft ?? 0) : own.originalState === "restored" ? "Put back in place. The smaller version was deleted." : own.originalState === "deleted" ? "Deleted. The new file replaced it." : own.originalState === "left_in_place" ? "Left where it was. The new file is in another folder." + freeNote : own.originalState === "kept_beside" ? "Kept next to the new file, in the same folder." + freeNote : "Left in place"}</dd></>}
            {!own && kept && <><dt>Original</dt><dd>{keptLine}</dd></>}
          </dl>
          {others.length > 0 && <p className="sm" style={{ marginTop: 8 }}>This file's other jobs: {others.map((x, i) => <span key={x.jobId ?? x.id}>{i > 0 && " · "}<a className="linkbtn" href={"/jobs?tab=" + tabOf(x.state) + "&job=" + encodeURIComponent(x.jobId ?? x.id)} onClick={(e) => { if (e.button === 0 && !e.metaKey && !e.ctrlKey) { e.preventDefault(); navigate("/jobs?tab=" + tabOf(x.state) + "&job=" + encodeURIComponent(x.jobId ?? x.id)); }}}>{WORD[x.state] ?? x.state} {x.finishedAt ? new Date(x.finishedAt).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) : ""}</a></span>)}</p>}
        </section>
          {back && kept && <div role="alertdialog" aria-label="Get the original back?" style={{ padding: 12, border: "1px solid var(--border-strong)", borderRadius: "var(--radius)", background: "var(--raised)" }}
            onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), closeBack())}>
            <p style={{ margin: "0 0 8px" }}><strong>Get the original of “{name}” back?</strong> The original ({bytes(kept.originalBytes)}) goes back in place and the smaller version{kept.newPath && kept.newPath.split(/[\\/]/).pop() !== name ? ", " + kept.newPath.split(/[\\/]/).pop() + "," : ""} is deleted. This file won't be converted again unless you press Convert again.</p>
            <div className="row"><button className="btn primary" type="button" autoFocus onClick={getBack}>Get original back</button><button className="btn ghost" type="button" onClick={closeBack}>Cancel</button></div></div>}
          {/* G7: a skipped file links to the workflow whose rule skipped it (a scan's skip has no job: its library's workflow) */}
          {(j?.workflow || rule) && <section><h3>Workflow</h3><p>{rule ? <a className="linkbtn" href={"/workflows/" + encodeURIComponent(rule.id)} onClick={(e) => { if (e.button === 0 && !e.metaKey && !e.ctrlKey) { e.preventDefault(); navigate("/workflows/" + encodeURIComponent(rule.id)); }}}>Change rule in {rule.name}</a> : own?.flow?.name ?? "Its library's workflow"}{j?.workflow && " · v" + j.workflow.version}</p></section>}
          {!scan && <section><h3>Timeline</h3>
            {!j ? <p className="muted">Loading…</p> : !j.steps.length && !j.events.length ? <p className="muted">No step history for jobs from before the update.</p> : <>
              {j.steps.length > 0 && <ol className="tline">{j.steps.map((s, i) => <li key={i} className={s.ok ? "" : SOFT.includes(s.step) ? "warn" : "bad"}><Icon id={s.ok ? "i-check-c" : SOFT.includes(s.step) ? "i-alert" : "i-x-c"} /><span className="sp">{cap(s.words)}</span><span className="muted sm nowrap">{clock(s.at)}</span></li>)}</ol>}
              {j.events.length > 0 && <p className="sm muted">History: {j.events.map((e) => WORD[e.to] + " " + clock(e.at)).join(" → ")}</p>}
            </>}
          </section>}
          {/* review db7b1f1 P2: the scan sentence only for a file a scan left out; a cancelled file whose job the server
              doesn't send yet claims nothing about scans or steps */}
          {scan && <section><h3>Timeline</h3><p className="muted">{state === "cancelled" ? "Cancelled." : "This file was left out when its library was scanned, so it has no job and no steps."}</p></section>}
          {!scan && (active ? <JobLog id={id} name={name} /> : <StepLog d={log} id={id} worker={j?.worker ?? null} failedIdx={j?.failure?.stepIndex ?? null} kept={kept0} hasResult={showResult} />)}
          {/* #148: before is the original; null for an older job that replaced a file of the same name: said so, never blank */}
          {showResult && <section id="job-result"><h3>Result</h3><table className="tbl kvt"><thead><tr><th /><th>Before</th><th>After</th></tr></thead><tbody>
            <tr><th>Video</th>{before ? <td>{before.video}</td> : <td rowSpan={4 + (subsRow ? 1 : 0) + (lenRow ? 1 : 0)} className="muted">Not recorded for this job</td>}<td>{after?.video ?? "—"}</td></tr>
            <tr><th>Audio</th>{before && <td>{before.audio}</td>}<td>{after?.audio ?? "—"}</td></tr>
            <tr><th>Bitrate</th>{before && <td>{before.bitrate}</td>}<td>{after?.bitrate ?? "—"}</td></tr>
            <tr><th>Size</th>{before && <td>{before.size}</td>}<td>{after?.size ?? "—"}</td></tr>{subsRow && <tr><th>Subtitles</th>{before && <td>{sumS?.before?.subtitles ?? "—"}</td>}<td>{sumS?.after?.subtitles ?? "—"}</td></tr>}{lenRow && <tr><th>Length</th>{before && <td>{lenText(sumS?.before)}</td>}<td>{lenText(sumS?.after)}</td></tr>}</tbody></table></section>}
        </div>
      </aside>
    </div>
  );
}
