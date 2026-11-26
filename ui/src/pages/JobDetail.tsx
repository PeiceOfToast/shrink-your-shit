import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, type Job, type JobDetail as Detail, type LogLine, type LogStep, type MediaRow, type StepOutcome } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { Icon } from "../ui/Icon.tsx";
import { useKeys } from "../ui/useKeys.ts";
import { Link, navigate, useLocation } from "../router.tsx";
import { PILL } from "./Jobs.tsx";

// The job panel over the Jobs list (page 03). Opens at /jobs/:id; /jobs/:id/log opens the Log tab.
type Tab = "summary" | "log" | "steps" | "before" | "history";
const TABS: { key: Tab; label: string }[] = [
  { key: "summary", label: "Summary" },
  { key: "log", label: "Log" },
  { key: "steps", label: "Steps" },
  { key: "before", label: "Before and after" },
  { key: "history", label: "History" },
];

const OUTCOME: Record<StepOutcome, { label: string; cls: string }> = {
  done: { label: "Done", cls: "ok" },
  nothing: { label: "Nothing to do", cls: "" },
  failed: { label: "Failed", cls: "bad" },
  running: { label: "Running", cls: "" },
  skipped: { label: "Skipped", cls: "" },
  waiting: { label: "Waiting", cls: "" },
};

function fmtMs(ms: number | null): string {
  if (ms == null) return "—";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// The text of the whole panel, for Copy all and the .txt download.
function logText(d: Detail): string {
  const head = `${d.job.file}\n${d.path}\n${d.job.result}\n`;
  const history = d.history.map((h) => `${h.time} ${h.text}`).join("\n");
  if (!d.logs) return `${head}\nThe detailed log is no longer kept.\n\nHistory\n${history}\n`;
  const steps = d.logs.map((s) =>
    [
      `${s.index}. ${s.name} · ${OUTCOME[s.outcome].label}`,
      `   ${s.decision}`,
      ...(s.command ? [`   $ ${s.command}`] : []),
      ...s.lines.map((l) => `   ${l.time} ${l.level.toUpperCase()} ${l.text}`),
    ].join("\n"),
  );
  return `${head}\n${steps.join("\n\n")}\n\nHistory\n${history}\n`;
}

export function JobDetail({ id, initialTab }: { id: string; initialTab: "summary" | "log" }) {
  const { query } = useLocation();
  const listState = query.get("state");
  const filterQs = listState ? `?state=${listState}` : "";
  const detail = useApi(() => api.getJob(id), [id]);
  const nb = useApi(() => api.getJobNeighbours(id, listState ?? "all"), [id, listState]);
  // The tab the reader picked stays put while ‹ › walks the list. Until they pick one, failed and running jobs open on Log.
  const [picked, setPicked] = useState<Tab | null>(null);
  const [confirm, setConfirm] = useState<"again" | "cancel" | null>(null);
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const d = detail.data;

  useEffect(() => {
    titleRef.current?.focus();
  }, [id]);

  const close = () => navigate(`/jobs${filterQs}`);
  useKeys((e) => {
    if (e.key === "Escape") close();
  });

  const fallback: Tab = initialTab === "log" || (d && (d.job.state === "failed" || d.job.state === "running")) ? "log" : "summary";
  const tab = picked ?? fallback;
  const go = (to: string | null) => {
    if (to) navigate(`/jobs/${to}${filterQs}`);
  };

  const act = async (run: () => Promise<unknown>, done: string) => {
    setBusy(true);
    setMsg("");
    try {
      await run();
      setConfirm(null);
      setMsg(done);
      detail.reload();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Something went wrong. Try again.");
    } finally {
      setBusy(false);
    }
  };

  const downloadLog = () => {
    if (!d) return;
    const url = URL.createObjectURL(new Blob([logText(d)], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `job-${d.job.id}-log.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const copyAll = async () => {
    if (!d) return;
    const ok = await copyText(logText(d));
    setMsg(ok ? "Copied the whole log." : "Couldn't copy here. Select the text in the log instead.");
  };

  const job: Job | undefined = d?.job;
  const state = job?.state;

  return (
    <>
      <div className="scrim" onClick={(e) => e.target === e.currentTarget && close()} />
      <aside className="ov panel jd" role="dialog" aria-modal="true" aria-labelledby="jd-title">
        <header className="ovh">
          <div style={{ minWidth: 0, flex: "1 1 260px" }}>
            <h2 id="jd-title" ref={titleRef} tabIndex={-1} style={{ overflowWrap: "anywhere", outline: "none" }}>
              {job ? job.file : "Job"}
            </h2>
            {d && <div className="muted xs mono" style={{ overflowWrap: "anywhere" }}>{d.path}</div>}
          </div>
          {job && (
            <>
              <span className={`pill ${PILL[job.state].cls}`}>{PILL[job.state].label}</span>
              <span className={`sm ${job.resultTone}`}>{job.result}</span>
            </>
          )}
          <span className="sp" style={{ flex: 1 }} />
          <button type="button" className="btn ghost sm" aria-label="Previous job" disabled={!nb.data?.prev} onClick={() => go(nb.data?.prev ?? null)}>‹</button>
          <button type="button" className="btn ghost sm" aria-label="Next job" disabled={!nb.data?.next} onClick={() => go(nb.data?.next ?? null)}>›</button>
          {state === "failed" && (
            <button type="button" className="btn sm" disabled={busy} onClick={() => act(() => api.runJobsAction("retry", [id]), "Queued to try again.")}>Retry</button>
          )}
          {(state === "done" || state === "skipped") && (
            <button type="button" className="btn sm" disabled={busy} onClick={() => setConfirm("again")}>Run again</button>
          )}
          {(state === "running" || state === "waiting") && (
            <button type="button" className="btn sm" disabled={busy} onClick={() => setConfirm("cancel")}>Cancel</button>
          )}
          <button type="button" className="btn ghost sm" disabled={!d} onClick={downloadLog}>Download log .txt</button>
          <button type="button" className="btn ghost sm" disabled={!d} onClick={copyAll}>Copy all</button>
          <Link to="/workflows" className="btn ghost sm">Open in workflow</Link>
          <button type="button" className="btn ghost sm" aria-label="Close" onClick={close}>
            <Icon name="x" />
          </button>
        </header>

        <div className="ovb">
          {confirm === "again" && job && (
            <div className="jd-confirm" role="alert">
              <span>Run this file through {job.workflow} again? The current file is treated as the original.</span>
              <button type="button" className="btn primary sm" disabled={busy} onClick={() => act(() => api.convertAgain(id), "Queued again. This panel now shows the new attempt.")}>Run again</button>
              <button type="button" className="btn sm" onClick={() => setConfirm(null)}>Keep it</button>
            </div>
          )}
          {confirm === "cancel" && (
            <div className="jd-confirm" role="alert">
              <span>Stop converting? The original stays as it is.</span>
              <button type="button" className="btn primary sm" disabled={busy} onClick={() => act(() => api.runJobsAction("cancel", [id]), "Stopped.")}>Stop</button>
              <button type="button" className="btn sm" onClick={() => setConfirm(null)}>Keep going</button>
            </div>
          )}
          {msg && <p className="hint" role="status" style={{ margin: 0 }}>{msg}</p>}

          {detail.error && !d && (
            <p className="bad">
              {detail.error} <button type="button" className="btn ghost sm" onClick={detail.reload}>Try now</button>
            </p>
          )}
          {!d && !detail.error && <span className="skel" />}

          {d && (
            <>
              <nav className="tabs" role="tablist" aria-label="Job sections">
                {TABS.map((t) => (
                  <button key={t.key} type="button" role="tab" aria-selected={tab === t.key} onClick={() => setPicked(t.key)}>
                    {t.label}
                  </button>
                ))}
              </nav>
              {tab === "summary" && <SummaryTab d={d} busy={busy} onRetry={() => act(() => api.runJobsAction("retry", [id]), "Queued to try again.")} />}
              {tab === "log" && <LogTab d={d} />}
              {tab === "steps" && <StepsTab d={d} />}
              {tab === "before" && <BeforeTab rows={d.media} />}
              {tab === "history" && <HistoryTab d={d} />}
            </>
          )}
        </div>
      </aside>
    </>
  );
}

function Fact({ label, value }: { label: string; value: ReactNode }) {
  return (
    <span>
      <span>{label}</span>
      <span className="tnum" style={{ textAlign: "right", overflowWrap: "anywhere" }}>{value}</span>
    </span>
  );
}

function SummaryTab({ d, busy, onRetry }: { d: Detail; busy: boolean; onRetry: () => void }) {
  return (
    <>
      {d.failure && (
        <div className="jd-fail" role="status">
          <b>Failed at {d.failure.step}</b>
          <span>{d.failure.reason}</span>
          <span className="muted sm">{d.failure.nextAction}</span>
          <div>
            <button type="button" className="btn sm" disabled={busy} onClick={onRetry}>{d.failure.actionLabel}</button>
          </div>
        </div>
      )}
      {d.skipReason && <p className="muted" style={{ margin: 0 }}>Skipped: {d.skipReason}</p>}

      <div className="jd-cards">
        <section className="card">
          <h3>File</h3>
          <div>
            <Fact label="Original" value={d.original} />
            <Fact label="Size" value={`${d.sizeBefore} → ${d.sizeAfter ?? "—"}`} />
            <Fact label="Saved" value={d.savedPct != null ? `${d.savedPct} %` : "—"} />
          </div>
        </section>
        <section className="card">
          <h3>Run</h3>
          <div>
            <Fact label="Workflow" value={d.workflow} />
            <Fact label="Worker" value={d.worker} />
            <Fact label="Attempt" value={d.attempt} />
            <Fact label="Started" value={d.startedAt ?? "—"} />
            <Fact label="Took" value={d.took ?? "—"} />
          </div>
        </section>
        <section className="card">
          <h3>Result</h3>
          <div>
            <Fact label="State" value={PILL[d.job.state].label} />
            <Fact label="Step" value={d.job.step} />
            <Fact label="Progress" value={d.job.progress != null ? `${d.job.progress} %` : "—"} />
            <Fact label="Result" value={d.job.result} />
          </div>
        </section>
      </div>

      <section className="card" style={{ padding: 0 }}>
        <table className="tbl">
          <thead>
            <tr>
              <th>#</th>
              <th>Step</th>
              <th>Outcome</th>
              <th className="num">Time</th>
            </tr>
          </thead>
          <tbody>
            {d.steps.slice(0, 8).map((s) => (
              <tr key={s.index}>
                <td className="tnum muted">{s.index}</td>
                <td>{s.name}</td>
                <td><span className={OUTCOME[s.outcome].cls}>{OUTCOME[s.outcome].label}</span></td>
                <td className="num tnum">{fmtMs(s.durationMs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </>
  );
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button type="button" className="btn ghost sm" style={{ alignSelf: "flex-start" }} onClick={async () => setDone(await copyText(text))}>
      {done ? "Copied" : label}
    </button>
  );
}

function LogTab({ d }: { d: Detail }) {
  const [q, setQ] = useState("");
  const [warnOnly, setWarnOnly] = useState(false);
  const [details, setDetails] = useState(false);
  if (!d.logs) {
    return <p className="muted">The detailed log is no longer kept. The steps and history are still here.</p>;
  }
  const needle = q.trim().toLowerCase();
  const keep = (l: LogLine) =>
    (details || l.level !== "detail") &&
    (!warnOnly || l.level === "warn" || l.level === "error") &&
    (!needle || l.text.toLowerCase().includes(needle));
  const sections = d.logs.map((s: LogStep) => ({ s, lines: s.lines.filter(keep) })).filter((x) => x.lines.length > 0);

  return (
    <>
      <div className="row">
        <input
          className="inp"
          type="search"
          placeholder="Search the log"
          aria-label="Search the log"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          style={{ maxWidth: 280 }}
        />
        <label className="sm"><input type="checkbox" checked={warnOnly} onChange={(e) => setWarnOnly(e.target.checked)} /> Warnings and errors only</label>
        <label className="sm"><input type="checkbox" checked={details} onChange={(e) => setDetails(e.target.checked)} /> Show details</label>
      </div>
      {d.logs.length === 0 && <p className="muted">Not started yet. The log fills in once a worker picks the file up.</p>}
      {d.logs.length > 0 && sections.length === 0 && <p className="muted">Nothing matches.</p>}
      {sections.map(({ s, lines }) => (
        <section className="log-step" key={s.index}>
          <header>
            <b>{s.index}. {s.name}</b>
            <span className={OUTCOME[s.outcome].cls}>{OUTCOME[s.outcome].label}</span>
            <span className="sp" />
            <span className="muted sm tnum">{fmtMs(s.durationMs)}</span>
          </header>
          <span className="muted sm">{s.decision}</span>
          {s.command && (
            <>
              <pre>{s.command}</pre>
              <CopyButton text={s.command} label="Copy command" />
            </>
          )}
          <div className="log">
            {lines.map((l, i) => (
              <div key={i} className={`ln ${l.level}`}>
                <span className="t">{l.time}</span>
                <span>{l.text}</span>
              </div>
            ))}
          </div>
        </section>
      ))}
    </>
  );
}

function StepsTab({ d }: { d: Detail }) {
  return (
    <>
      <p className="hint" style={{ margin: 0 }}>The flow view of these steps isn't built yet. The list is here.</p>
      <table className="tbl">
        <thead>
          <tr>
            <th>#</th>
            <th>Step</th>
            <th>Outcome</th>
            <th className="num">Time</th>
          </tr>
        </thead>
        <tbody>
          {d.steps.map((s) => (
            <tr key={s.index}>
              <td className="tnum muted">{s.index}</td>
              <td>{s.name}</td>
              <td><span className={OUTCOME[s.outcome].cls}>{OUTCOME[s.outcome].label}</span></td>
              <td className="num tnum">{fmtMs(s.durationMs)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

function BeforeTab({ rows }: { rows: MediaRow[] }) {
  const [changed, setChanged] = useState(false);
  const shown = changed ? rows.filter((r) => r.changed) : rows;
  return (
    <>
      <label className="sm"><input type="checkbox" checked={changed} onChange={(e) => setChanged(e.target.checked)} /> Changes only</label>
      <table className="tbl">
        <thead>
          <tr>
            <th>Group</th>
            <th>Field</th>
            <th>Before</th>
            <th>After</th>
          </tr>
        </thead>
        <tbody>
          {shown.map((r, i) => (
            <tr key={i}>
              <td className="muted">{r.group}</td>
              <td>{r.field}</td>
              <td>{r.before}</td>
              <td>{r.after === null ? "—" : r.changed ? <b>{r.after}</b> : r.after}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

function HistoryTab({ d }: { d: Detail }) {
  return (
    <div className="log">
      {d.history.map((h, i) => (
        <div key={i} className={`ln ${h.level}`}>
          <span className="t">{h.time}</span>
          <span>{h.text}</span>
        </div>
      ))}
    </div>
  );
}
