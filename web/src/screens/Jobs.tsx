// V6 Jobs (SCREENS V6; DESIGN/v2 mockups/jobs.html, jobs-drawer.html, jobs-drawer-done.html) on the real /api/v2/jobs
// (#113) with live updates from /api/v2/events (#118/#120/#122), or the sample jobs with ?mock=1. The server lists one
// tab at a time (Waiting, Running, Done, Skipped, Failed); All and Paused tabs and the codec/step/worker filters come
// when it has them. Not here yet: output path and Get original back (v2 has neither).
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, clock, count, elapsed, plural } from "../format";
import { convertDoneAgain, fileJobs, findSkip, getJob, getJobLog, jobAction, listJobs, listKept, restoreKept } from "../jobsApi";
import { getWorkflow } from "../workflowApi";
import { getHealth, getSettings } from "../libraryApi";
import { onLive } from "../live";
import { listLibraries } from "../libraryApi";
import { Empty, PageHead } from "../PageHead";
import { navigate, useSearch } from "../router";
import { useData } from "../useData";
import type { Failure, JobAction, JobDetail, JobItem, JobProgressEvent, JobState, Probe, Tab, Pin } from "../types/jobs";

const TABS: [Tab, string][] = [["waiting", "Waiting"], ["running", "Running"], ["completed", "Done"], ["skipped", "Skipped"], ["failed", "Failed"]];
// Spec states in words (SCREENS V6); a cancelled file lists under Skipped and reads "Cancelled".
const tabOf = (st: JobState): Tab => (st === "queued" || st === "retry_wait" ? "waiting" : st === "completed" ? "completed" : st === "skipped" || st === "cancelled" ? "skipped" : st === "failed" ? "failed" : "running");
const WORD: Record<string, string> = { queued: "Waiting", preparing: "Preparing", running: "Running", validating: "Checking", committing: "Saving",
  completed: "Done", skipped: "Skipped", failed: "Failed", cancelled: "Cancelled", retry_wait: "Retry", done: "Done", kept: "Original kept: the new file wasn't used." };
const PILL: Partial<Record<JobState, string>> = { preparing: "run", running: "run", validating: "run", committing: "run", completed: "ok", failed: "bad", retry_wait: "warn" };
const ICON: Partial<Record<JobState, string>> = { completed: "i-check-c", failed: "i-x-c", skipped: "i-skip", cancelled: "i-stop", retry_wait: "i-clock", queued: "i-clock" };
// V5b: a waiting file whose library is paused (subState library_disabled) reads "Library paused" (design SCREENS V5);
// it has no place in the queue, so its '#' is '–'.
type PillJob = { state: JobState; retryAt?: string | null; subState?: string | null };
const libPaused = (j: PillJob) => j.state === "queued" && j.subState === "library_disabled";
const words = (j: PillJob) => (libPaused(j) ? "Library paused" : j.state === "retry_wait" && j.retryAt ? "Retry at " + clock(j.retryAt) : WORD[j.state]);
function Pill({ j }: { j: PillJob }) {
  return <span className={"pill " + (libPaused(j) ? "warn" : PILL[j.state] ?? "")}><Icon id={libPaused(j) ? "i-pause" : ICON[j.state] ?? "i-play"} />{words(j)}</span>;
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
  if (j.state === "completed") return <span className="sm">{saved(j.beforeBytes, j.afterBytes)}{j.originalState === "kept" && j.keptDaysLeft != null && <span className="muted"> · original kept {plural(j.keptDaysLeft, "more day", "more days")}</span>}</span>;
  return <span className="sm muted clamp2" title={reasonText(j)}>{reasonText(j)}</span>;
}

const RUNS: JobState[] = ["preparing", "running", "validating", "committing"]; // live progress only for a running row
// The drawer opens by job: a skipped row with a job behind it (#149: a workflow's skip after it failed, or one the user
// stopped) opens that job (steps, tries, log); a file a scan left out has no job and opens its row ("skip-N").
const key = (j: JobItem) => j.jobId ?? j.id;

export function Jobs() {
  const search = new URLSearchParams(useSearch());
  const tab = (TABS.find(([t]) => t === search.get("tab"))?.[0] ?? "waiting") as Tab, open = search.get("job"), library = search.get("libraryId") ?? "";
  // "See its jobs" (a library page) filters every tab to that library; the library's name shows as a chip that clears it
  const libs = useData(listLibraries, [], 60000);
  const [q, setQ] = useState(""), [selecting, setSelecting] = useState(false), [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState(""), [prio, setPrio] = useState<Pin | "none">("first");
  // each answer remembers its tab and search, so a tab switch never shows the last tab's rows while it loads
  const page = useData(() => listJobs(tab, q, library).then((r) => (failed(r) ? r : { ...r, for: tab + "|" + q + "|" + library })), [tab, q, library], 15000);
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
  const go = (t: Tab, job?: string | null) => navigate("/jobs?tab=" + t + (library ? "&libraryId=" + encodeURIComponent(library) : "") + (job ? "&job=" + encodeURIComponent(job) : ""));
  const current = page.data?.for === tab + "|" + q + "|" + library ? page.data : undefined;
  const items = (current?.items ?? []).map((j) => (live[j.id] && RUNS.includes(j.state) ? { ...j, progress: live[j.id] } : j)), counts = page.data?.counts;
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
    <button className="btn sm ghost" type="button" disabled={none} title={none ? "Select files first" : undefined} onClick={() => run("retry")}><Icon id="i-refresh" />Retry</button>
    {(tab === "skipped" || tab === "failed") && <button className="btn sm ghost" type="button" disabled={none} title={none ? "Select files first" : undefined} onClick={() => run("requeue")}><Icon id="i-arrow-r" />Put back in queue</button>} {/* only where the server takes it: skips (convert-again) and failed jobs */}
    <button className="btn sm ghost" type="button" disabled={none} title={none ? "Select files first" : undefined} onClick={() => run("cancel")}><Icon id="i-stop" />Cancel</button>
    {tab === "waiting" && <span className="row nw" style={{ gap: 4 }}>
      <select className="input sm" aria-label="Priority" value={prio} onChange={(e) => setPrio(e.target.value as Pin | "none")}><option value="first">First</option><option value="none">Normal</option><option value="last">Last</option></select>
      <button className="btn sm ghost" type="button" disabled={none} onClick={() => run("reprioritize")}>Change priority</button></span>}
  </>;
  return (
    <div className="stack">
      <PageHead icon="i-jobs" title="Jobs" help="Every file the workers look at: waiting, converting, done, skipped and failed." />
      {library && <div className="row" style={{ gap: 6 }}><span className="mchip">Library: {libs.data?.find((l) => l.id === library)?.name ?? library}
        <button className="btn sm icon ghost" type="button" aria-label="Clear the library filter" onClick={() => navigate("/jobs?tab=" + tab)}><Icon id="i-x" /></button></span></div>}
      <div className="tabs" role="tablist" aria-label="Jobs">
        {TABS.map(([t, label]) => <button key={t} role="tab" type="button" aria-selected={tab === t} onClick={() => tab !== t && go(t)}>{label}{counts && <span className="n">{count(counts[t])}</span>}</button>)}
      </div>
      <div className="ltools" role="toolbar" aria-label="Jobs">
        <span className="desk">{actions}</span>
        <button className="btn sm ghost phone" type="button" aria-pressed={selecting} onClick={() => (setSelecting(!selecting), setSel([]))}>{selecting ? "Done" : "Select"}</button>
        <details className="menu phone"><summary className="btn sm icon ghost" aria-label="More actions"><Icon id="i-more" /></summary><div className="pop" role="menu">{actions}</div></details>
        {sel.length > 0 && <span className="muted sm">{sel.length} selected</span>}
        <input className="input find" type="search" placeholder="Search file names" aria-label="Search file names" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <p className="muted" role="status">{said}</p>
      {page.error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{page.error.message}</span></p>}
      {!current ? <div className="empty">Loading...</div> : !items.length ? (q ? <div className="empty">No file matches.</div>
        : <Empty icon="i-jobs" text={{ waiting: "Nothing is waiting.", running: "Nothing is converting right now.", completed: "No file is done yet.", skipped: "No file was skipped.", failed: "Nothing has failed." }[tab]} />) : (
        <div className={"list" + (selecting ? " selecting" : "")}><table className="tbl jobs">
          <thead><tr>
            <th className="selc"><label className="hit"><input type="checkbox" aria-label="Select all" checked={sel.length === items.length} onChange={(e) => setSel(e.target.checked ? items.map((j) => j.id) : [])} /></label></th>
            {tab === "waiting" && <th className="posc" title="Place in the queue: 1 is converted next">#</th>}
            <th>File</th><th className="stc">Status</th><th data-p="2">Details</th><th data-p="3">Worker</th><th data-p="4">{tab === "completed" ? "Finished" : "Size"}</th>
          </tr></thead>
          <tbody>{items.map((j) => (
            <tr key={j.id} className={sel.includes(j.id) ? "sel" : undefined}>
              <td className="selc"><label className="hit"><input type="checkbox" aria-label={"Select " + j.name} checked={sel.includes(j.id)} onChange={(e) => setSel(e.target.checked ? [...sel, j.id] : sel.filter((x) => x !== j.id))} /></label></td>
              {tab === "waiting" && <td className="posc" title={j.position != null ? "Place " + j.position + " in the queue" : "Not taken now"}>{j.position ?? "–"}</td>}
              <td className="idc"><a className="ttl" href={"/jobs?tab=" + tab + "&job=" + encodeURIComponent(key(j))} data-job={key(j)} title={j.path}
                onClick={(e) => { if (e.button === 0 && !e.metaKey && !e.ctrlKey) { e.preventDefault(); go(tab, key(j)); } }}>{j.name}</a>
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
      {open && <Drawer id={open} row={items.find((j) => key(j) === open)} onClose={() => go(tab)} onChanged={page.reload} />}
    </div>
  );
}

// The job's log (SCREENS V6: streaming, searchable, Download): GET /api/v2/jobs/{id}/log/stream (#131). Each piece
// comes as job.log {text, next} with next as its id, so the browser's own reconnect resumes where it stopped; "end"
// comes once the job is over and the whole log has been sent.
// E (SCREENS V6b 'Why it failed'): the sentence, where it stopped, the server's hint when it has one, and the last
// ffmpeg error lines (at most five) behind a disclosure. Each part is left out when the server sends none.
// durations read '4 min 12 s' and '0.1 s' everywhere (the design's phone note)
const dur = (ms: number) => { const s = ms / 1000; return s < 1 ? s.toFixed(1) + " s" : s < 60 ? Math.round(s) + " s" : Math.floor(s / 60) + " min " + (Math.round(s) % 60) + " s"; };
const text = (o: string | string[]) => Array.isArray(o) ? o.join("\n") : o;
// map server step labels to person-facing names (workshop #217: encode, check)
const stepLabel = (s: string) => ({ encode: "Converting video", check: "Checking the picture", imported: "Older log, not split into steps", replace: "Save the result" } as Record<string, string>)[s] ?? s;
// SCREENS V6b: the sentence, the reason code small under it, where it stops (the step opens in the log), the hint as
// 'What you can do' (left out when null), the last ffmpeg lines with Copy, and the buttons. --error left border.
function WhyFailed({ id, msg, code, worker, f }: { id: string; msg: string; code: string; worker: string | null; f: Failure }) {
  const t = dur(f.afterMs), box = useRef<HTMLElement>(null), ver = useData(getHealth, []).data?.version;
  // Copy summary: the plain text a bug report needs (V6b item 5): the job, its worker and the app version, then the box
  const copy = () => navigator.clipboard?.writeText("Job " + id + " · " + (worker ?? "no worker") + " · Shrink Your Shit " + (ver ?? "") + "\n" + (box.current?.innerText ?? "")).catch(() => {});
  return (
    <section ref={box} className="note" role="region" aria-label="Why it failed" style={{ display: "block", borderLeft: "3px solid var(--error)" }}>
      <h3><Icon id="i-alert" /> Why it failed <span className="pill bad">Failed</span></h3>
      <p style={{ fontSize: "var(--fs-lg)" }}>{msg}</p>
      <p className="sm muted mono">{code}</p>
      <p className="sm">In step {f.stepIndex + 1} of {f.stepCount}, <a href={"#job-step-" + (f.stepIndex + 1)}>{stepLabel(f.step)}</a>, after {t}{f.mediaTime ? " (at " + f.mediaTime + " in the video)" : ""}.</p>
      {f.hint && <p><strong>What you can do:</strong> {f.hint}</p>}
      {f.ffmpegRan === false
        ? <p className="sm muted">ffmpeg didn't run.</p>
        : f.keyLines.length > 0 && <div className="stack" style={{ gap: 4 }}><div className="row" style={{ justifyContent: "space-between" }}><span className="sm">{(f.step === "encode" || f.step === "check" ? "What ffmpeg said" : "The error")}</span><button className="btn sm ghost" type="button" onClick={() => navigator.clipboard?.writeText(f.keyLines.join("\n")).catch(() => {})}><Icon id="i-copy" />Copy</button></div>
          <pre className="joblog">{f.keyLines.join("\n")}</pre></div>}
      <div className="row" style={{ gap: 8, flexWrap: "wrap", marginTop: 12 }}>
        <a className="btn sm" href={"#job-step-" + (f.stepIndex + 1)}><Icon id="i-file" />Open the full log</a>
        {!MOCK && <a className="btn sm ghost" href={"/api/v2/jobs/" + encodeURIComponent(id) + "/log/download"}><Icon id="i-download" />Download</a>}
        <button className="btn sm ghost" type="button" onClick={copy}><Icon id="i-copy" />Copy summary</button>
      </div>
    </section>);
}

// count of q in a step's searchable text (case-insensitive)
const matches = (st: { name: string; command: string | null; output: string | string[]; result: string | null }, q: string) => [st.name, st.command ?? "", text(st.output), st.result ?? ""].join("\n").toLowerCase().split(q.toLowerCase()).length - 1;
// E: a finished job's log by step (GET /log), SCREENS V6b: each step a row that opens; the failed step open; inside a step
// its command, its source, its picture check, its ffmpeg output (last 200 lines on a failure, the rest by download), its
// result; the log's last line is the job's final line.
function StepLog({ id, worker, failedIdx }: { id: string; worker: string | null; failedIdx: number | null }) {
  const d = useData(() => getJobLog(id), [id]), keep = useData(getSettings, []), [q, setQ] = useState("");
  if (!d.loaded) return <section><h3>Log</h3><p className="muted">Loading the log...</p></section>;
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
  return (
    <section id="job-log"><h3>Log</h3>
      {note[l.state] ? <p className="sm muted">{note[l.state]}</p> : <>
      {/* V6b Search: every step's command, output and result; the steps with matches open and are counted */}
      <div className="row" style={{ gap: 6, margin: "8px 0", flexWrap: "wrap" }}>
        <input className="input sm" type="search" placeholder="Search the log" aria-label="Search the log" value={q} onChange={(e) => setQ(e.target.value)} style={{ flex: 1, minWidth: 0 }} />
        {!MOCK && <a className="btn sm" href={"/api/v2/jobs/" + encodeURIComponent(id) + "/log/download"}><Icon id="i-download" />Download the full log</a>}
        {q && <span className="sm muted" role="status">{(() => { const ns = l.steps.map((st) => matches(st, q)), t = ns.reduce((x, y) => x + y, 0), k = ns.filter((x) => x > 0).length; return t ? t + " matches in " + k + " step" + (k > 1 ? "s" : "") : "No match"; })()}</span>}
      </div>
      {l.steps.map((st, i) => {
        const hit = q ? matches(st, q) : 0;
        const pr = st.probe as Probe | null, v = pr?.streams?.find((x) => x.type === "video"), n = (t: string) => (pr?.streams ?? []).filter((x) => x.type === t).length;
        const hdr = v?.dolbyVision ? "Dolby Vision" : v?.colorTransfer === "smpte2084" ? "HDR10" : v?.colorTransfer === "arib-std-b67" ? "HLG" : "";
        const sum = pr && [pr.videoCodec?.toUpperCase().replace("H264", "H.264"), v?.Width && v.Height ? v.Width + "×" + v.Height : "", v?.frameRate ? +v.frameRate.toFixed(3) + " fps" : "", [v?.bitDepth ? v.bitDepth + "-bit" : "", hdr].filter(Boolean).join(" "), n("audio") && n("audio") + " audio", n("subtitle") && n("subtitle") + " subtitles", pr.durationSec ? elapsed(pr.durationSec) : "", pr.sizeBytes ? bytes(pr.sizeBytes) : ""].filter(Boolean).join(" · ");
        return (
          <details key={i} id={"job-step-" + (i + 1)} open={q ? hit > 0 : st.outcome !== "ok" || i === failedIdx} className="stepd" style={{ borderTop: "1px solid var(--border)", padding: "8px 0" }}>
            <summary className="row nw" style={{ gap: 8, flexWrap: "wrap", cursor: "pointer" }}><strong>{stepLabel(st.name)}</strong>{st.startedAt && st.tookMs != null && <span className="sm muted">{clock(st.startedAt)} · took {dur(st.tookMs)}</span>}
              {["ok", "done", "completed"].includes(st.outcome) ? <span className="pill ok">Done</span> : st.outcome === "failed" ? <span className="pill bad">Failed</span> : null}{hit > 0 && <span className="pill">{hit} match{hit > 1 ? "es" : ""}</span>}</summary>
            <div className="stack" style={{ gap: 6, paddingTop: 6 }}>
              {st.command && <div className="stack" style={{ gap: 4 }}><div className="row" style={{ justifyContent: "space-between" }}><span className="sm">Command</span><button className="btn sm ghost" type="button" onClick={() => navigator.clipboard?.writeText(st.command ?? "").catch(() => {})}><Icon id="i-copy" />Copy</button></div>
                <pre className="joblog sm">{st.command}</pre></div>}
              {sum && <p className="sm">Source: {sum}</p>}
              {st.check && <p className="sm">Picture check: Score {st.check.score.toFixed(3)}{st.check.min != null ? " (needs " + st.check.min.toFixed(3) + ")" : ""}{st.check.passed != null ? " · " + (st.check.passed ? "passed" : "failed") : ""}</p>}
              {st.outputLines > 0 && <details open={st.outcome !== "ok"}><summary className="sm">Show ffmpeg output ({st.outputLines} lines)</summary>
                {st.outputLines > 200 && <p className="sm"><a href={"/api/v2/jobs/" + encodeURIComponent(id) + "/log/download"}>Download all {st.outputLines.toLocaleString()} lines</a></p>}
                <pre className="joblog sm">{text(st.output)}</pre></details>}
              {st.result && <p className="sm">{st.result}</p>}
            </div>
          </details>);
      })}
      {(l.final != null && WORD[l.final]) && <p className="sm" style={{ borderTop: "1px solid var(--border)", paddingTop: 8 }}>{WORD[l.final]}</p>}</>}
    </section>);
}

function JobLog({ id, name }: { id: string; name: string }) {
  const [text, setText] = useState(""), [done, setDone] = useState(false), [gone, setGone] = useState(false), [q, setQ] = useState(""), [follow, setFollow] = useState(true);
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    setText(""); setDone(false); setGone(false);
    if (MOCK) { setText("01:12:03 started on This computer\n01:12:04 Converting video: HEVC, Balanced quality\n01:13:10 10% done · about 9 min left\n01:14:22 25% done · about 7 min left\n"); setDone(true); return; }
    const es = new EventSource("/api/v2/jobs/" + encodeURIComponent(id) + "/log/stream?from=0");
    es.addEventListener("job.log", (e) => { const d = JSON.parse((e as MessageEvent).data) as { text: string }; setText((t) => t + d.text); });
    es.addEventListener("end", () => { setDone(true); es.close(); });
    es.onerror = () => { if (es.readyState === EventSource.CLOSED) setGone(true); }; // a refused stream (404); a dropped one reconnects by itself
    return () => es.close();
  }, [id]);
  useEffect(() => { if (follow && box.current) box.current.scrollTop = box.current.scrollHeight; }, [text, follow]);
  const shown = q ? text.split("\n").filter((l) => l.toLowerCase().includes(q.toLowerCase())).join("\n") : text;
  const download = () => { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" })); a.download = name.replace(/\.[^.]+$/, "") + ".log"; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000); };
  return (
    <section><h3>Log</h3>
      <div className="row" style={{ gap: 6, marginBottom: 6 }}>
        <input className="input sm" type="search" placeholder="Search the log" aria-label="Search the log" value={q} onChange={(e) => setQ(e.target.value)} style={{ flex: 1, minWidth: 0 }} />
        {!done && <button className="btn sm ghost" type="button" aria-pressed={follow} onClick={() => setFollow(!follow)}>{follow ? "Stop following" : "Follow"}</button>}
        <button className="btn sm ghost" type="button" disabled={!text} onClick={download}><Icon id="i-download" />{q ? "Download the whole log" : "Download"}</button>
      </div>
      <pre className="joblog" ref={box} tabIndex={0} aria-label="Log" aria-live="off">{shown || (text ? "No line matches." : done ? "The log is empty." : gone ? "The log can't be shown." : "Waiting for the log...")}</pre>
      <p className="sm muted">{done ? "The log is complete." : gone ? "The server didn't send it. Close and open this file again to retry." : "Live: new lines appear as they are written."}</p>
    </section>
  );
}

const fmtV = (p: Probe | null | undefined) => {
  if (!p) return null;
  const v = p.streams?.find((s) => s.type === "video"), a = p.streams?.filter((s) => s.type === "audio") ?? [];
  return { video: [p.videoCodec?.toUpperCase().replace("H264", "H.264"), v?.Height ? v.Height + "p" : ""].filter(Boolean).join(" · ") || "-",
    audio: a.map((x) => x.codec.toUpperCase() + (x.channelLayout ? " " + x.channelLayout : "")).join(", ") || "None",
    bitrate: p.bitrateKbps ? count(p.bitrateKbps) + " kbps" : "-", size: p.sizeBytes ? bytes(p.sizeBytes) : "-" };
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
  const keptLine = kept && <>Replaced · kept until {new Date(kept.deleteOn).toLocaleDateString(undefined, { day: "numeric", month: "short" })} · <button ref={backBtn} className="linkbtn" type="button" aria-expanded={back} onClick={() => setBack(!back)}>Get original back</button></>;
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
  const before = fmtV(j?.metadata?.before), after = fmtV(j?.metadata?.after);
  const reason = j?.reason?.message ?? (row ? reasonText(row) : "");
  return (
    <div className="drawer-wrap" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="drawer jobd" ref={box} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="jd-h">
        <div className={"hd" + (state === "failed" ? " failed" : "")}>
          <button className="btn icon ghost" type="button" aria-label="Close" onClick={onClose}><Icon id="i-chev-l" /></button>
          <div className="sp"><h2 id="jd-h" className="clamp2" title={name}>{name}</h2>{state && <span style={{ alignSelf: "flex-start" }}><Pill j={{ state, retryAt: j?.retryAt ?? own?.retryAt, subState: own?.subState }} /></span>}</div>
          {/* SCREENS V6b header: Retry, Cancel or Stop, and the job ID to copy (the body keeps the rest) */}
          <div className="row nw" style={{ gap: 6, flexWrap: "wrap" }}>
            {(state === "failed" || state === "retry_wait") && <button className="btn sm" type="button" onClick={() => run("retry")}><Icon id="i-refresh" />{state === "retry_wait" ? "Try now" : "Retry"}</button>}
            {active && <button className="btn sm" type="button" onClick={() => run("cancel")}><Icon id="i-stop" />{state === "queued" || state === "retry_wait" ? "Cancel" : "Stop"}</button>}
            <button className="btn sm ghost" type="button" title="Copy the job ID" onClick={() => navigator.clipboard?.writeText(id).then(() => setSaid("Job ID " + id + " copied."), () => setSaid("Job ID: " + id))}><Icon id="i-copy" />ID {id}</button>
          </div>
        </div>
        <div className="bd">
          {(done && ca?.possible) || (state === "skipped" || state === "cancelled") ? <div className="row">
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
          {j?.removed && j.reason?.code === "removed" ? <div className="note"><Icon id="i-info" /><div>Its library was removed, so this file won't be converted.</div></div>
            : j?.failure ? <WhyFailed id={id} msg={reason} code={j.reason?.code ?? ""} worker={j.worker ?? null} f={j.failure} />
            : reason && <section><h3>Why</h3><p>{reason}</p></section>}
          <section><h3>File</h3><dl className="kv">
            {(own?.path ?? j?.file?.path) && <><dt>Source</dt><dd className="mono sm">{own?.path ?? j?.file?.path}</dd></>} {/* review 54ff228: no '-' when the server doesn't keep the path (a removed library's job) */}
            {(j?.library || own) && <><dt>Library</dt><dd>{j?.library?.name ?? own?.library}</dd></>}
            {own?.state === "completed" && <>{own.originalState !== "restored" && <><dt>Size</dt><dd>{saved(own.beforeBytes, own.afterBytes)}</dd></>} {/* review 66edc3d P3: after a restore the file is the original again: no saving */}
              <dt>Original</dt><dd>{kept ? keptLine : own.originalState === "kept" ? "Kept " + plural(own.keptDaysLeft ?? 0, "more day", "more days") + ", then deleted" : own.originalState === "removed" ? "Deleted after the keep period" : "Left in place"}</dd></>}
            {!own && kept && <><dt>Original</dt><dd>{keptLine}</dd></>}
          </dl>
          {others.length > 0 && <p className="sm" style={{ marginTop: 8 }}>This file's other jobs: {others.map((x, i) => <span key={x.jobId ?? x.id}>{i > 0 && " · "}<a className="linkbtn" href={"/jobs?tab=" + tabOf(x.state) + "&job=" + encodeURIComponent(x.jobId ?? x.id)} onClick={(e) => { if (e.button === 0 && !e.metaKey && !e.ctrlKey) { e.preventDefault(); navigate("/jobs?tab=" + tabOf(x.state) + "&job=" + encodeURIComponent(x.jobId ?? x.id)); }}}>{WORD[x.state] ?? x.state} {x.finishedAt ? new Date(x.finishedAt).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) : ""}</a></span>)}</p>}
        </section>
          {back && kept && <div role="alertdialog" aria-label="Get the original back?" style={{ padding: 12, border: "1px solid var(--border-strong)", borderRadius: "var(--radius)", background: "var(--raised)" }}
            onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), closeBack())}>
            <p style={{ margin: "0 0 8px" }}><strong>Get the original of “{name}” back?</strong> The original ({bytes(kept.originalBytes)}) goes back in place and the smaller version{kept.newPath && kept.newPath.split(/[\\/]/).pop() !== name ? ", " + kept.newPath.split(/[\\/]/).pop() + "," : ""} is deleted. This file won't be converted again unless you press Convert again.</p>
            <div className="row"><button className="btn primary" type="button" autoFocus onClick={getBack}>Get original back</button><button className="btn ghost" type="button" onClick={closeBack}>Cancel</button></div></div>}
          {j?.workflow && <section><h3>Workflow</h3><p>{own?.flow?.name ?? "Its library's workflow"} · v{j.workflow.version}</p></section>}
          {!scan && <section><h3>Timeline</h3>
            {!j ? <p className="muted">Loading...</p> : !j.steps.length && !j.events.length ? <p className="muted">No step history for jobs from before the update.</p> : <>
              {j.steps.length > 0 && <ol className="tline">{j.steps.map((s, i) => <li key={i} className={s.ok ? "" : "bad"}><Icon id={s.ok ? "i-check-c" : "i-x-c"} /><span className="sp">{s.words}</span><span className="muted sm nowrap">{clock(s.at)}</span></li>)}</ol>}
              {j.events.length > 0 && <p className="sm muted">History: {j.events.map((e) => WORD[e.to] + " " + clock(e.at)).join(" → ")}</p>}
            </>}
          </section>}
          {/* review db7b1f1 P2: the scan sentence only for a file a scan left out; a cancelled file whose job the server
              doesn't send yet claims nothing about scans or steps */}
          {scan && <section><h3>Timeline</h3><p className="muted">{state === "cancelled" ? "Cancelled." : "This file was left out when its library was scanned, so it has no job and no steps."}</p></section>}
          {!scan && (active ? <JobLog id={id} name={name} /> : <StepLog id={id} worker={j?.worker ?? null} failedIdx={j?.failure?.stepIndex ?? null} />)}
          {/* #148: before is the original; null for an older job that replaced a file of the same name: said so, never blank */}
          {(before || after) && <section><h3>Result</h3><table className="tbl kvt"><thead><tr><th /><th>Before</th><th>After</th></tr></thead><tbody>
            <tr><th>Video</th>{before ? <td>{before.video}</td> : <td rowSpan={4} className="muted">Not recorded for this job</td>}<td>{after?.video ?? "-"}</td></tr>
            <tr><th>Audio</th>{before && <td>{before.audio}</td>}<td>{after?.audio ?? "-"}</td></tr>
            <tr><th>Bitrate</th>{before && <td>{before.bitrate}</td>}<td>{after?.bitrate ?? "-"}</td></tr>
            <tr><th>Size</th>{before && <td>{before.size}</td>}<td>{after?.size ?? "-"}</td></tr></tbody></table></section>}
        </div>
      </aside>
    </div>
  );
}
