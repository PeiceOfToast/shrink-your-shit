// V6 Jobs (SCREENS V6; DESIGN/v2 mockups/jobs.html, jobs-drawer.html, jobs-drawer-done.html) on the real /api/v2/jobs
// (#113) with live updates from /api/v2/events (#118/#120/#122), or the sample jobs with ?mock=1. The server lists one
// tab at a time (Waiting, Running, Done, Skipped, Failed); All and Paused tabs and the codec/step/worker filters come
// when it has them. Not here yet: output path and Get original back (v2 has neither).
import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { MOCK, SignedOut, failed, type Answer } from "../api";
import { bytes, clock, count, elapsed, plural } from "../format";
import { convertDoneAgain, findSkip, getJob, jobAction, listJobs, listKept, restoreKept } from "../jobsApi";
import { getWorkflow } from "../workflowApi";
import { onLive } from "../live";
import { Empty, PageHead } from "../PageHead";
import { navigate, useSearch } from "../router";
import { useData } from "../useData";
import type { JobAction, JobDetail, JobItem, JobProgressEvent, JobState, Probe, Tab, Pin } from "../types/jobs";

const TABS: [Tab, string][] = [["waiting", "Waiting"], ["running", "Running"], ["completed", "Done"], ["skipped", "Skipped"], ["failed", "Failed"]];
// Spec states in words (SCREENS V6); a cancelled file lists under Skipped and reads "Cancelled".
const WORD: Record<JobState, string> = { queued: "Waiting", preparing: "Preparing", running: "Running", validating: "Checking", committing: "Saving",
  completed: "Done", skipped: "Skipped", failed: "Failed", cancelled: "Cancelled", retry_wait: "Retry" };
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
  const tab = (TABS.find(([t]) => t === search.get("tab"))?.[0] ?? "waiting") as Tab, open = search.get("job");
  const [q, setQ] = useState(""), [selecting, setSelecting] = useState(false), [sel, setSel] = useState<string[]>([]), [said, setSaid] = useState(""), [prio, setPrio] = useState<Pin | "none">("first");
  // each answer remembers its tab and search, so a tab switch never shows the last tab's rows while it loads
  const page = useData(() => listJobs(tab, q).then((r) => (failed(r) ? r : { ...r, for: tab + "|" + q })), [tab, q], 15000);
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
  const go = (t: Tab, job?: string | null) => navigate("/jobs?tab=" + t + (job ? "&job=" + encodeURIComponent(job) : ""));
  const current = page.data?.for === tab + "|" + q ? page.data : undefined;
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
        <div className="hd">
          <button className="btn icon ghost" type="button" aria-label="Close" onClick={onClose}><Icon id="i-chev-l" /></button>
          <div className="sp"><h2 id="jd-h" className="clamp2" title={name}>{name}</h2>{state && <Pill j={{ state, retryAt: j?.retryAt ?? own?.retryAt, subState: own?.subState }} />}</div>
        </div>
        <div className="bd">
          <div className="row">
            {(state === "failed" || state === "retry_wait") && <button className="btn sm" type="button" onClick={() => run("retry")}><Icon id="i-refresh" />{state === "retry_wait" ? "Try now" : "Retry"}</button>}
            {active && <button className="btn sm" type="button" onClick={() => run("cancel")}><Icon id="i-stop" />{state === "queued" || state === "retry_wait" ? "Cancel" : "Stop"}</button>}
            {/* a skipped file whose job is gone (a workflow's skip: the server says removed) can still be converted again (review f1aaf30) */}
            {done && ca?.possible && <button className="btn sm" type="button" aria-expanded={again} onClick={() => setAgain(!again)}><Icon id="i-arrow-r" />Convert again</button>}
            {(state === "skipped" || state === "cancelled") && (!j?.removed || !!own?.id.startsWith("skip-")) && <button className="btn sm" type="button" onClick={() => run("requeue")}><Icon id="i-arrow-r" />Convert again</button>}
            <span className="sp" /><button className="btn sm ghost" type="button" title="Copy the job ID" onClick={() => navigator.clipboard?.writeText(id).then(() => setSaid("Job ID " + id + " copied."), () => setSaid("Job ID: " + id))}><Icon id="i-copy" />ID {id}</button>
          </div>
          {done && ca && !ca.possible && ca.reason && <p className="muted sm">{ca.reason.message}</p>}
          {again && <div role="alertdialog" aria-label="Convert again?" style={{ padding: 12, border: "1px solid var(--border-strong)", borderRadius: "var(--radius)", background: "var(--raised)" }} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), setAgain(false))}>
            <p style={{ margin: "0 0 8px" }}>Convert {name} again{wf.data && !failed(wf.data) ? " with " + wf.data.name + (wf.data.version ? " v" + wf.data.version : "") : ""}? It starts from the original, never from the new file. The earlier new file is replaced once the new one passes its check.</p>
            <div className="row"><button className="btn sm primary" type="button" autoFocus onClick={runAgain}>Convert again</button><button className="btn sm ghost" type="button" onClick={() => setAgain(false)}>Cancel</button></div></div>}
          <p className="muted" role="status">{said}</p>
          {d.error && <p className="st bad" role="alert"><Icon id="i-alert" /><span>{d.error.message}</span></p>}
          {/* review db7b1f1 P3: "removed" is the server's flag, not a person's word. Only a job removed with its library
              gets a note; a job that ended as a skip shows its reason under Why like any other. */}
          {j?.removed && j.reason?.code === "removed" ? <div className="note"><Icon id="i-info" /><div>Its library was removed, so this file won't be converted.</div></div>
            : reason && <section><h3>Why</h3><p>{reason}</p></section>}
          <section><h3>File</h3><dl className="kv">
            {(own?.path ?? j?.file?.path) && <><dt>Source</dt><dd className="mono sm">{own?.path ?? j?.file?.path}</dd></>} {/* review 54ff228: no '-' when the server doesn't keep the path (a removed library's job) */}
            {(j?.library || own) && <><dt>Library</dt><dd>{j?.library?.name ?? own?.library}</dd></>}
            {own?.state === "completed" && <>{own.originalState !== "restored" && <><dt>Size</dt><dd>{saved(own.beforeBytes, own.afterBytes)}</dd></>} {/* review 66edc3d P3: after a restore the file is the original again: no saving */}
              <dt>Original</dt><dd>{kept ? keptLine : own.originalState === "kept" ? "Kept " + plural(own.keptDaysLeft ?? 0, "more day", "more days") + ", then deleted" : own.originalState === "removed" ? "Deleted after the keep period" : "Left in place"}</dd></>}
            {!own && kept && <><dt>Original</dt><dd>{keptLine}</dd></>}
          </dl></section>
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
          {!scan && <JobLog id={id} name={name} />}
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
